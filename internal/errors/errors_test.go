package errors_test

import (
	"testing"

	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/stretchr/testify/assert"
)

func TestAuthError(t *testing.T) {
	e := stderrors.New("X", "msg")
	assert.Equal(t, "X", e.Code)
	assert.Equal(t, "msg", e.Error())
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