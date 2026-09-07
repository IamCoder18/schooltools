package session

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TokenRecord is the Brightspace OAuth access token we mint after a successful
// SAML login, persisted to token.json next to session.json / saml-last.json.
//
// D2L stores this same token in browser LocalStorage["D2L.Fetch.Tokens"].
// In our CLI we treat it as a first-class session artifact because the LE
// ("/d2l/api/le/...") APIs work fine on session cookies alone, but the newer
// Brightspace APIs (api.brightspace.com) require this bearer JWT.
type TokenRecord struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"` // "Bearer"
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	IssuedAt     time.Time `json:"issued_at"`
	// Claims below are pulled from the JWT payload for diagnostics; the raw
	// token is the source of truth.
	TenantID string `json:"tenant_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// TokenPath returns the on-disk path for the captured Brightspace token. It
// lives next to session.json so a backup of ~/.config/schooltools carries the
// full auth state.
func TokenPath() string {
	return filepath.Join(filepath.Dir(SessionPath()), "token.json")
}

// SaveToken writes the record to TokenPath() with 0600 perms, creating the
// parent directory if needed.
func SaveToken(r TokenRecord) error {
	if r.AccessToken == "" {
		return errors.New("token: access_token is empty")
	}
	if r.IssuedAt.IsZero() {
		r.IssuedAt = time.Now().UTC()
	}
	if r.ExpiresAt.IsZero() {
		// Default to 1h if server didn't tell us.
		r.ExpiresAt = r.IssuedAt.Add(time.Hour)
	}
	if r.TokenType == "" {
		r.TokenType = "Bearer"
	}
	dir := filepath.Dir(TokenPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(TokenPath(), data, 0o600)
}

// LoadToken reads the previously captured Brightspace token. Returns (nil, nil)
// when no capture file exists yet.
func LoadToken() (*TokenRecord, error) {
	raw, err := os.ReadFile(TokenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r TokenRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("token.json is corrupt (%v)", err)
	}
	return &r, nil
}

// NewTokenFromJWT builds a TokenRecord from the raw OAuth response body.
// `issuedAt` is the time the response was received; if zero, now is used.
// Decoded claims (tenant, user, iss, aud) are pulled from the JWT payload
// for display in `schooltools session`.
func NewTokenFromJWT(accessToken, tokenType, scope string, expiresAt, issuedAt time.Time) TokenRecord {
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	if expiresAt.IsZero() {
		expiresAt = issuedAt.Add(time.Hour)
	}
	if tokenType == "" {
		tokenType = "Bearer"
	}
	r := TokenRecord{
		AccessToken: accessToken,
		TokenType:   tokenType,
		Scope:       scope,
		ExpiresAt:   expiresAt,
		IssuedAt:    issuedAt,
	}
	if claims, ok := decodeJWTClaims(accessToken); ok {
		r.TenantID, _ = claims["tenantid"].(string)
		r.UserID, _ = claims["sub"].(string)
		r.Issuer, _ = claims["iss"].(string)
		if aud, ok := claims["aud"].(string); ok {
			r.Audience = aud
		}
	}
	return r
}

// decodeJWTClaims extracts the payload of a JWS in compact form without
// verifying the signature. Returns the JSON claims as a map.
func decodeJWTClaims(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Fall back to standard base64 if needed.
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, false
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

// IsExpired reports whether the token is past its expiry. Mirrors the same
// "zero ExpiresAt means non-expiring" convention as cookies.Record.
func (r TokenRecord) IsExpired(now time.Time) bool {
	if r.ExpiresAt.IsZero() {
		return false
	}
	return !r.ExpiresAt.After(now)
}

// Remaining returns the duration until the token expires. Negative if expired.
// Zero for tokens without an expiry.
func (r TokenRecord) Remaining(now time.Time) time.Duration {
	if r.ExpiresAt.IsZero() {
		return 0
	}
	d := r.ExpiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}
