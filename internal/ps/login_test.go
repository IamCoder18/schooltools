package ps_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/cookies"
	stderrors "github.com/IamCoder18/schooltools/internal/errors"
	"github.com/IamCoder18/schooltools/internal/ps"
)

// TestLoginHappyPath exercises the full login flow against an httptest
// mock that mirrors PowerSchool's: GET /public/ seeds a JSESSIONID,
// POST /guardian/home.html sets psaid + JSESSIONID (rotated) +
// currentSchool + current_locale and 302s to /guardian/home.html.
func TestLoginHappyPath(t *testing.T) {
	var postedForm url.Values
	var srvLoopback string
	mux := http.NewServeMux()
	mux.HandleFunc("/public/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "SEEDJS", Path: "/", Secure: true, HttpOnly: true})
		_, _ = w.Write([]byte("<html>login page</html>"))
	})
	mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The 302 follow-up arrives here. Make sure the four
			// required cookies are present.
			for _, want := range []string{"JSESSIONID", "psaid", "currentSchool", "current_locale"} {
				_, err := r.Cookie(want)
				assert.NoError(t, err, "missing %s cookie on follow-up GET", want)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>dashboard</html>"))
			return
		}
		// POST: capture the form body and issue the 302.
		postedForm = r.URL.Query()
		_ = r.ParseForm()
		postedForm = r.PostForm
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "ROTATEDJS", Path: "/", Secure: true, HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "psaid", Value: "PSAIDVAL", Path: "/", Secure: true, HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "currentSchool", Value: "816", Path: "/", Secure: true, HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "current_locale", Value: "en_US", Path: "/", Secure: true, HttpOnly: true})
		w.Header().Set("Location", srvLoopback+"/guardian/home.html")
		w.WriteHeader(http.StatusFound)
	})
	// First GET on home.html (follow-up) — handled by the guard above.

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	srvLoopback = srv.URL

	// Override BaseURL so the mock's redirect location matches.
	prev := ps.BaseURL
	ps.BaseURL = srv.URL
	t.Cleanup(func() { ps.BaseURL = prev })

	records, err := ps.Login(ps.LoginOptions{
		Account:  "990000001",
		Password: "hunter2",
	})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	assert.True(t, ps.HasValidSession(records))

	// Validate the form body.
	require.NotNil(t, postedForm)
	assert.Equal(t, "990000001", postedForm.Get("account"))
	assert.Equal(t, "hunter2", postedForm.Get("pw"))
	assert.Equal(t, "hunter2", postedForm.Get("dbpw"))
	assert.Equal(t, "hunter2", postedForm.Get("ldappassword"))
	assert.Equal(t, "User Id and Password Credential", postedForm.Get("credentialType"))

	// Validate that the seeded cookie is overwritten by the rotated one.
	var hasRotated, hasSeeded bool
	for _, r := range records {
		if r.Name == "JSESSIONID" && r.Value == "ROTATEDJS" {
			hasRotated = true
		}
		if r.Name == "JSESSIONID" && r.Value == "SEEDJS" {
			hasSeeded = true
		}
	}
	assert.True(t, hasRotated, "rotated JSESSIONID missing from saved cookies")
	assert.False(t, hasSeeded, "stale seeded JSESSIONID should be replaced by the rotated one")
}

func TestLoginInvalidCreds(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/public/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "x", Path: "/"})
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
		// 200 with "Invalid Username or Password!" inline.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<div class="feedback-alert">Invalid Username or Password!</div>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ps.BaseURL = srv.URL
	t.Cleanup(func() { ps.BaseURL = "https://sis.cbe.ab.ca" })

	_, err := ps.Login(ps.LoginOptions{Account: "bad", Password: "wrong"})
	require.Error(t, err)
	var authErr *stderrors.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, "INVALID_CREDS", authErr.Code)
}

