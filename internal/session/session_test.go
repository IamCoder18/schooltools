package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTempHome(t *testing.T, fn func(home string)) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Some envs also consult these.
	t.Setenv("XDG_CONFIG_HOME", "")
	fn(home)
}

func mkCookie(name, value string, opts ...func(*cookies.Record)) cookies.Record {
	r := cookies.Record{
		Name:   name,
		Value:  value,
		Domain: ".cbe.ab.ca",
		Path:   "/",
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func pastExpires(r *cookies.Record)    { r.Expires = time.Now().Add(-time.Minute) }
func futureExpires(r *cookies.Record)  { r.Expires = time.Now().Add(time.Hour) }
func domain(d string) func(*cookies.Record) {
	return func(r *cookies.Record) { r.Domain = d }
}

func TestHasValidSessionEmpty(t *testing.T) {
	assert.False(t, session.HasValidSession(nil))
	assert.False(t, session.HasValidSession([]cookies.Record{}))
}

func TestHasValidSessionAcceptsKnownNames(t *testing.T) {
	cookies := []cookies.Record{
		mkCookie("d2lSessionVal", "x"),
		mkCookie("d2lSecureSessionVal", "y"),
		mkCookie("d2l.SESSIONID", "z", domain("d2l.cbe.ab.ca")),
		mkCookie("d2l.session", "w", domain("d2l.cbe.ab.ca")),
	}
	assert.True(t, session.HasValidSession(cookies))
}

func TestHasValidSessionRejectsUnknownAndWrongDomain(t *testing.T) {
	assert.False(t, session.HasValidSession([]cookies.Record{
		mkCookie("d2l.snc", "x"),
	}))
	assert.False(t, session.HasValidSession([]cookies.Record{
		mkCookie("d2lSessionVal", "x", domain(".example.com")),
	}))
	assert.False(t, session.HasValidSession([]cookies.Record{
		mkCookie("not-d2l", "x"),
	}))
}

func TestHasValidSessionRejectsExpired(t *testing.T) {
	c := mkCookie("d2lSessionVal", "x", pastExpires)
	assert.False(t, session.HasValidSession([]cookies.Record{c}))
	c = mkCookie("d2lSessionVal", "x", futureExpires)
	assert.True(t, session.HasValidSession([]cookies.Record{c}))
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempHome(t, func(string) {
		future := time.Now().Add(time.Hour)
		in := []cookies.Record{
			mkCookie("d2lSessionVal", "abc", futureExpires),
			mkCookie("d2lSecureSessionVal", "def", futureExpires),
			mkCookie("d2l.snc", "ghi"),
			mkCookie("d2lSessionVal", "expired-1", pastExpires),
			func() cookies.Record {
				r := mkCookie("adfs_cookie", "should-persist", futureExpires, domain("adfs.cbe.ab.ca"))
				r.Expires = future
				return r
			}(),
			func() cookies.Record {
				r := mkCookie("jsessionid", "should-persist", futureExpires, domain("example.com"))
				r.Expires = future
				return r
			}(),
		}
		require.NoError(t, session.Save(in))
		loaded, err := session.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 5)
		names := make([]string, len(loaded))
		for i, c := range loaded {
			names[i] = c.Name
		}
		assert.ElementsMatch(t, names, []string{"adfs_cookie", "d2l.snc", "d2lSecureSessionVal", "d2lSessionVal", "jsessionid"})
		assert.True(t, session.HasValidSession(loaded))
	})
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	withTempHome(t, func(string) {
		got, err := session.Load()
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestLoadCorruptJSON(t *testing.T) {
	withTempHome(t, func(home string) {
		dir := filepath.Join(home, ".config", "schooltools")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		path := filepath.Join(dir, "session.json")
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
		_, err := session.Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "corrupt")
	})
}

func TestLoadNonArrayJSON(t *testing.T) {
	withTempHome(t, func(home string) {
		dir := filepath.Join(home, ".config", "schooltools")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		path := filepath.Join(dir, "session.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"key":"d2lSessionVal"}`), 0o600))
		_, err := session.Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not")
	})
}

func TestDiagnoseMissingFile(t *testing.T) {
	withTempHome(t, func(string) {
		d := session.Diagnose()
		assert.False(t, d.Exists)
		assert.Equal(t, 0, d.CookieCount)
	})
}

func TestDiagnoseRoundTrip(t *testing.T) {
	withTempHome(t, func(string) {
		future := time.Now().Add(time.Hour)
		require.NoError(t, session.Save([]cookies.Record{
			mkCookie("d2lSessionVal", "fresh", futureExpires),
			mkCookie("d2lSessionVal", "expired", pastExpires),
			func() cookies.Record {
				r := mkCookie("adfs_cookie", "should-be-persisted", futureExpires)
				r.Expires = future
				return r
			}(),
		}))
		d := session.Diagnose()
		assert.True(t, d.Exists)
		assert.Equal(t, 2, d.CookieCount)
		names := d.CookieNames
		assert.ElementsMatch(t, names, []string{"adfs_cookie", "d2lSessionVal"})
	})
}

func TestCookieSerializationRoundTrip(t *testing.T) {
	r := mkCookie("a", "b")
	r.Expires = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	r.Secure = true
	r.HttpOnly = true
	r.SameSite = 1 // Lax
	data, err := json.Marshal(r)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(data), `"Name":"a"`))
	var out cookies.Record
	require.NoError(t, json.Unmarshal(data, &out))
	assert.Equal(t, r, out)
}