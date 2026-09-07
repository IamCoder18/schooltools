package session

import (
	"fmt"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/d2lver"
	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/aarav/schooltools/internal/env"
)

// EnsureOptions bundles flags used by every authenticated command.
type EnsureOptions struct {
	EnvFile     string
	AutoRefresh bool
	KMSI        bool
	// SkipHeartbeat disables the /d2l/home server-side probe. Off by default;
	// commands that already do their own authenticated GET (archive, whoami)
	// can set this to avoid the extra round-trip when the response will tell
	// them anyway.
	SkipHeartbeat bool
	// LoginFn is the dependency-injected login runner; cmd/login supplies it so
	// this package doesn't import internal/login (avoiding a cycle).
	LoginFn func(envFile string, kmsi bool) ([]cookies.Record, error)
	// Silent suppresses the user-visible stderr summary (auto-refresh
	// messages, "saved session is invalid" warnings). Off by default —
	// any session-invalidation event is worth surfacing because users
	// otherwise see only the downstream command failure with no context.
	// Hidden commands that already print their own diagnostic set this
	// to true.
	Silent bool
}

// EnsureResult captures what the caller needs after EnsureSession runs.
type EnsureResult struct {
	Jar           *cookiejar.Jar
	Reauthed      bool    // true if we re-ran login because the saved session was invalid
	InvalidReason string // set when the saved session was missing / expired / etc.
}

