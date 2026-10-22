package cmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/session"
)

func TestExpiryPhrase(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	require.Contains(t, expiryPhrase(now.Add(5*24*time.Hour), now), "expires in 5 days")
	require.Contains(t, expiryPhrase(now.Add(-2*time.Hour), now), "expired 2 hours ago")
}

func TestWhenOfZero(t *testing.T) {
	require.Equal(t, "—", whenOf(time.Time{}))
	require.NotEqual(t, "—", whenOf(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)))
}

func TestTokenKVExpiry(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	tok := session.TokenRecord{
		TenantID:  "cbe",
		UserID:    "42",
		IssuedAt:  now,
		ExpiresAt: now.Add(48 * time.Hour),
	}
	out := tokenKV(tok, now)
	require.Contains(t, out, "Tenant/user")
	require.Contains(t, out, "cbe / 42")
	require.Contains(t, out, "expires in 2 days")

	expired := tokenKV(session.TokenRecord{IssuedAt: now, ExpiresAt: now.Add(-time.Minute)}, now)
	require.Contains(t, expired, "expired")

	none := tokenKV(session.TokenRecord{IssuedAt: now}, now)
	require.Contains(t, none, "no expiry recorded")
}

func TestRedactedToken(t *testing.T) {
	tok := session.TokenRecord{
		AccessToken:  "super-secret",
		RefreshToken: "refresh-secret",
		TenantID:     "cbe",
		UserID:       "42",
	}
	raw, err := json.Marshal(redactedToken(tok))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "super-secret")
	require.NotContains(t, string(raw), "refresh-secret")
	require.Contains(t, string(raw), "[redacted]")
	require.Contains(t, string(raw), `"tenant_id":"cbe"`)
	require.Equal(t, "super-secret", tok.AccessToken)
}
