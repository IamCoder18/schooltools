package httpclient

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"

	"github.com/IamCoder18/schooltools/internal/ua"
)

var xsrfTokenRegex = regexp.MustCompile(`localStorage\.setItem\(\s*'XSRF\.Token'\s*,\s*'([^']+)'\s*\)`)

// FetchXSRFToken GETs /d2l/home with the session jar and pulls the XSRF
// token out of the inline `localStorage.setItem('XSRF.Token', ...)` script
// that D2L writes into every SSR page. The value doubles as the
// X-Csrf-Token header required by cookie-authenticated API writes (and by
// every call once the tenant enters its CSRF-checking state).
func FetchXSRFToken(jar *cookiejar.Jar) (string, error) {
	return FetchXSRFTokenFrom(jar, ua.D2LBase+"/d2l/home")
}

// FetchXSRFTokenFrom is FetchXSRFToken against an explicit home URL, so
// tests can point it at an httptest server.
func FetchXSRFTokenFrom(jar *cookiejar.Jar, homeURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, homeURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html")
	client := &http.Client{
		Transport: BrowserTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if jar != nil {
		client.Jar = jar
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
	m := xsrfTokenRegex.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("XSRF.Token not found in D2L home page")
	}
	return string(m[1]), nil
}
