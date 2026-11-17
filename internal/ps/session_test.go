package ps_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aarav/schooltools/internal/cookies"
	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/aarav/schooltools/internal/ps"
)

func withTempHome(t *testing.T, fn func(home string)) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	fn(home)
}

func mkCookie(name, value, domain string, opts ...func(*cookies.Record)) cookies.Record {
	r := cookies.Record{
		Name:   name,
		Value:  value,
		Domain: domain,
		Path:   "/",
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func pastExpires(r *cookies.Record) { r.Expires = time.Now().Add(-time.Minute) }

func TestSessionPath(t *testing.T) {
	withTempHome(t, func(home string) {
		got := ps.SessionPath()
		want := filepath.Join(home, ".config", "schooltools", "sis-session.json")
		assert.Equal(t, want, got)
	})
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempHome(t, func(string) {
		records := []cookies.Record{
			mkCookie("JSESSIONID", "AAAA", "sis.cbe.ab.ca"),
			mkCookie("psaid", "BBBB", "sis.cbe.ab.ca"),
			mkCookie("currentSchool", "816", "sis.cbe.ab.ca"),
			mkCookie("current_locale", "en_US", "sis.cbe.ab.ca"),
		}
		require.NoError(t, ps.Save(records))
		got, err := ps.Load()
		require.NoError(t, err)
		require.Len(t, got, 4)
		assert.Equal(t, "AAAA", got[0].Value)
		assert.Equal(t, "BBBB", got[1].Value)
		assert.Equal(t, "816", got[2].Value)
	})
}

func TestSaveDropsExpired(t *testing.T) {
	withTempHome(t, func(string) {
		records := []cookies.Record{
			mkCookie("psaid", "BBBB", "sis.cbe.ab.ca"),
			mkCookie("JSESSIONID", "AAAA", "sis.cbe.ab.ca", pastExpires),
		}
		require.NoError(t, ps.Save(records))
		got, err := ps.Load()
		require.NoError(t, err)
		// Expired cookie dropped on save.
		require.Len(t, got, 1)
		assert.Equal(t, "psaid", got[0].Name)
	})
}

func TestLoadMissing(t *testing.T) {
	withTempHome(t, func(string) {
		got, err := ps.Load()
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestLoadCorrupt(t *testing.T) {
	withTempHome(t, func(string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(ps.SessionPath()), 0o700))
		require.NoError(t, os.WriteFile(ps.SessionPath(), []byte("not json"), 0o600))
		_, err := ps.Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "corrupt")
	})
}

func TestClear(t *testing.T) {
	withTempHome(t, func(string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(ps.SessionPath()), 0o700))
		require.NoError(t, os.WriteFile(ps.SessionPath(), []byte("[]"), 0o600))
		require.NoError(t, ps.Clear())
		_, err := os.Stat(ps.SessionPath())
		assert.True(t, os.IsNotExist(err))
		// Idempotent.
		require.NoError(t, ps.Clear())
	})
}

func TestHasValidSession(t *testing.T) {
	tests := []struct {
		name string
		recs []cookies.Record
		want bool
	}{
		{"empty", nil, false},
		{"only-jsessionid", []cookies.Record{mkCookie("JSESSIONID", "x", "sis.cbe.ab.ca")}, false},
		{"psaid-but-expired", []cookies.Record{mkCookie("psaid", "x", "sis.cbe.ab.ca", pastExpires)}, false},
		{"psaid-but-wrong-domain", []cookies.Record{mkCookie("psaid", "x", "evil.com")}, false},
		{"psaid-good", []cookies.Record{mkCookie("psaid", "x", "sis.cbe.ab.ca")}, true},
		{"psaid-with-leading-dot-domain", []cookies.Record{mkCookie("psaid", "x", ".cbe.ab.ca")}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ps.HasValidSession(tc.recs))
		})
	}
}

