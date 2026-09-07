package session

import (
	"time"

	"github.com/aarav/schooltools/internal/cookies"
)

// CookieExpiry describes one session cookie's expiry in human-readable form.
type CookieExpiry struct {
	Name        string
	Domain      string
	Expires     time.Time // zero for session-only cookies
	IsSession   bool      // true when Expires is zero
	IsExpired   bool
	Remaining   time.Duration // zero for session-only or expired
}

// Expiry is the aggregate view of session lifetime derived from persisted
// cookies plus the most recently captured SAML assertion.
type Expiry struct {
	Soonest       time.Time         // earliest non-zero Expires across session cookies; zero if none
	SoonestName   string            // name of the cookie driving Soonest
	HasSession    bool              // true if any cookie is session-only (no Expires set)
	PerCookie     []CookieExpiry    // one entry per persisted session cookie
	SAMLNotAfter  time.Time         // from saml-last.json when available
	SAMLAvailable bool              // true when SAMLNotAfter was populated
	Now           time.Time         // when this snapshot was computed
}

// SessionExpiry inspects the persisted session cookies (and, if present, the
// captured SAML token) and returns the soonest forced re-login horizon.
func SessionExpiry() (Expiry, error) {
	records, err := Load()
	if err != nil {
		return Expiry{}, err
	}
	samlNotAfter, _ := loadSAMLNotAfter()
	return ComputeExpiryForTest(records, samlNotAfter, time.Now()), nil
}

func loadSAMLNotAfter() (time.Time, bool) {
	r, err := LoadSAML()
	if err != nil || r == nil {
		return time.Time{}, false
	}
	return r.NotOnOrAfter, !r.NotOnOrAfter.IsZero()
}

// ComputeExpiryForTest is the pure computation, separated so tests can pin
// "now" without monkey-patching the clock. Production code should call
// SessionExpiry.
func ComputeExpiryForTest(records []cookies.Record, samlNotAfter time.Time, now time.Time) Expiry {
	out := Expiry{Now: now, SAMLNotAfter: samlNotAfter}
	out.SAMLAvailable = !samlNotAfter.IsZero()
	for _, r := range records {
		c := CookieExpiry{
			Name:   r.Name,
			Domain: r.Domain,
		}
		if r.Expires.IsZero() {
			c.IsSession = true
			c.IsExpired = false
			out.HasSession = true
			out.PerCookie = append(out.PerCookie, c)
			continue
		}
		c.Expires = r.Expires
		c.IsExpired = !r.Expires.After(now)
		if !c.IsExpired {
			c.Remaining = r.Expires.Sub(now)
			if out.Soonest.IsZero() || r.Expires.Before(out.Soonest) {
				out.Soonest = r.Expires
				out.SoonestName = r.Name
			}
		}
		out.PerCookie = append(out.PerCookie, c)
	}
	return out
}
