package cmd

import (
	"fmt"
	"net/http/cookiejar"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/d2lver"
	"github.com/aarav/schooltools/internal/env"
	"github.com/aarav/schooltools/internal/login"
	"github.com/aarav/schooltools/internal/session"
	stderrors "github.com/aarav/schooltools/internal/errors"
)

var (
	loginEnvFile string
	loginVerbose bool
	loginKMSI    bool
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate via ADFS SAML and persist the session cookies",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := env.LoadEnv(loginEnvFile); err != nil {
			return err
		}
		_ = authlog.Log("login.start", map[string]any{"kmsi": loginKMSI, "env_file": loginEnvFile})

		res, err := login.Run(login.Options{EnvFile: loginEnvFile, Verbose: loginVerbose, KMSI: loginKMSI, CaptureSAML: true})
		if err != nil {
			code := "LOGIN_ERROR"
			type coder interface{ AuthCode() string }
			if c, ok := err.(coder); ok {
				code = c.AuthCode()
			}
			_ = authlog.Log("login.failure", map[string]any{"code": code, "message": err.Error()})
			return err
		}
		if err := session.Save(res.Cookies); err != nil {
			return err
		}
		// Invalidate any persisted API-version cache — a new login might
		// land on a tenant that was upgraded between sessions, and
		// re-discovery is cheap if it does.
		d2lver.Reset()
		_ = authlog.LogCookies("login.success", summariseForLog(res.Cookies))
		_ = authlog.Log("session.saved", map[string]any{"count": len(res.Cookies)})

		fmt.Fprintf(os.Stderr, "Saved %d cookie(s) from the SAML flow.\n", len(res.Cookies))

		// Heartbeat-after-login: verify the saved cookies are actually
		// accepted by D2L. Catches the case where SAML appears to succeed
		// but D2L has already invalidated the session server-side. If the
		// heartbeat fails we log it and warn the user — we don't fail the
		// login itself because the cookies are still saved and may recover.
		verifySavedSession(res.Cookies)

		if res.SAML != nil {
			if err := session.SaveSAML(*res.SAML); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save captured SAML token: %v\n", err)
			} else {
				_ = authlog.Log("saml.captured", map[string]any{
					"path":          session.SAMLPath(),
					"not_on_or_after": res.SAML.NotOnOrAfter,
				})
				if !res.SAML.NotOnOrAfter.IsZero() {
					fmt.Fprintf(os.Stderr, "Captured SAMLResponse (valid until %s) -> %s\n",
						res.SAML.NotOnOrAfter.Format(time.RFC3339), session.SAMLPath())
				} else {
					fmt.Fprintf(os.Stderr, "Captured SAMLResponse -> %s\n", session.SAMLPath())
				}
			}
		}
		if res.Token != nil {
			if err := session.SaveToken(*res.Token); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save captured Brightspace token: %v\n", err)
			} else {
				_ = authlog.Log("token.captured", map[string]any{
					"path":          session.TokenPath(),
					"expires_at":    res.Token.ExpiresAt,
					"user_id":       res.Token.UserID,
					"tenant_id":     res.Token.TenantID,
				})
				if !res.Token.ExpiresAt.IsZero() {
					fmt.Fprintf(os.Stderr, "Captured Brightspace token (valid until %s, user %s, tenant %s) -> %s\n",
						res.Token.ExpiresAt.Format(time.RFC3339), res.Token.UserID, res.Token.TenantID, session.TokenPath())
				} else {
					fmt.Fprintf(os.Stderr, "Captured Brightspace token -> %s\n", session.TokenPath())
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "Brightspace token: not captured (LE APIs will still work on session cookies).")
		}
		return nil
	},
}

func init() {
	loginCmd.Flags().StringVarP(&loginEnvFile, "env-file", "e", ".env", "Path to .env file")
	loginCmd.Flags().BoolVarP(&loginVerbose, "verbose", "v", false, "Print each hop URL and status")
	loginCmd.Flags().BoolVar(&loginKMSI, "kmsi", true, "Send KMSI to extend ADFS lifetime")
	rootCmd.AddCommand(loginCmd)
}

func summariseForLog(records []cookies.Record) []authlog.CookieSummary {
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

// verifySavedSession runs a heartbeat against D2L using the freshly-saved
// cookies. On success: logs session.heartbeat.ok and prints a confirmation.
// On failure: logs the failure with full context and prints a clear warning
// that the cookies may already be invalid — without failing the login itself.
func verifySavedSession(records []cookies.Record) {
	jar, err := session.LoadJar()
	if err != nil || jar == nil {
		// If we can't load the jar we just saved, something is very wrong.
		fmt.Fprintln(os.Stderr, "warning: post-login verification skipped (couldn't load session jar)")
		_ = authlog.Log("login.verify.skipped", map[string]any{"reason": "load_failed", "error": fmt.Sprint(err)})
		return
	}
	res, err := session.Heartbeat(jar)
	if err == nil {
		_ = authlog.Log("login.verify.ok", map[string]any{
			"url":        res.URL,
			"status":     res.Status,
			"latency_ms": res.Latency.Milliseconds(),
		})
		fmt.Fprintf(os.Stderr, "Verified: D2L accepted the new session (%s, %dms).\n", res.URL, res.Latency.Milliseconds())
		return
	}
	kind := "transport_error"
	if stderrors.IsSessionExpired(err) {
		kind = "session_expired"
	}
	_ = authlog.Log("login.verify.failed", map[string]any{
		"kind":  kind,
		"url":   res.URL,
		"error": err.Error(),
	})
	fmt.Fprintln(os.Stderr, "warning: post-login verification FAILED — D2L did not accept the saved session cookies.")
	fmt.Fprintf(os.Stderr, "  %s\n", err)
	fmt.Fprintln(os.Stderr, "  The cookies are still saved; the next heartbeat will re-check.")
}

// keep imports used in case of future edits
var _ = cookiejar.New