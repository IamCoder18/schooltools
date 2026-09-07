package login

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

// D2LTokenEndpoint is the Brightspace OAuth2 token endpoint reachable from
// inside an authenticated D2L LMS session. Discovered by capturing network
// traffic from a real D2L SPA: it issues the JWT that D2L's own frontend
// stashes in LocalStorage["D2L.Fetch.Tokens"], used by the newer Brightspace
// APIs at api.brightspace.com.
const D2LTokenEndpoint = ua.D2LBase + "/d2l/lp/auth/oauth2/token"

// tokenResponse mirrors the JSON shape returned by D2LTokenEndpoint. The
// response may include a refresh_token in some tenants; we capture it when
// present even though our current refresh strategy falls back to a re-login
// when no refresh token is available.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// xsrfFromHomeRegex pulls the XSRF token out of the inline script D2L writes
// into every SSR page: localStorage.setItem('XSRF.Token','<value>').
var xsrfFromHomeRegex = regexp.MustCompile(`localStorage\.setItem\(\s*'XSRF\.Token'\s*,\s*'([^']+)'\s*\)`)

// mintBrightspaceToken fetches a fresh access token from D2L's OAuth2 endpoint
// using the cookie jar of an already-authenticated D2L session. The endpoint
// requires an X-Csrf-Token header whose value D2L renders into the home page
// HTML (the SPA later promotes it into localStorage). We GET /d2l/home first,
// parse the XSRF token out of the inline script, then POST to the token
// endpoint.
//
// Returns (nil, nil) on transient errors that the caller should treat as
// "token not available, session is still valid" — the LE APIs (/d2l/api/le/*)
// work fine on session cookies alone, so a missing token is non-fatal for the
// login flow itself.
func mintBrightspaceToken(jar *cookiejar.Jar) (*session.TokenRecord, error) {
	xsrf, err := fetchXSRFToken(jar)
	if err != nil {
		return nil, fmt.Errorf("fetch xsrf: %w", err)
	}

	form := url.Values{"scope": {"*:*:*"}}.Encode()
	req, err := http.NewRequest(http.MethodPost, D2LTokenEndpoint, strings.NewReader(form))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Csrf-Token", xsrf)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	client := &http.Client{
		Jar:       jar,
		Transport: httpclient.BrowserTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth2/token: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth2/token: %d %s", res.StatusCode, truncateForLog(string(body)))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("oauth2/token: parse: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("oauth2/token: empty access_token in response")
	}

	issuedAt := time.Now().UTC()
	expiresAt := issuedAt.Add(time.Hour)
	if tr.ExpiresIn > 0 {
		expiresAt = issuedAt.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	rec := session.NewTokenFromJWT(tr.AccessToken, tr.TokenType, tr.Scope, expiresAt, issuedAt)
	rec.RefreshToken = tr.RefreshToken
	return &rec, nil
}

// fetchXSRFToken GETs /d2l/home with the session jar and pulls the XSRF
// token out of the inline `localStorage.setItem('XSRF.Token', ...)` script
// that D2L writes into every SSR page. Returns "" on any failure so callers
// can treat it as "skip token capture".
func fetchXSRFToken(jar *cookiejar.Jar) (string, error) {
	req, err := http.NewRequest(http.MethodGet, ua.D2LBase+"/d2l/home", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html")
	client := &http.Client{
		Jar:       jar,
		Transport: httpclient.BrowserTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	m := xsrfFromHomeRegex.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("XSRF.Token not found in D2L home page")
	}
	return string(m[1]), nil
}

// TokenRefresher adapts the persistent-session + XSRF-token minting flow into
// the httpclient.TokenRefresher interface, so a 401 from a Brightspace API
// call re-mints transparently without forcing a full SAML re-login.
//
// Requirements for a successful refresh:
//   - ~/.config/schooltools/session.json is still loadable and contains a
//     d2lSessionVal cookie (i.e. the LMS session itself hasn't expired).
//   - /d2l/home returns the XSRF inline script (always true while cookies
//     work; ADFS redirects to its own login page once the session is gone).
//
// If session.json is missing or the session has been invalidated, the error
// returned here signals to the caller that a real `schooltools login` is
// required.
func TokenRefresher() httpclient.TokenRefresher {
	return httpclient.TokenRefresherFunc(refresh)
}

func refresh(ctx context.Context) (string, error) {
	jar, err := session.LoadJar()
	if err != nil {
		return "", fmt.Errorf("refresh: load session jar: %w", err)
	}
	if jar == nil {
		return "", fmt.Errorf("refresh: no saved session — run `schooltools login` first")
	}
	rec, err := mintBrightspaceToken(jar)
	if err != nil {
		return "", fmt.Errorf("refresh: mint token: %w", err)
	}
	if rec == nil {
		return "", fmt.Errorf("refresh: token mint returned no record")
	}
	if err := session.SaveToken(*rec); err != nil {
		return "", fmt.Errorf("refresh: save token: %w", err)
	}
	httpclient.InvalidateBearerCache()
	return rec.AccessToken, nil
}