// EnsureSession loads the persisted cookies, returns a ready cookie jar, and
// (when autoRefresh is set) re-runs login if the saved session is invalid.
// Logs session.invalid / reauth.* events as side effects.
//
// When saved cookies are present, EnsureSession also probes /d2l/home via
// the heartbeat helper so that server-side session timeouts (which our local
// expiry check can't detect) surface as a clean SessionExpiredError rather
// than a 302 buried inside a downstream command's response.
func EnsureSession(opts EnsureOptions) (EnsureResult, error) {
	records, err := Load()
	if err != nil {
		return EnsureResult{}, err
	}
	if len(records) > 0 && HasValidSession(records) {
		jar, err := LoadJar()
		if err != nil {
			return EnsureResult{}, err
		}
		// Run API-version discovery against the freshly-loaded jar so every
		// command this session runs talks to the highest version CBE
		// supports. Best-effort — failures fall back to the hardcoded
		// defaults and never block the caller.
		v := d2lver.Get(jar)
		_ = authlog.Log("api.versions.discovered", map[string]any{
			"source": v.Source,
			"lp":     v.LP,
			"le":     v.LE,
			"bas":    v.BAS,
			"ep":     v.EP,
		})
		_ = authlog.Log("session.loaded", map[string]any{"count": len(records)})
		if !opts.SkipHeartbeat {
			if err := HeartbeatWithLog(jar); err != nil {
				// Heartbeat failed. Decide whether to re-auth or surface the error.
				hbErr := err
				reason := "server_expired"
				if stderrors.IsSessionExpired(err) {
					reason = "session_expired"
				}
				_ = authlog.Log("session.invalid", map[string]any{"reason": reason})
				if !opts.AutoRefresh {
					return EnsureResult{InvalidReason: reason}, err
				}
				// Fall through to re-auth path below. Preserve the heartbeat
				// error so it lands in the next auth.log entry if the reauth
				// itself fails — gives us a record of both signals.
				if err := env.LoadEnv(opts.EnvFile); err != nil {
					return EnsureResult{}, err
				}
				_ = authlog.Log("reauth.start", map[string]any{
					"reason":         reason,
					"kmsi":           opts.KMSI,
					"heartbeat_error": hbErr.Error(),
				})
				if opts.LoginFn == nil {
					return EnsureResult{InvalidReason: reason},
						fmt.Errorf("auto-refresh requested but no login function supplied (heartbeat: %w)", hbErr)
				}
				kmsi := opts.KMSI
				if !kmsi {
					kmsi = true
				}
				newRecords, lerr := opts.LoginFn(opts.EnvFile, kmsi)
				if lerr != nil {
					_ = authlog.Log("reauth.failure", map[string]any{
						"code":           "LOGIN_ERROR",
						"message":        lerr.Error(),
						"heartbeat_error": hbErr.Error(),
					})
					return EnsureResult{InvalidReason: reason}, lerr
				}
				if err := Save(newRecords); err != nil {
					return EnsureResult{}, err
				}
				_ = authlog.LogCookies("reauth.success", summarise(newRecords))
				jar, err := LoadJar()
				if err != nil {
					return EnsureResult{}, err
				}
				d2lver.Get(jar)
				if !opts.Silent {
					fmt.Fprintf(os.Stderr,
						"[schooltools] saved session was %s; auto-refreshed via ADFS SAML (cookies: %s). Run `auth log` for the event trail.\n",
						reason, summariseCookieNames(newRecords))
				}
				return EnsureResult{Jar: jar, Reauthed: true, InvalidReason: reason}, nil
			}
		}
		return EnsureResult{Jar: jar}, nil
	}
	reason := "all_expired"
	if len(records) == 0 {
		reason = "missing"
	}
	_ = authlog.Log("session.invalid", map[string]any{"reason": reason})
	if !opts.AutoRefresh {
		if len(records) == 0 {
			if !opts.Silent {
				fmt.Fprintln(os.Stderr,
					"[schooltools] no saved session; run `schooltools login` first.")
			}
			return EnsureResult{InvalidReason: reason},
				fmt.Errorf("no saved session. Run 'schooltools login' first.")
		}
		names := make([]string, len(records))
		for i, r := range records {
			names[i] = r.Name
		}
		if !opts.Silent {
			fmt.Fprintf(os.Stderr,
				"[schooltools] saved session cookies are all expired (%s); re-run `schooltools login` or pass --auto-refresh.\n",
				strings.Join(names, ", "))
		}
		return EnsureResult{InvalidReason: reason},
			fmt.Errorf("saved session cookies are all expired: %s", strings.Join(names, ", "))
	}
	if err := env.LoadEnv(opts.EnvFile); err != nil {
		return EnsureResult{}, err
	}
	_ = authlog.Log("reauth.start", map[string]any{"reason": reason, "kmsi": opts.KMSI})
	if opts.LoginFn == nil {
		return EnsureResult{InvalidReason: reason},
			fmt.Errorf("auto-refresh requested but no login function supplied")
	}
	kmsi := opts.KMSI
	if !kmsi {
		kmsi = true
	}
	newRecords, err := opts.LoginFn(opts.EnvFile, kmsi)
	if err != nil {
		_ = authlog.Log("reauth.failure", map[string]any{"code": "LOGIN_ERROR", "message": err.Error()})
		return EnsureResult{InvalidReason: reason}, err
	}
	if err := Save(newRecords); err != nil {
		return EnsureResult{}, err
	}
	_ = authlog.LogCookies("reauth.success", summarise(newRecords))
	jar, err := LoadJar()
	if err != nil {
		return EnsureResult{}, err
	}
	d2lver.Get(jar)
	if !opts.Silent {
		fmt.Fprintf(os.Stderr,
			"[schooltools] saved session was %s; auto-refreshed via ADFS SAML (cookies: %s). Run `auth log` for the event trail.\n",
			reason, summariseCookieNames(newRecords))
	}
	return EnsureResult{Jar: jar, Reauthed: true, InvalidReason: reason}, nil
}

// summariseCookieNames returns just the cookie names from a record set,
// for the user-visible "what got saved" line. Avoids printing the
// secret cookie values.
func summariseCookieNames(records []cookies.Record) string {
	names := make([]string, 0, len(records))
	for _, r := range records {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}

func summarise(records []cookies.Record) []authlog.CookieSummary {
	out := make([]authlog.CookieSummary, len(records))
	for i, r := range records {
		var t *time.Time
		if !r.Expires.IsZero() {
			v := r.Expires.UTC()
			t = &v
		}
		out[i] = authlog.CookieSummary{Name: r.Name, Domain: r.Domain, Expires: t}
	}
	return out
}