func TestLoginNoPSAID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/public/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "x", Path: "/"})
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		// 302 but only JSESSIONID set, no psaid.
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "y", Path: "/"})
		w.Header().Set("Location", "/guardian/home.html")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ps.BaseURL = srv.URL
	t.Cleanup(func() { ps.BaseURL = "https://sis.cbe.ab.ca" })

	_, err := ps.Login(ps.LoginOptions{Account: "a", Password: "b"})
	require.Error(t, err)
	var authErr *stderrors.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, "NO_PSAID", authErr.Code)
}

func TestLoginMissingCreds(t *testing.T) {
	_, err := ps.Login(ps.LoginOptions{})
	require.Error(t, err)
	var authErr *stderrors.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, "MISSING_CREDS", authErr.Code)
}

// Test that EnsureSession calls loginFn when the saved cookies are
// invalid/missing and persists whatever loginFn returns.
func TestEnsureSessionReauth(t *testing.T) {
	withTempHome(t, func(string) {
		calls := 0
		jar, err := ps.EnsureSession(ps.EnsureSessionOptions{
			AutoRefresh: true,
			LoginFn: func() ([]cookies.Record, error) {
				calls++
				return []cookies.Record{mkCookie("psaid", "fresh", "sis.cbe.ab.ca")}, nil
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
		require.NotNil(t, jar)

		// Saved file should now contain the fresh cookie.
		loaded, err := ps.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		assert.Equal(t, "psaid", loaded[0].Name)
		assert.Equal(t, "fresh", loaded[0].Value)
	})
}

// TestInvalidCredsBannerVariants covers the bodies that must be treated
// as a credential rejection: the banner text itself, and the follow-up
// page PowerSchool serves when the POST is redirected back to the login
// form (MFA, lockout, exhausted session) with no banner.
func TestInvalidCredsBannerVariants(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "explicit invalid banner",
			body: `<div class="feedback-alert">Invalid Username or Password!</div>`,
			code: "INVALID_CREDS",
		},
		{
			name: "login form re-rendered after redirect",
			body: `<html><body><form action="/public/home.html"><input id="username"></form></body></html>`,
			code: "LOGIN_REDIRECTED_TO_LOGIN",
		},
		{
			name: "guardian page is a success",
			body: `<html><a href="/guardian/home.html">Home</a><a href="/guardian/grades.html">Grades</a></html>`,
			code: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/public/", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("ok"))
			})
			mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				for _, c := range []string{"psaid", "JSESSIONID"} {
					http.SetCookie(w, &http.Cookie{Name: c, Value: "v", Path: "/", Secure: true})
				}
				w.Header().Set("Location", "/guardian/home.html")
				w.WriteHeader(http.StatusFound)
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			prev := ps.BaseURL
			ps.BaseURL = srv.URL
			t.Cleanup(func() { ps.BaseURL = prev })

			_, err := ps.Login(ps.LoginOptions{Account: "a", Password: "b"})
			if tc.code == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var authErr *stderrors.AuthError
			require.ErrorAs(t, err, &authErr)
			assert.Equal(t, tc.code, authErr.Code)
		})
	}
}

// TestLoginRejectsOffHostRedirect asserts the credential POST will not
// follow a Location pointing at another origin.
func TestLoginRejectsOffHostRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("off-host redirect was followed to %s", r.URL)
	}))
	t.Cleanup(evil.Close)

	mux := http.NewServeMux()
	mux.HandleFunc("/public/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<a href="/guardian/home.html">Home</a>`))
			return
		}
		w.Header().Set("Location", evil.URL+"/steal")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	prev := ps.BaseURL
	ps.BaseURL = srv.URL
	t.Cleanup(func() { ps.BaseURL = prev })

	_, err := ps.Login(ps.LoginOptions{Account: "a", Password: "b"})
	require.Error(t, err)
	var authErr *stderrors.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, "LOGIN_FOLLOW_FAILED", authErr.Code)
	assert.NotContains(t, authErr.Message, "steal")
}
