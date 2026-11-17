package ps

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/IamCoder18/schooltools/internal/cookies"
	stderrors "github.com/IamCoder18/schooltools/internal/errors"
	"github.com/IamCoder18/schooltools/internal/httpclient"
)

// LoginOptions configures a PowerSchool login.
type LoginOptions struct {
	Account  string
	Password string
}

// Login authenticates against the MyCBE portal and returns the cookies
// that should be persisted to the SIS session file.
//
// Flow:
//  1. GET /public/ to seed a JSESSIONID cookie
//  2. POST /guardian/home.html with form-urlencoded credentials —
//     CBE's customised doPCASLogin (in /admin/javascript/md5.js)
//     simply copies it into pw/dbpw/ldappassword; no HMAC-MD5 needed.
//  3. If the response is 302, follow it and verify we landed on a
//     guardian/ page (not /public/home.html).
//  4. Return the resulting cookie set, which must include psaid.
func Login(opts LoginOptions) ([]cookies.Record, error) {
	if opts.Account == "" || opts.Password == "" {
		return nil, stderrors.New("MISSING_CREDS", "PowerSchool login requires SIS_STUDENT_NUMBER and SIS_PASSWORD (or CBE_STUDENT_NUMBER + CBE_PASSWORD) to be set in the .env file")
	}

	jar, err := httpclient.NewJar()
	if err != nil {
		return nil, err
	}

	if _, err := httpclient.Fetch(BaseURL+PublicPath, jar, httpclient.FetchOptions{
		Referer: BaseURL + PublicPath,
	}); err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", PublicPath, err)
	}

	form := url.Values{}
	form.Set("account", opts.Account)
	form.Set("pw", opts.Password)
	form.Set("dbpw", opts.Password)
	form.Set("ldappassword", opts.Password)
	form.Set("serviceName", "PS Parent Portal")
	form.Set("pcasServerUrl", "/")
	form.Set("credentialType", "User Id and Password Credential")
	form.Set("translator_username", "")
	form.Set("translator_password", "")
	form.Set("translator_ldappassword", "")
	form.Set("returnUrl", "")
	form.Set("serviceTicket", "")
	form.Set("translatorpw", "")

	res, err := httpclient.Fetch(BaseURL+LoginPath, jar, httpclient.FetchOptions{
		Method:  http.MethodPost,
		Referer: BaseURL + PublicPath,
		Body:    form.Encode(),
		Headers: map[string]string{
			"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			"Origin": BaseURL,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("POST %s failed: %w", LoginPath, err)
	}

	switch res.StatusCode {
	case http.StatusOK:
		body, _ := httpclient.BodyString(res)
		if isInvalidCreds(body) {
			return nil, stderrors.New("INVALID_CREDS", "PowerSchool rejected the credentials (Invalid Username or Password!)")
		}
		if isPublicLoginPage(body) {
			return nil, stderrors.New("UNEXPECTED_OK", "login POST returned 200 with the public login form instead of a guardian page; the portal did not accept the session")
		}
	case http.StatusFound, http.StatusMovedPermanently, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		loc, err := res.Location()
		if err != nil {
			return nil, fmt.Errorf("302 had no usable Location: %w", err)
		}
		next, err := safeRedirectTarget(loc)
		if err != nil {
			return nil, err
		}
		res2, err := httpclient.Fetch(next, jar, httpclient.FetchOptions{
			Referer: BaseURL + LoginPath,
		})
		if err != nil {
			return nil, fmt.Errorf("follow-redirect GET %s failed: %w", redirectPath(loc), err)
		}
		if res2.StatusCode != http.StatusOK {
			status := res2.Status
			_ = res2.Body.Close()
			return nil, stderrors.New("LOGIN_FOLLOW_FAILED", fmt.Sprintf("POST → %s, follow-up returned %d %s", redirectPath(loc), res2.StatusCode, status))
		}
		body, _ := httpclient.BodyString(res2)
		_ = res2.Body.Close()
		if isInvalidCreds(body) {
			return nil, stderrors.New("INVALID_CREDS", "PowerSchool rejected the credentials (Invalid Username or Password!)")
		}
		if isPublicLoginPage(body) {
			return nil, stderrors.New("LOGIN_REDIRECTED_TO_LOGIN", fmt.Sprintf("login redirected back to the public login page (%s); the portal did not establish a session", redirectPath(loc)))
		}
	default:
		return nil, stderrors.New("UNEXPECTED_STATUS", fmt.Sprintf("login POST returned %d %s", res.StatusCode, res.Status))
	}

	records := allPSCookies(jar)
	if !HasValidSession(records) {
		return nil, stderrors.New("NO_PSAID", fmt.Sprintf("PowerSchool login did not issue a psaid cookie — got %d cookies: %v", len(records), cookieNames(records)))
	}
	return records, nil
}

// allPSCookies returns every cookie in the jar as Record values.
// Login always runs against a single host, so the jar holds only the
// PS cookies; we don't need to filter by hostname.
func allPSCookies(jar *cookiejar.Jar) []cookies.Record {
	if jar == nil {
		return nil
	}
	host := BaseURLHost()
	if host == "" {
		return nil
	}
	u, err := url.Parse("https://" + host)
	if err != nil {
		return nil
	}
	cs := jar.Cookies(u)
	if len(cs) == 0 {
		return nil
	}
	out := make([]cookies.Record, len(cs))
	for i, c := range cs {
		out[i] = cookies.FromHTTP(c, host)
	}
	return out
}

// BaseURLHost returns just the host part of BaseURL, including any port.
func BaseURLHost() string {
	u, err := url.Parse(BaseURL)
	if err != nil {
		return ""
	}
	return u.Host
}

func cookieNames(records []cookies.Record) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Name
	}
	return out
}

