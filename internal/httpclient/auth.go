package httpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TokenRefresher mints a fresh Brightspace OAuth access token. The login
// package implements this; httpclient holds an indirection so the two
// packages don't import each other.
type TokenRefresher interface {
	Refresh(ctx context.Context) (token string, err error)
}

// TokenRefresherFunc lets a plain function satisfy TokenRefresher without
// declaring a new type. The login package uses this to adapt mintBrightspaceToken.
type TokenRefresherFunc func(ctx context.Context) (string, error)

// Refresh implements TokenRefresher.
func (f TokenRefresherFunc) Refresh(ctx context.Context) (string, error) { return f(ctx) }

var (
	refresherMu sync.RWMutex
	refresher   TokenRefresher
)

// RegisterTokenRefresher wires in the package that knows how to mint a new
// token (typically internal/login). Passing nil clears the registration.
func RegisterTokenRefresher(r TokenRefresher) {
	refresherMu.Lock()
	defer refresherMu.Unlock()
	refresher = r
}

// needsBearer reports whether the URL host expects a Brightspace OAuth bearer
// token. Today that's the api.brightspace.com family; the LMS UI
// (d2l.cbe.ab.ca) keeps using session cookies and is reached through the jar.
func needsBearer(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".brightspace.com") ||
		host == "brightspace.com" ||
		host == "api.brightspace.com"
}

// bearerTokenCache caches the most recent token.json read per host so we don't
// hit disk on every API call.
var bearerTokenCache sync.Map // host -> cachedToken

type cachedToken struct {
	value     string
	expiresAt time.Time
}

// bearerFor returns the bearer token to attach to a request, or "" if the
// host doesn't need one or no token is on disk.
func bearerFor(u *url.URL) string {
	if !needsBearer(u) {
		return ""
	}
	host := u.Hostname()
	if v, ok := bearerTokenCache.Load(host); ok {
		c := v.(cachedToken)
		if c.expiresAt.IsZero() || time.Now().Before(c.expiresAt) {
			return c.value
		}
	}
	tok, exp := readTokenFromDisk()
	if tok == "" {
		return ""
	}
	bearerTokenCache.Store(host, cachedToken{value: tok, expiresAt: exp})
	return tok
}

// readTokenFromDisk reads ~/.config/schooltools/token.json. Re-implemented
// here (instead of importing internal/session) to keep httpclient a leaf.
func readTokenFromDisk() (string, time.Time) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", time.Time{}
	}
	path := filepath.Join(home, ".config", "schooltools", "token.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}
	}
	var t struct {
		AccessToken string    `json:"access_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return "", time.Time{}
	}
	return t.AccessToken, t.ExpiresAt
}

// InvalidateBearerCache drops the in-memory cache so the next request re-reads
// token.json. Call after a refresh.
func InvalidateBearerCache() {
	bearerTokenCache.Range(func(k, _ any) bool { bearerTokenCache.Delete(k); return true })
}

// authedFetch wraps Fetch with Bearer-token attachment + 401-refresh-retry for
// Brightspace API hosts. We never auto-attach Bearer to LMS UI URLs.
func authedFetch(rawURL string, jar *cookiejar.Jar, opts FetchOptions) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	tok := bearerFor(u)
	if tok != "" {
		if opts.Headers == nil {
			opts.Headers = map[string]string{}
		}
		if _, ok := opts.Headers["Authorization"]; !ok {
			opts.Headers["Authorization"] = "Bearer " + tok
		}
	}
	res, err := Fetch(rawURL, jar, opts)
	if err != nil {
		return nil, err
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized || !needsBearer(u) {
		return res, nil
	}
	refresherMu.RLock()
	r := refresher
	refresherMu.RUnlock()
	if r == nil {
		return res, nil
	}
	_ = res.Body.Close()
	newTok, err := r.Refresh(context.Background())
	if err != nil || newTok == "" {
		// Re-issue the original request so the caller gets a proper 401.
		return Fetch(rawURL, jar, opts)
	}
	InvalidateBearerCache()
	if opts.Headers == nil {
		opts.Headers = map[string]string{}
	}
	opts.Headers["Authorization"] = "Bearer " + newTok
	return Fetch(rawURL, jar, opts)
}
