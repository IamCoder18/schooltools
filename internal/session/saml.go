package session

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// SAMLRecord is a snapshot of the SAMLResponse captured during login, persisted
// alongside session.json so callers can inspect the assertion that produced the
// current session (or replay/re-sign it for testing).
type SAMLRecord struct {
	CapturedAt   time.Time `json:"captured_at"`
	NotBefore    time.Time `json:"not_before,omitempty"`
	NotOnOrAfter time.Time `json:"not_on_or_after,omitempty"`
	RelayState   string    `json:"relay_state,omitempty"`
	Response     string    `json:"response"`
	Decoded      string    `json:"decoded_xml"`
}

// SAMLPath returns the on-disk path for the most recent captured SAML token.
// It lives next to session.json so a backup of ~/.config/schooltools captures
// everything needed to reconstruct the auth state.
func SAMLPath() string {
	return filepath.Join(filepath.Dir(SessionPath()), "saml-last.json")
}

// SaveSAML writes the record to SAMLPath() with 0600 perms, creating the
// parent directory if needed. Returns any write error.
func SaveSAML(r SAMLRecord) error {
	dir := filepath.Dir(SAMLPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if r.CapturedAt.IsZero() {
		r.CapturedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SAMLPath(), data, 0o600)
}

// LoadSAML reads the previously captured SAML token. Returns (nil, nil) when
// no capture file exists yet.
func LoadSAML() (*SAMLRecord, error) {
	raw, err := os.ReadFile(SAMLPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r SAMLRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// NewSAMLRecord builds a record from a base64 SAMLResponse + optional
// RelayState, decoding the response and parsing NotBefore/NotOnOrAfter from
// the resulting XML. CapturedAt defaults to now (UTC) if zero.
func NewSAMLRecord(response, relayState string, capturedAt time.Time) SAMLRecord {
	r := SAMLRecord{
		CapturedAt: capturedAt,
		RelayState: relayState,
		Response:   response,
	}
	if r.CapturedAt.IsZero() {
		r.CapturedAt = time.Now().UTC()
	}
	if response == "" {
		return r
	}
	decoded, err := base64.StdEncoding.DecodeString(response)
	if err != nil {
		r.Decoded = "(base64 decode failed: " + err.Error() + ")"
		return r
	}
	r.Decoded = string(decoded)
	r.NotBefore, r.NotOnOrAfter = parseSAMLWindow(decoded)
	return r
}

var samlWindowRe = regexp.MustCompile(`(?s)NotBefore="([^"]+)"|NotOnOrAfter="([^"]+)"`)

func parseSAMLWindow(xml []byte) (notBefore, notOnOrAfter time.Time) {
	for _, m := range samlWindowRe.FindAllSubmatch(xml, -1) {
		raw := ""
		if m[1] != nil {
			raw = string(m[1])
		} else if m[2] != nil {
			raw = string(m[2])
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			continue
		}
		if m[1] != nil {
			notBefore = t
		} else {
			notOnOrAfter = t
		}
	}
	return notBefore, notOnOrAfter
}