// isInvalidCreds returns true when the response body carries the
// "Invalid Username or Password!" alert banner that PowerSchool renders
// on the public login page for rejected credentials.
func isInvalidCreds(body string) bool {
	if body == "" {
		return false
	}
	return strings.Contains(body, "Invalid Username or Password")
}

// isPublicLoginPage returns true when a response body is the public
// sign-in form rather than an authenticated guardian page. PowerSchool
// re-renders the login form with HTTP 200 for a handful of challenges
// (MFA, account lockout, an exhausted session) that do not carry the
// invalid-creds banner, so a status check alone would treat them as a
// successful login.
func isPublicLoginPage(body string) bool {
	if body == "" {
		return false
	}
	lower := strings.ToLower(body)
	if strings.Contains(lower, "/guardian/home") {
		return false
	}
	return strings.Contains(lower, "signin") || strings.Contains(lower, "sign in") ||
		strings.Contains(lower, "public/home") || strings.Contains(lower, "id=\"username\"")
}

// safeRedirectTarget validates a redirect Location before it is fetched.
// Anything that leaves the portal's own origin is rejected: the login
// flow has just posted a plaintext password, and following a
// server-supplied cross-host URL would send the seeded session cookies
// off-tenant. Only the path component is ever used in error messages, so
// a Location carrying a token in its query string is not persisted to the
// auth log.
//
// The scheme must match the scheme of BaseURL rather than being pinned to
// https, so the same check holds against the production portal and a
// plain-HTTP staging or test host.
func safeRedirectTarget(loc *url.URL) (string, error) {
	if loc == nil {
		return "", stderrors.New("LOGIN_FOLLOW_FAILED", "redirect had no usable Location")
	}
	want := "https"
	if base, err := url.Parse(BaseURL); err == nil && base.Scheme != "" {
		want = base.Scheme
	}
	if loc.Scheme != "" && loc.Scheme != want {
		return "", stderrors.New("LOGIN_FOLLOW_FAILED", fmt.Sprintf("refusing to follow a redirect that downgrades %s to %s", want, loc.Scheme))
	}
	if h := loc.Host; h != "" && normalizeHostPort(h, want) != normalizeHostPort(BaseURLHost(), want) {
		return "", stderrors.New("LOGIN_FOLLOW_FAILED", fmt.Sprintf("refusing to follow a redirect off the PowerSchool origin (host %q)", h))
	}
	return loc.String(), nil
}

// normalizeHostPort strips an explicit default port for the scheme so
// "sis.cbe.ab.ca:443" and "sis.cbe.ab.ca" compare equal.
func normalizeHostPort(host, scheme string) string {
	if host == "" {
		return host
	}
	u, err := url.Parse("//" + host)
	if err != nil {
		return host
	}
	port := u.Port()
	if port == "" {
		return host
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return u.Hostname()
	}
	return host
}

// redirectPath renders the safe-to-log portion of a redirect URL: the
// path only, never the query string.
func redirectPath(loc *url.URL) string {
	if loc == nil {
		return "<no location>"
	}
	if loc.RawQuery != "" {
		return loc.Path + "?<redacted>"
	}
	return loc.Path
}

// EnsureSessionOptions configures EnsureSession.
type EnsureSessionOptions struct {
	AutoRefresh bool
	LoginFn     func() ([]cookies.Record, error)
}

// EnsureSession loads the saved PS cookies, returning a ready jar. If
// the saved session is missing or invalid and AutoRefresh is set, it
// calls LoginFn to re-authenticate and persists the new cookies.
//
// The on-disk file is the single source of truth.
//
// Local expiry is not sufficient on its own: PowerSchool issues psaid
// and JSESSIONID as session cookies with no Expires, so a locally-saved
// session always looks valid even after the server has revoked it. The
// resulting 401/403 is caught by the Client's refresh hook, which calls
// Reauthenticate below.
func EnsureSession(opts EnsureSessionOptions) (*cookiejar.Jar, error) {
	saved, err := Load()
	if err != nil {
		return nil, err
	}
	if !HasValidSession(saved) {
		if !opts.AutoRefresh || opts.LoginFn == nil {
			return nil, stderrors.New("NO_SESSION", "no valid PowerSchool session; run 'schooltools sis login' first")
		}
		if _, err := Reauthenticate(opts.LoginFn); err != nil {
			return nil, err
		}
	}
	return LoadJar()
}

// Reauthenticate runs a fresh login, persists the resulting cookies, and
// returns a jar built from them. It backs both EnsureSession's initial
// login and the Client's 401/403 refresh hook.
func Reauthenticate(loginFn func() ([]cookies.Record, error)) (*cookiejar.Jar, error) {
	if loginFn == nil {
		return nil, stderrors.New("NO_SESSION", "no saved PowerSchool session; run 'schooltools sis login' first")
	}
	fresh, err := loginFn()
	if err != nil {
		return nil, err
	}
	if err := Save(fresh); err != nil {
		return nil, err
	}
	return LoadJar()
}
