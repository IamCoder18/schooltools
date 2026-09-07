package login_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/login"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefreshMintsNewTokenWithoutSAML exercises the same code path the
// httpclient 401-retry uses. Requires the saved LMS session cookies from a
// real `schooltools login` to still be valid (session-only cookies have no
// Expires, so if the cookies were captured within the last few minutes they
// will work). Skipped when ~/.config/schooltools/session.json is missing.
func TestRefreshMintsNewTokenWithoutSAML(t *testing.T) {
	jar, err := session.LoadJar()
	if err != nil {
		t.Skipf("session.LoadJar failed (no saved session): %v", err)
	}
	if jar == nil {
		t.Skip("no saved LMS session cookies; run `schooltools login` first")
	}

	// Diagnostic: confirm /d2l/home still answers with the XSRF inline
	// script. If it doesn't, the saved session is stale.
	tok := peekD2LHome(t, jar)
	if tok == "" {
		t.Skip("/d2l/home did not yield an XSRF.Token — saved session cookies appear stale; rerun `schooltools login`")
	}

	// Capture pre-refresh state
	before, _ := session.LoadToken()
	_ = before

	httpclient.RegisterTokenRefresher(login.TokenRefresher())
	newTok, err := login.TokenRefresher().Refresh(context.Background())
	require.NoError(t, err, "refresh failed — session cookies likely expired; rerun `schooltools login`")
	assert.NotEmpty(t, newTok)
	assert.True(t, len(newTok) > 200, "JWT should be hundreds of chars; got %d", len(newTok))

	// Token must be persisted to token.json
	after, err := session.LoadToken()
	require.NoError(t, err)
	require.NotNil(t, after, "token.json missing after Refresh")
	assert.NotEmpty(t, after.AccessToken)
	assert.NotEmpty(t, after.UserID)
	assert.NotEmpty(t, after.TenantID)
	assert.True(t, after.ExpiresAt.After(time.Now()), "ExpiresAt should be in the future, got %s", after.ExpiresAt)

	// A second refresh should produce a different ExpiresAt (new issuance)
	firstExp := after.ExpiresAt
	time.Sleep(1100 * time.Millisecond) // ensure iat advances at least 1s
	tok2, err := login.TokenRefresher().Refresh(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, tok2)
	after2, err := session.LoadToken()
	require.NoError(t, err)
	require.NotNil(t, after2)
	assert.True(t, after2.ExpiresAt.After(firstExp) || after2.ExpiresAt.Equal(firstExp.Add(1*time.Second)),
		"second refresh should advance ExpiresAt; first=%s second=%s", firstExp, after2.ExpiresAt)

	// Sanity: token.json lives next to session.json, as documented
	assert.Equal(t, filepath.Join(filepath.Dir(session.SessionPath()), "token.json"),
		session.TokenPath())
}

// peekD2LHome GETs /d2l/home with the supplied jar and returns the XSRF.Token
// inline-script value, or "" on any failure. Used by
// TestRefreshMintsNewTokenWithoutSAML to skip cleanly when the saved session
// is stale rather than failing with a confusing network error.
func peekD2LHome(t *testing.T, jar *cookiejar.Jar) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ua.D2LBase+"/d2l/home", nil)
	if err != nil {
		return ""
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
		t.Logf("peek: %v", err)
		return ""
	}
	defer res.Body.Close()
	t.Logf("/d2l/home peek: status=%d final=%s", res.StatusCode, res.Request.URL.String())
	if res.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return ""
	}
	t.Logf("/d2l/home body: %d bytes, contains 'XSRF.Token': %v, contains 'localStorage': %v",
		len(body),
		bytesContains(body, []byte("XSRF.Token")),
		bytesContains(body, []byte("localStorage")),
	)
	re := regexp.MustCompile(`localStorage\.setItem\(\s*'XSRF\.Token'\s*,\s*'([^']+)'\s*\)`)
	m := re.FindSubmatch(body)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func bytesContains(b []byte, sub []byte) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return true
		}
	}
	return false
}
