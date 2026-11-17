package errors_test

import (
	"errors"
	"testing"

	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthError(t *testing.T) {
	e := stderrors.New("X", "msg")
	assert.Equal(t, "X", e.Code)
	assert.Equal(t, "msg", e.Error())
	// AuthError must satisfy the same `interface{ AuthCode() string }`
	// assertion the other auth error types do, or call sites that extract
	// the code for the auth log silently fall back to a generic label.
	assert.Equal(t, "X", e.AuthCode())

	var coder interface{ AuthCode() string }
	require.True(t, errors.As(error(e), &coder))
	assert.Equal(t, "X", coder.AuthCode())
}

func TestSamlConsumedError(t *testing.T) {
	s := &stderrors.SamlConsumedError{
		LastURL: "https://d2l/",
		Cookies: []stderrors.SamlCookieSummary{{Key: "d2lSessionVal", Domain: ".cbe.ab.ca"}},
	}
	assert.Equal(t, "SAML_CONSUMED", s.AuthCode())
	assert.Equal(t, "https://d2l/", s.LastURL)
	assert.Equal(t, "d2lSessionVal", s.Cookies[0].Key)
	assert.Contains(t, s.Error(), "https://d2l/")
}

func TestMfaRequiredError(t *testing.T) {
	m := &stderrors.MfaRequiredError{URL: "https://adfs/mfa"}
	assert.Equal(t, "MFA_REQUIRED", m.AuthCode())
	assert.Contains(t, m.Error(), "MFA")
	assert.Contains(t, m.Error(), "second-factor")
}

func TestAdfsRejectedError(t *testing.T) {
	r := &stderrors.AdfsRejectedError{Reason: "bad password"}
	assert.Equal(t, "ADFS_REJECTED", r.AuthCode())
	assert.Contains(t, r.Error(), "bad password")
}