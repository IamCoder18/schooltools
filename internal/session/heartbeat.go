package session

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/aarav/schooltools/internal/authlog"
	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/ua"
)

// Heartbeat does a single cheap authenticated request against D2L to confirm
// the saved LMS session cookies are still server-side valid. It returns a
// SessionProbeResult with latency and a SessionExpiredError if the probe
// detects that the server has invalidated the session.
//
// The probe is GET /d2l/home — D2L's standard SSR shell. When the session is
// dead, the server responds with either:
//   - HTTP 302 with Location pointing to /d2l/login?sessionExpired=1, OR
//   - HTTP 200 with a tiny HTML body whose inline JS redirects to the same URL.
//
// We detect both. The 200-tiny-body case is the common one in practice.
//
// Heartbeat is intentionally side-effect-free: no cookies written, no auth
// log events emitted (callers should log the result themselves).
func Heartbeat(jar *cookiejar.Jar) (stderrors.SessionProbeResult, error) {
	start := time.Now()
	if jar == nil {
		return stderrors.SessionProbeResult{}, fmt.Errorf("heartbeat: nil cookie jar")
	}
	req, err := http.NewRequest(http.MethodGet, ua.D2LBase+"/d2l/home", nil)
	if err != nil {
		return stderrors.SessionProbeResult{}, err
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
	probeAt := time.Now()
	if err != nil {
		return stderrors.SessionProbeResult{DetectedAt: probeAt, Latency: time.Since(start)},
			fmt.Errorf("heartbeat: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	finalURL := res.Request.URL.String()
	result := stderrors.SessionProbeResult{
		URL:        finalURL,
		Status:     res.StatusCode,
		BodyBytes:  len(body),
		Latency:    time.Since(start),
		DetectedAt: probeAt,
	}

	if stderrors.IsExpiredStatus(res.StatusCode) || stderrors.ProbeExpiredURL(finalURL) {
		result.Expired = true
		result.OK = false
		return result, &stderrors.SessionExpiredError{
			URL:        finalURL,
			ProbeAt:    probeAt,
			LastAccess: time.Now().Add(-time.Since(start)),
		}
	}

	// Tiny redirect-via-JS body signals session-expired at HTTP 200.
	if res.StatusCode == http.StatusOK && len(body) < 1024 && bytesContain(body, []byte("sessionExpired=1")) {
		result.Expired = true
		result.OK = false
		return result, &stderrors.SessionExpiredError{
			URL:     finalURL,
			ProbeAt: probeAt,
		}
	}

	result.OK = true
	return result, nil
}

// HeartbeatWithLog is Heartbeat plus authlog events for the outcome. Use this
// from command paths so the result lands in auth.log.
func HeartbeatWithLog(jar *cookiejar.Jar) error {
	res, err := Heartbeat(jar)
	if err == nil {
		_ = authlog.Log("session.heartbeat.ok", map[string]any{
			"url":        res.URL,
			"status":     res.Status,
			"latency_ms": res.Latency.Milliseconds(),
			"body_bytes": res.BodyBytes,
		})
		return nil
	}
	kind := "transport_error"
	if stderrors.IsSessionExpired(err) {
		kind = "session_expired"
	}
	_ = authlog.Log("session.heartbeat.failed", map[string]any{
		"kind":  kind,
		"url":   res.URL,
		"error": err.Error(),
	})
	return err
}

// IsLikelyLoginURL reports whether a URL looks like D2L's login surface.
func IsLikelyLoginURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Host != "" && u.Host != "d2l.cbe.ab.ca" {
		return false
	}
	return u.Path == "/d2l/login" || u.Path == "/d2l/loginh/" ||
		stderrors.ProbeExpiredURL(rawURL)
}

// bytesContain is a zero-allocation substring check.
func bytesContain(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
