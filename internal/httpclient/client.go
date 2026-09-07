package httpclient

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/andybalholm/brotli"

	"github.com/aarav/schooltools/internal/browser"
	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/ua"
	"golang.org/x/net/publicsuffix"
)

var RedirectStatuses = map[int]struct{}{
	301: {}, 302: {}, 303: {}, 307: {}, 308: {},
}

func IsRedirect(status int) bool {
	_, ok := RedirectStatuses[status]
	return ok
}

func NewJar() (*cookiejar.Jar, error) {
	return cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
}

type FetchOptions struct {
	Method  string
	Headers map[string]string
	Body    string
	// Referer is the URL the user "came from". Passed through to
	// browser.Headers so Sec-Fetch-Site / Referer look right. Empty for a
	// direct navigation.
	Referer string
}

// Fetch performs one HTTP request without following redirects (manual mode),
// applies browser-like headers, sends cookies from the jar, and stores any
// returned Set-Cookie values back into the jar.
func Fetch(rawURL string, jar *cookiejar.Jar, opts FetchOptions) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	method := opts.Method
	if method == "" {
		method = http.MethodGet
	}
	headers := browser.Headers(browser.RequestContext{
		URL:     rawURL,
		Method:  method,
		Referer: opts.Referer,
	}, opts.Headers)
	if _, ok := headers["User-Agent"]; !ok {
		headers["User-Agent"] = ua.UserAgent
	}

	var body io.Reader
	if opts.Body != "" {
		body = strings.NewReader(opts.Body)
		if _, ok := headers["Content-Type"]; !ok {
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Jar:           jar,
		Transport:     BrowserTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	jarCookies(u, res, jar)
	res.Body = decompressBody(res)
	return res, nil
}

// decompressBody wraps res.Body with the appropriate decoder based on the
// response's Content-Encoding. Browser-like clients always advertise
// Accept-Encoding (which makes Go's transport skip auto-decompression), so
// we handle it here.
func decompressBody(res *http.Response) io.ReadCloser {
	if res.Body == nil || res.ContentLength == 0 {
		return res.Body
	}
	enc := strings.ToLower(strings.TrimSpace(res.Header.Get("Content-Encoding")))
	var reader io.ReadCloser
	switch enc {
	case "gzip":
		r, err := gzip.NewReader(res.Body)
		if err != nil {
			return res.Body
		}
		reader = r
	case "deflate":
		r, err := zlib.NewReader(res.Body)
		if err != nil {
			// Some servers send raw deflate without zlib wrapper.
			r2, err2 := flate.NewReader(res.Body), error(nil)
			if err2 != nil {
				return res.Body
			}
			_ = err
			reader = r2
		} else {
			reader = r
		}
	case "br":
		reader = io.NopCloser(brotli.NewReader(res.Body))
	default:
		return res.Body
	}
	// Unset so downstream readers (e.g. JSON decoder) don't try to re-decompress.
	res.Header.Del("Content-Encoding")
	res.Header.Del("Content-Length")
	res.ContentLength = -1
	return reader
}

func jarCookies(u *url.URL, res *http.Response, jar *cookiejar.Jar) {
	if jar == nil || u == nil {
		return
	}
	setURL := &url.URL{Scheme: u.Scheme, Host: u.Hostname(), Path: u.Path}
	for _, sc := range res.Cookies() {
		jar.SetCookies(setURL, []*http.Cookie{sc})
	}
}

// FollowRedirects calls Fetch and, if the response is a redirect, performs one
// manual hop to the Location URL. Mirrors the TS pattern used everywhere.
func FollowRedirects(rawURL string, jar *cookiejar.Jar, opts FetchOptions) (*http.Response, error) {
	res, err := Fetch(rawURL, jar, opts)
	if err != nil {
		return nil, err
	}
	if !IsRedirect(res.StatusCode) {
		return res, nil
	}
	loc := res.Header.Get("Location")
	if loc == "" {
		return nil, fmt.Errorf("redirect %d with no Location header", res.StatusCode)
	}
	next, err := res.Location()
	if err != nil {
		return nil, err
	}
	res.Body.Close()
	return Fetch(next.String(), jar, opts)
}

// CookiesForHost returns the jar's cookies for the given host as Record values
// suitable for persistence or authlog events. `host` should be a bare hostname
// (no port); pass it through url.Hostname() if unsure.
func CookiesForHost(jar *cookiejar.Jar, host string) []cookies.Record {
	if jar == nil {
		return nil
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		host = h
	}
	u := &url.URL{Scheme: "https", Host: host}
	var out []cookies.Record
	for _, c := range jar.Cookies(u) {
		out = append(out, cookies.FromHTTP(c, host))
	}
	return out
}

// BodyBytes is a convenience for callers that need the full response body.
func BodyBytes(res *http.Response) ([]byte, error) {
	if res == nil {
		return nil, fmt.Errorf("nil response")
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, err := io.Copy(&buf, res.Body)
	return buf.Bytes(), err
}

func BodyString(res *http.Response) (string, error) {
	b, err := BodyBytes(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}