// Package ps is the client for the Calgary Board of Education's
// PowerSchool SIS parent/student portal at https://sis.cbe.ab.ca.
//
// The portal is a modern Angular SPA (PowerSchool 21.06.339 / PDS 31.0.3,
// "Neon" UI) that talks to internal REST endpoints under /ws/xte/….
// Authentication is session-cookie based: a successful login sets
// JSESSIONID, psaid, currentSchool and current_locale; subsequent API
// calls just need those four cookies plus an Origin header for POSTs.
//
// We do not use the public Pearson REST plugin
// (/pearson-rest/services/PublicPortalServiceJSON) — CBE rotated the
// default digest credentials and that endpoint returns 401. We also do
// not use /ws/v1/ (the OAuth2 SIS REST API) — student accounts don't
// have admin OAuth tokens.
package ps

// BaseURL is the CBE PowerSchool SIS portal. It's a var (not a const)
// so tests can swap it for an httptest server.
var BaseURL = "https://sis.cbe.ab.ca"

const (
	jsonAccept = "application/json, text/plain, */*"
	htmlAccept = "text/html,application/xhtml+xml"
)

// LoginPath is the form-post endpoint CBE customised to send plaintext
// passwords (no HMAC-MD5 with pskey, unlike vanilla PowerSchool).
const LoginPath = "/guardian/home.html"

// PublicPath seeds the JSESSIONID cookie.
const PublicPath = "/public/"
