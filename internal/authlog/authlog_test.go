package authlog_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathIsUnderConfigHome(t *testing.T) {
	p := authlog.Path()
	assert.True(t, strings.HasSuffix(p, "auth.log"))
	assert.Contains(t, p, ".config")
}

func TestSetEnabledTogglesNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCHOOLTOOLS_NO_AUTH_LOG", "")
	authlog.SetEnabled(true)
	require.True(t, authlog.Enabled())
	require.NoError(t, authlog.Log("test.event", map[string]any{"k": "v"}))
	// File should exist now.
	st, err := os.Stat(authlog.Path())
	require.NoError(t, err)
	assert.NotZero(t, st.Size())

	// Disable and ensure no further writes happen.
	authlog.SetEnabled(false)
	require.False(t, authlog.Enabled())
	sizeBefore := st.Size()
	require.NoError(t, authlog.Log("test.skipped", map[string]any{"k": "v"}))
	st2, err := os.Stat(authlog.Path())
	require.NoError(t, err)
	assert.Equal(t, sizeBefore, st2.Size())
}

func TestEnvVarOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	authlog.SetEnabled(true)
	t.Setenv("SCHOOLTOOLS_NO_AUTH_LOG", "1")
	assert.False(t, authlog.Enabled())
}

func TestLogAppendsJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	authlog.SetEnabled(true)
	t.Setenv("SCHOOLTOOLS_NO_AUTH_LOG", "")

	require.NoError(t, authlog.Log("login.start", map[string]any{"kmsi": true}))
	exp := time.Now().Add(time.Hour)
	require.NoError(t, authlog.LogCookies("login.success", []authlog.CookieSummary{
		{Name: "d2lSessionVal", Domain: ".cbe.ab.ca", Expires: &exp},
	}))
	require.NoError(t, authlog.Log("session.saved", map[string]any{"count": 7}))

	data, err := os.ReadFile(authlog.Path())
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 3)
	for _, ln := range lines {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(ln), &m))
		assert.NotEmpty(t, m["ts"])
		assert.NotEmpty(t, m["event"])
	}
	// File mode is 0o600.
	st, err := os.Stat(authlog.Path())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	// Directory mode is 0o700.
	stDir, err := os.Stat(filepath.Dir(authlog.Path()))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), stDir.Mode().Perm())
}

func TestLogFailureIsSwallowed(t *testing.T) {
	// Point HOME to a file so MkdirAll on its config dir fails.
	tmp := t.TempDir()
	blocked := filepath.Join(tmp, "blocked")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o600))
	t.Setenv("HOME", blocked)
	t.Setenv("SCHOOLTOOLS_NO_AUTH_LOG", "")
	authlog.SetEnabled(true)
	// Should not panic / propagate.
	_ = authlog.Log("test.event", map[string]any{"k": "v"})
	// Sanity check we didn't accidentally write to the blocked path.
	data, err := os.ReadFile(blocked)
	require.NoError(t, err)
	assert.Equal(t, "x", string(data))
}

func TestLogCookiesSerialisesExpiries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	authlog.SetEnabled(true)
	t.Setenv("SCHOOLTOOLS_NO_AUTH_LOG", "")

	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, authlog.LogCookies("login.success", []authlog.CookieSummary{
		{Name: "d2lSessionVal", Domain: ".cbe.ab.ca", Expires: &exp},
	}))
	data, err := os.ReadFile(authlog.Path())
	require.NoError(t, err)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	require.True(t, scanner.Scan())
	var line map[string]any
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &line))
	cookies, ok := line["cookies"].([]any)
	require.True(t, ok)
	require.Len(t, cookies, 1)
	c := cookies[0].(map[string]any)
	assert.Equal(t, "d2lSessionVal", c["name"])
	assert.Equal(t, ".cbe.ab.ca", c["domain"])
}