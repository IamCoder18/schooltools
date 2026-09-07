package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadTokenRoundTrip(t *testing.T) {
	withTempHome(t, func(string) {
		now := time.Now().UTC()
		rec := session.NewTokenFromJWT(
			"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiI3ODIwMDIiLCJ0ZW5hbnRpZCI6Ijg1ZGNjNWNjIn0.sig",
			"Bearer", "*:*:*",
			now.Add(time.Hour), now,
		)
		require.NoError(t, session.SaveToken(rec))
		got, err := session.LoadToken()
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, rec.AccessToken, got.AccessToken)
		assert.Equal(t, "Bearer", got.TokenType)
		assert.Equal(t, "*:*:*", got.Scope)
		assert.Equal(t, "782002", got.UserID)
		assert.Equal(t, "85dcc5cc", got.TenantID)
		assert.True(t, got.ExpiresAt.Equal(rec.ExpiresAt))
	})
}

func TestLoadTokenMissing(t *testing.T) {
	withTempHome(t, func(string) {
		got, err := session.LoadToken()
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestLoadTokenCorrupt(t *testing.T) {
	withTempHome(t, func(home string) {
		dir := filepath.Join(home, ".config", "schooltools")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		path := filepath.Join(dir, "token.json")
		require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
		_, err := session.LoadToken()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "corrupt")
	})
}

func TestSaveTokenRejectsEmpty(t *testing.T) {
	err := session.SaveToken(session.TokenRecord{})
	require.Error(t, err)
}

func TestTokenPath(t *testing.T) {
	withTempHome(t, func(string) {
		dir := filepath.Dir(session.SessionPath())
		assert.Equal(t, filepath.Join(dir, "token.json"), session.TokenPath())
	})
}

func TestTokenExpiredAndRemaining(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	r := session.TokenRecord{ExpiresAt: now.Add(15 * time.Minute)}
	assert.False(t, r.IsExpired(now))
	assert.Equal(t, 15*time.Minute, r.Remaining(now))

	r = session.TokenRecord{ExpiresAt: now.Add(-time.Minute)}
	assert.True(t, r.IsExpired(now))
	assert.Equal(t, time.Duration(0), r.Remaining(now))

	r = session.TokenRecord{}
	assert.False(t, r.IsExpired(now))
	assert.Equal(t, time.Duration(0), r.Remaining(now))
}

func TestNewTokenFromJWTDecodesClaims(t *testing.T) {
	// Build a JWT with payload {"sub":"42","tenantid":"abc","iss":"https://x","aud":"https://y"}
	payload, _ := json.Marshal(map[string]any{
		"sub":      "42",
		"tenantid": "abc",
		"iss":      "https://x",
		"aud":      "https://y",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	header := []byte(`{"alg":"none","typ":"JWT"}`)
	token := base64URL(header) + "." + base64URL(payload) + ".signature"

	now := time.Now()
	r := session.NewTokenFromJWT(token, "", "*", now.Add(time.Hour), now)
	assert.Equal(t, "42", r.UserID)
	assert.Equal(t, "abc", r.TenantID)
	assert.Equal(t, "https://x", r.Issuer)
	assert.Equal(t, "https://y", r.Audience)
	// IssuedAt defaulted to now
	assert.WithinDuration(t, now, r.IssuedAt, time.Second)
	// TokenType defaulted to Bearer
	assert.Equal(t, "Bearer", r.TokenType)
}

func base64URL(b []byte) string {
	// std base64 with URL-safe alphabet, no padding
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out strings.Builder
	v := uint(0)
	nb := 0
	for _, c := range b {
		v = (v << 8) | uint(c)
		nb += 8
		for nb >= 6 {
			nb -= 6
			out.WriteByte(alphabet[(v>>nb)&0x3F])
		}
	}
	if nb > 0 {
		out.WriteByte(alphabet[(v<<(6-nb))&0x3F])
	}
	return out.String()
}
