package cookies

import (
	"net/http"
	"time"
)

// Record is a JSON-serialisable snapshot of a cookie for our session file.
type Record struct {
	Name     string    `json:"Name"`
	Value    string    `json:"Value"`
	Domain   string    `json:"Domain"`
	Path     string    `json:"Path"`
	Expires  time.Time `json:"Expires"`
	Secure   bool      `json:"Secure"`
	HttpOnly bool      `json:"HttpOnly"`
	SameSite int       `json:"SameSite"`
}

// IsSession returns true when the cookie has no Expires set (treat as persistent non-expiring).
func (r Record) IsSession() bool {
	return r.Expires.IsZero()
}

func FromHTTP(c *http.Cookie, reqHost string) Record {
	exp := time.Time{}
	if !c.Expires.IsZero() {
		exp = c.Expires
	}
	domain := c.Domain
	if domain == "" {
		// Host-only cookies have no Domain attribute; they're bound to the
		// request host. Persist that so downstream checks (endsWithCBEDomain)
		// see the right domain.
		domain = reqHost
	}
	return Record{
		Name:     c.Name,
		Value:    c.Value,
		Domain:   domain,
		Path:     c.Path,
		Expires:  exp,
		Secure:   c.Secure,
		HttpOnly: c.HttpOnly,
		SameSite: int(c.SameSite),
	}
}

// IsExpired reports whether a Record is past its expiration.
// Session cookies (Expires == zero) are treated as non-expiring.
func (r Record) IsExpired(now time.Time) bool {
	if r.Expires.IsZero() {
		return false
	}
	return !r.Expires.After(now)
}

// ToString renders the cookie in a form that can be handed to a cookie jar
// (net/http/cookiejar accepts Set-Cookie header strings).
func (r Record) ToString() string {
	s := r.Name + "=" + r.Value
	if r.Path != "" {
		s += "; Path=" + r.Path
	}
	if r.Domain != "" {
		s += "; Domain=" + r.Domain
	}
	if !r.Expires.IsZero() {
		s += "; Expires=" + r.Expires.UTC().Format(http.TimeFormat)
	}
	if r.Secure {
		s += "; Secure"
	}
	if r.HttpOnly {
		s += "; HttpOnly"
	}
	switch http.SameSite(r.SameSite) {
	case http.SameSiteLaxMode:
		s += "; SameSite=Lax"
	case http.SameSiteStrictMode:
		s += "; SameSite=Strict"
	case http.SameSiteNoneMode:
		s += "; SameSite=None"
	}
	return s
}