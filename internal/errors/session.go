package errors

import (
	"fmt"
	"net/http"
	"time"
)

// SessionExpiredError is returned when the saved LMS session cookies
// (d2lSessionVal etc.) are no longer honoured by D2L. Distinct from login-flow
// errors (SamlConsumedError, AdfsRejectedError, MfaRequiredError) — those fire
// during a fresh `schooltools login`. This one fires during any subsequent
// invocation that reads session.json from disk and tries to use it.
//
// Concretely: D2L returns either a tiny JS-redirect to /d2l/login?sessionExpired=1
// or a 302 there. We map both to this error so callers can translate it into
// "your saved session is dead; run `schooltools login`" without inspecting
// URLs.
type SessionExpiredError struct {
	URL           string        // final URL D2L landed us on after detecting expiry
	LastAccess    time.Time     // when this cookie jar last successfully talked to D2L (zero if unknown)
	LastAccessSet bool          // true when LastAccess is meaningful
	ProbeAt       time.Time     // when we detected the expiry
}

func (e *SessionExpiredError) Error() string {
	return fmt.Sprintf("LMS session expired at %s (last access: %s). "+
		"Run `schooltools login` to re-authenticate.",
		e.ProbeAt.UTC().Format(time.RFC3339),
		formatLastAccess(e.LastAccess, e.LastAccessSet),
	)
}

// AuthCode is the stable machine-readable code for this failure. Downstream
// callers (authlog, JSON output) can rely on this instead of string-matching
// the error message.
func (e *SessionExpiredError) AuthCode() string { return "SESSION_EXPIRED" }

// IsSessionExpired reports whether err is — or wraps — a SessionExpiredError.
// Use this in callers rather than a direct type assertion, so wrapped errors
// (errors.Wrap from external packages, fmt.Errorf with %w) still resolve.
func IsSessionExpired(err error) bool {
	if err == nil {
		return false
	}
	for cur := err; cur != nil; {
		if _, ok := cur.(*SessionExpiredError); ok {
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := cur.(unwrap)
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}

// SessionProbeResult is the structured outcome of a heartbeat probe against
// /d2l/home. Used by internal/login.Heartbeat and surfaced in verbose logs.
type SessionProbeResult struct {
	OK         bool          // true if the probe succeeded (no 302 to login, valid HTML)
	URL        string        // final URL after any redirects
	Status     int           // HTTP status of the final response
	BodyBytes  int           // size of the response body
	Latency    time.Duration // request round-trip
	DetectedAt time.Time     // probe completion time
	Expired    bool          // true if the server signalled sessionExpired
}

// formatLastAccess renders "last access" without leaking zero-time strings.
func formatLastAccess(t time.Time, set bool) string {
	if !set || t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format(time.RFC3339) + " (≈" + time.Since(t).Truncate(time.Minute).String() + " ago)"
}

// ProbeExpiredURL inspects a response URL for the sessionExpired marker D2L
// appends when redirecting to its login page.
func ProbeExpiredURL(rawURL string) bool {
	return rawURL != "" && (contains(rawURL, "sessionExpired=1") || contains(rawURL, "/d2l/login"))
}

// contains is a tiny helper that avoids the strings.Contains dependency when
// callers use this from hot paths.
func contains(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// IsExpiredStatus returns true for HTTP statuses that suggest the session is
// no longer honoured. D2L's typical signal is 302 to /d2l/login, but we also
// catch 401/403 since some auth-gated paths use those.
func IsExpiredStatus(status int) bool {
	return status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status == http.StatusFound ||
		status == http.StatusMovedPermanently
}
