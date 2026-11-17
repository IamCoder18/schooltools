package env_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/env"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// clearSISEnv unsets every variable GetSISCreds consults. LoadEnv writes
// into the process environment, so without this a value set by one
// subtest leaks into the next.
func clearSISEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SIS_STUDENT_NUMBER", "CBE_STUDENT_NUMBER", "CBE_EMAIL", "SIS_PASSWORD", "CBE_PASSWORD"} {
		t.Setenv(k, "")
		require.NoError(t, os.Unsetenv(k))
	}
}

// LoadEnv must export SIS_DCID into the process environment, not just the
// credentials the `sis` login path reads. resolveStudentDCID in
// cmd/sis_whoami.go reads SIS_DCID with os.Getenv, so a value written in
// .env was previously invisible on the valid-session path because nothing
// loaded the file before the lookup.
func TestLoadEnvExportsSISDCID(t *testing.T) {
	p := writeEnv(t, "SIS_DCID=90000001\nSIS_STUDENT_NUMBER=1234567890\nSIS_PASSWORD=hunter2\n")
	require.NoError(t, env.LoadEnv(p))
	assert.Equal(t, "90000001", os.Getenv("SIS_DCID"))
}

// TestLoadEnvMissingWrapsNotExist asserts the missing-file error still
// satisfies errors.Is(err, fs.ErrNotExist). The sis command tree uses
// that to tolerate a missing .env when a saved session is already valid.
func TestLoadEnvMissingWrapsNotExist(t *testing.T) {
	err := env.LoadEnv(filepath.Join(t.TempDir(), "missing.env"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, fs.ErrNotExist))
	assert.Contains(t, err.Error(), "env file not found")
}

func TestGetSISCredsPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantAccount string
	}{
		{
			name:        "SIS_STUDENT_NUMBER wins",
			body:        "SIS_STUDENT_NUMBER=111\nCBE_STUDENT_NUMBER=222\nCBE_EMAIL=a@b.ca\n",
			wantAccount: "111",
		},
		{
			name:        "CBE_STUDENT_NUMBER is second",
			body:        "CBE_STUDENT_NUMBER=222\nCBE_EMAIL=a@b.ca\n",
			wantAccount: "222",
		},
		{
			name:        "CBE_EMAIL is the last resort",
			body:        "CBE_EMAIL=a@b.ca\n",
			wantAccount: "a@b.ca",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSISEnv(t)
			require.NoError(t, env.LoadEnv(writeEnv(t, tc.body+"SIS_PASSWORD=hunter2\n")))
			creds, err := env.GetSISCreds()
			require.NoError(t, err)
			assert.Equal(t, tc.wantAccount, creds.Account)
			assert.Equal(t, "hunter2", creds.Password)
		})
	}
}

func TestGetSISCredsPasswordFallback(t *testing.T) {
	clearSISEnv(t)
	require.NoError(t, env.LoadEnv(writeEnv(t, "SIS_STUDENT_NUMBER=111\nCBE_PASSWORD=fromcbe\n")))
	creds, err := env.GetSISCreds()
	require.NoError(t, err)
	assert.Equal(t, "fromcbe", creds.Password)
}

func TestGetSISCredsMissing(t *testing.T) {
	clearSISEnv(t)
	_, err := env.GetSISCreds()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SIS_STUDENT_NUMBER")
	// The message must not point at flags that do not exist.
	assert.NotContains(t, err.Error(), "--password")
}

func TestGetCredsUnchangedBySISHelper(t *testing.T) {
	t.Setenv("CBE_EMAIL", "you@example.com")
	t.Setenv("CBE_PASSWORD", "hunter2")
	creds, err := env.GetCreds()
	require.NoError(t, err)
	assert.Equal(t, "you@example.com", creds.Email)
	assert.Equal(t, "hunter2", creds.Password)
}

// TestGetSISCredsWarnsOnEmailAccount asserts the documented stderr
// warning fires when the CBE_EMAIL fallback resolves the account, since
// MyCBE rejects an email address as the username.
func TestGetSISCredsWarnsOnEmailAccount(t *testing.T) {
	clearSISEnv(t)
	require.NoError(t, env.LoadEnv(writeEnv(t, "CBE_EMAIL=user@example.com\nCBE_PASSWORD=hunter2\n")))

	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved := os.Stderr
	os.Stderr = w

	_, credsErr := env.GetSISCreds()

	os.Stderr = saved
	require.NoError(t, w.Close())
	out, readErr := io.ReadAll(r)
	require.NoError(t, readErr)

	require.NoError(t, credsErr)
	got := string(out)
	assert.Contains(t, got, "SIS_STUDENT_NUMBER")
	assert.Contains(t, got, "user@example.com")
}

// TestGetSISCredsNoWarnForStudentNumber asserts the warning is not
// emitted for a correctly configured student number.
func TestGetSISCredsNoWarnForStudentNumber(t *testing.T) {
	clearSISEnv(t)
	require.NoError(t, env.LoadEnv(writeEnv(t, "SIS_STUDENT_NUMBER=1234567890\nSIS_PASSWORD=hunter2\n")))

	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved := os.Stderr
	os.Stderr = w

	_, credsErr := env.GetSISCreds()

	os.Stderr = saved
	require.NoError(t, w.Close())
	out, readErr := io.ReadAll(r)
	require.NoError(t, readErr)

	require.NoError(t, credsErr)
	assert.Empty(t, string(out))
}