func TestLoadJar(t *testing.T) {
	withTempHome(t, func(string) {
		records := []cookies.Record{
			mkCookie("JSESSIONID", "AAAA", "sis.cbe.ab.ca"),
			mkCookie("psaid", "BBBB", "sis.cbe.ab.ca"),
			mkCookie("currentSchool", "816", "sis.cbe.ab.ca"),
			mkCookie("current_locale", "en_US", "sis.cbe.ab.ca"),
		}
		require.NoError(t, ps.Save(records))
		jar, err := ps.LoadJar()
		require.NoError(t, err)
		require.NotNil(t, jar)
		// All four cookies should now be in the jar, retrievable for the
		// sis.cbe.ab.ca host.
		req, _ := http.NewRequest("GET", "https://sis.cbe.ab.ca/ws/xte/test", nil)
		var found int
		for _, c := range jar.Cookies(req.URL) {
			if c.Name == "JSESSIONID" || c.Name == "psaid" || c.Name == "currentSchool" || c.Name == "current_locale" {
				found++
			}
		}
		assert.Equal(t, 4, found)
	})
}

func TestEnsureSessionReloadsWhenMissing(t *testing.T) {
	withTempHome(t, func(string) {
		called := false
		jar, err := ps.EnsureSession(ps.EnsureSessionOptions{
			AutoRefresh: true,
			LoginFn: func() ([]cookies.Record, error) {
				called = true
				return []cookies.Record{mkCookie("psaid", "x", "sis.cbe.ab.ca")}, nil
			},
		})
		require.NoError(t, err)
		assert.True(t, called)
		require.NotNil(t, jar)
	})
}

func TestEnsureSessionLoadsWhenValid(t *testing.T) {
	withTempHome(t, func(string) {
		require.NoError(t, ps.Save([]cookies.Record{
			mkCookie("psaid", "x", "sis.cbe.ab.ca"),
		}))
		// Verify it's saved properly.
		loaded, err := ps.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		require.True(t, ps.HasValidSession(loaded))

		called := false
		jar, err := ps.EnsureSession(ps.EnsureSessionOptions{
			AutoRefresh: true,
			LoginFn: func() ([]cookies.Record, error) {
				called = true
				return nil, nil
			},
		})
		require.NoError(t, err)
		assert.False(t, called)
		require.NotNil(t, jar)
		// sanity: jar returns the loaded cookie
		req, _ := http.NewRequest("GET", "https://sis.cbe.ab.ca/test", nil)
		var found bool
		for _, c := range jar.Cookies(req.URL) {
			t.Logf("jar cookie: %s=%s domain=%q", c.Name, c.Value, c.Domain)
			if c.Name == "psaid" {
				found = true
			}
		}
		assert.True(t, found)
	})
}

// TestEnsureSessionRespectsAutoRefreshOff asserts `sis logout` is sticky
// when --auto-refresh=false: the missing session is an error rather than
// a silent re-login.
func TestEnsureSessionRespectsAutoRefreshOff(t *testing.T) {
	withTempHome(t, func(string) {
		called := false
		_, err := ps.EnsureSession(ps.EnsureSessionOptions{
			AutoRefresh: false,
			LoginFn: func() ([]cookies.Record, error) {
				called = true
				return []cookies.Record{mkCookie("psaid", "x", "sis.cbe.ab.ca")}, nil
			},
		})
		require.Error(t, err)
		assert.False(t, called)
		var authErr *stderrors.AuthError
		require.ErrorAs(t, err, &authErr)
		assert.Equal(t, "NO_SESSION", authErr.Code)
	})
}

// TestReauthenticatePersists asserts the 401 refresh path writes the new
// cookies to disk, not just returns a jar.
func TestReauthenticatePersists(t *testing.T) {
	withTempHome(t, func(string) {
		jar, err := ps.Reauthenticate(func() ([]cookies.Record, error) {
			return []cookies.Record{mkCookie("psaid", "renewed", "sis.cbe.ab.ca")}, nil
		})
		require.NoError(t, err)
		require.NotNil(t, jar)

		loaded, err := ps.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		assert.Equal(t, "renewed", loaded[0].Value)
	})
}
