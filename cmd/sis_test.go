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

func TestResolveYearIDPicksNewest(t *testing.T) {
	c := newYearClient(t)
	got, err := resolveYearID(c, 90000001, 0)
	require.NoError(t, err)
	assert.Equal(t, 36, got)
}

func TestResolveYearIDOverride(t *testing.T) {
	c := newYearClient(t)
	got, err := resolveYearID(c, 90000001, 34)
	require.NoError(t, err)
	assert.Equal(t, 34, got)
}

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

func TestResolveStudentDCIDEnvOverride(t *testing.T) {
	t.Setenv("SIS_DCID", "90000001")
	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)

	got, err := resolveStudentDCID(c)
	require.NoError(t, err)
	assert.Equal(t, int64(90000001), got)
}

func TestLoadEnvOptionalToleratesMissing(t *testing.T) {
	err := loadEnvOptional(filepath.Join(t.TempDir(), "missing.env"))
	require.NoError(t, err)
}

func TestLoadEnvOptionalPropagatesRealErrors(t *testing.T) {
	err := loadEnvOptional(t.TempDir())
	require.Error(t, err)
}
