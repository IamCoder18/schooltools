package cmd

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/httpclient"
	"github.com/IamCoder18/schooltools/internal/ps"
)

// newYearClient returns a ps.Client whose year picker serves a fixed
// fixture, so resolveYearID can be exercised without a live portal.
func newYearClient(t *testing.T) *ps.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/standard_grade_section/student/years", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"abbr":"23-24","yearid":34},{"abbr":"25-26","yearid":35},{"abbr":"26-27","yearid":36}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL
	return c
}

// TestResolveYearIDPicksNewest guards the finding-6/7 fix: the year must
// come from the picker, never from a hardcoded constant, and must be the
// newest available.
func TestResolveYearIDPicksNewest(t *testing.T) {
	c := newYearClient(t)
	got, err := resolveYearID(c, 90000001, 0)
	require.NoError(t, err)
	assert.Equal(t, 36, got)
}

// TestResolveYearIDOverride asserts an explicit --year wins.
func TestResolveYearIDOverride(t *testing.T) {
	c := newYearClient(t)
	got, err := resolveYearID(c, 90000001, 34)
	require.NoError(t, err)
	assert.Equal(t, 34, got)
}

// TestResolveYearIDEmptyPicker asserts an empty picker is an error
// rather than a silent fallback to a baked-in year.
func TestResolveYearIDEmptyPicker(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/standard_grade_section/student/years", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err = resolveYearID(c, 90000001, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entries")
}

// TestResolveYearIDUnorderedPicker asserts the newest year is chosen by
// value, not by position, so a portal that reverses its ordering cannot
// silently pin the CLI to an old year.
func TestResolveYearIDUnorderedPicker(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/standard_grade_section/student/years", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"abbr":"26-27","yearid":36},{"abbr":"23-24","yearid":34},{"abbr":"25-26","yearid":35}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL

	got, err := resolveYearID(c, 90000001, 0)
	require.NoError(t, err)
	assert.Equal(t, 36, got)
}

// TestResolveStudentDCIDParsesDashboard asserts the credentials-link
// scrape still resolves the DCID from a real dashboard body.
func TestResolveStudentDCIDParsesDashboard(t *testing.T) {
	t.Setenv("SIS_DCID", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><a href="/guardian/credentials.html?frn=00190000001">me</a></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL

	got, err := resolveStudentDCID(c)
	require.NoError(t, err)
	assert.Equal(t, int64(90000001), got)
}

// TestResolveStudentDCIDReportsStaleSession asserts a body that is not
// the guardian dashboard is reported as an expired session rather than
// an opaque "could not parse DCID" message.
func TestResolveStudentDCIDReportsStaleSession(t *testing.T) {
	t.Setenv("SIS_DCID", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/home.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>please sign in</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err = resolveStudentDCID(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "likely expired")
	assert.Contains(t, err.Error(), "schooltools sis login")
}

// TestResolveStudentDCIDEnvOverride asserts SIS_DCID short-circuits the
// scrape entirely.
func TestResolveStudentDCIDEnvOverride(t *testing.T) {
	t.Setenv("SIS_DCID", "90000001")
	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)

	got, err := resolveStudentDCID(c)
	require.NoError(t, err)
	assert.Equal(t, int64(90000001), got)
}

// TestLoadEnvOptionalToleratesMissing asserts a missing .env is not an
// error, so read-only sis commands work with only a saved session.
func TestLoadEnvOptionalToleratesMissing(t *testing.T) {
	err := loadEnvOptional(filepath.Join(t.TempDir(), "missing.env"))
	require.NoError(t, err)
}

// TestLoadEnvOptionalPropagatesRealErrors asserts permission and I/O
// failures are still surfaced.
func TestLoadEnvOptionalPropagatesRealErrors(t *testing.T) {
	err := loadEnvOptional(t.TempDir())
	require.Error(t, err)
}
