package session_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionExpirySoonest(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(2 * time.Hour)
	later := now.Add(20 * time.Hour)
	records := []cookies.Record{
		mkCookie("d2lSessionVal", "a", func(r *cookies.Record) { r.Expires = later }),
		mkCookie("d2lSecureSessionVal", "b", func(r *cookies.Record) { r.Expires = earlier }),
		mkCookie("MSISAuth", "c", func(r *cookies.Record) { r.Expires = now.Add(48 * time.Hour) }),
		mkCookie("MSISSignIn", "d"), // session-only, no Expires
		mkCookie("adfs_session", "e", func(r *cookies.Record) { r.Expires = now.Add(-time.Hour) }), // expired
	}
	ex := session.ComputeExpiryForTest(records, time.Time{}, now)
	assert.False(t, ex.Soonest.IsZero())
	assert.Equal(t, earlier, ex.Soonest)
	assert.Equal(t, "d2lSecureSessionVal", ex.SoonestName)
	assert.True(t, ex.HasSession, "MSISSignIn is session-only")
	assert.False(t, ex.SAMLAvailable)
	assert.Len(t, ex.PerCookie, 5)
}

func TestSessionExpiryNoCookies(t *testing.T) {
	now := time.Now()
	ex := session.ComputeExpiryForTest(nil, time.Time{}, now)
	assert.True(t, ex.Soonest.IsZero())
	assert.False(t, ex.HasSession)
}

func TestSessionExpiryWithSAML(t *testing.T) {
	withTempHome(t, func(string) {
		now := time.Now()
		nb := now.Add(-time.Minute)
		noa := now.Add(2 * time.Hour)
		rec := session.NewSAMLRecord(base64.StdEncoding.EncodeToString([]byte(
			`<saml:Assertion><saml:Conditions NotBefore="`+nb.Format(time.RFC3339)+`" NotOnOrAfter="`+noa.Format(time.RFC3339)+`"/></saml:Assertion>`,
		)), "relay", now)
		require.NoError(t, session.SaveSAML(rec))
		ex, err := session.SessionExpiry()
		require.NoError(t, err)
		assert.True(t, ex.SAMLAvailable)
		assert.True(t,
			noa.Truncate(time.Second).Equal(ex.SAMLNotAfter.Truncate(time.Second)),
			"SAMLNotAfter mismatch: want %s, got %s", noa.Truncate(time.Second), ex.SAMLNotAfter.Truncate(time.Second),
		)
	})
}

func TestNewSAMLRecordParsesWindow(t *testing.T) {
	xml := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"><saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"><saml:Conditions NotBefore="2026-09-04T12:00:00Z" NotOnOrAfter="2026-09-04T13:00:00Z"/></saml:Assertion></samlp:Response>`
	encoded := base64.StdEncoding.EncodeToString([]byte(xml))
	rec := session.NewSAMLRecord(encoded, "rs", time.Date(2026, 9, 4, 11, 59, 0, 0, time.UTC))
	assert.Equal(t, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC), rec.NotBefore)
	assert.Equal(t, time.Date(2026, 9, 4, 13, 0, 0, 0, time.UTC), rec.NotOnOrAfter)
	assert.Equal(t, "rs", rec.RelayState)
	assert.True(t, strings.Contains(rec.Decoded, "NotOnOrAfter=\"2026-09-04T13:00:00Z\""))
}

func TestNewSAMLRecordInvalidBase64(t *testing.T) {
	rec := session.NewSAMLRecord("!!!not base64!!!", "", time.Time{})
	assert.True(t, strings.HasPrefix(rec.Decoded, "(base64 decode failed"))
	assert.Empty(t, rec.NotOnOrAfter)
}

func TestSAMLPath(t *testing.T) {
	withTempHome(t, func(string) {
		assert.Equal(t, session.SessionPath()[0:len(session.SessionPath())-len("session.json")]+"saml-last.json",
			session.SAMLPath())
	})
}
