package ps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IamCoder18/schooltools/internal/cookies"
	"golang.org/x/net/publicsuffix"
)

// SessionPath returns the canonical SIS session file path.
//
// We deliberately keep this separate from the D2L session.json: the
// PS portal uses a different cookie set (psaid, currentSchool, …) and
// the expiry semantics are independent.
func SessionPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "schooltools", "sis-session.json")
}

// Save persists the four PowerSchool session cookies to disk at
// SessionPath(). The file is 0600 and the directory is 0700, matching
// the D2L session.json rules in SECURITY.md.
func Save(records []cookies.Record) error {
	fresh := make([]cookies.Record, 0, len(records))
	for _, r := range records {
		if !r.IsExpired(time.Now()) {
			fresh = append(fresh, r)
		}
	}
	dir := filepath.Dir(SessionPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return err
	}
	path := SessionPath()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Load returns the persisted PowerSchool cookies. Session cookies
// (Expires == zero) are returned as-is; expired cookies are dropped.
func Load() ([]cookies.Record, error) {
	records, err := loadFileRaw()
	if err != nil {
		return nil, err
	}
	out := records[:0]
	for _, r := range records {
		if !r.IsExpired(time.Now()) {
			out = append(out, r)
		}
	}
	return out, nil
}

func loadFileRaw() ([]cookies.Record, error) {
	raw, err := os.ReadFile(SessionPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read SIS session file %s: %v", SessionPath(), err)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("SIS session file %s is corrupt (%v); delete it and run 'schooltools sis login' again", SessionPath(), err)
	}
	records := make([]cookies.Record, 0, len(arr))
	for _, m := range arr {
		var r cookies.Record
		if err := json.Unmarshal(m, &r); err != nil {
			continue
		}
		records = append(records, r)
	}
	return records, nil
}

// Clear removes the saved SIS session file. A missing file is not an
// error.
func Clear() error {
	err := os.Remove(SessionPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasValidSession reports whether the saved cookie set includes a
// non-expired psaid cookie — the PowerSchool equivalent of
// session.HasValidSession() for D2L. JSESSIONID alone is not enough:
// PowerSchool also issues a `psaid` cookie on login and subsequent
// requests will 401/403 without it.
//
// A psaid cookie is considered valid when:
//   - its domain ends with "cbe.ab.ca", OR
//   - its domain matches BaseURL's host (covers test environments
//     against 127.0.0.1 or any other staging host).
func HasValidSession(records []cookies.Record) bool {
	if len(records) == 0 {
		return false
	}
	now := time.Now()
	baseHost := strings.TrimPrefix(strings.Split(BaseURLHost(), ":")[0], ".")
	for _, r := range records {
		if r.Name != "psaid" {
			continue
		}
		if r.IsExpired(now) {
			continue
		}
		if r.Domain == "" {
			continue
		}
		d := strings.TrimPrefix(strings.Split(r.Domain, ":")[0], ".")
		if endsWithCBEDomain(d) {
			return true
		}
		if baseHost != "" && d == baseHost {
			return true
		}
	}
	return false
}

func endsWithCBEDomain(domain string) bool {
	const suffix = "cbe.ab.ca"
	if len(domain) < len(suffix) {
		return false
	}
	return domain[len(domain)-len(suffix):] == suffix
}

// LoadJar builds a fresh cookie jar from the persisted records. The
// cookie domain in PowerSchool responses is host-only (no leading dot)
// so the jar handles per-host storage automatically.
func LoadJar() (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	records, err := Load()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.IsExpired(time.Now()) {
			continue
		}
		host := strings.TrimPrefix(r.Domain, ".")
		u := &url.URL{Scheme: "https", Host: host, Path: r.Path}
		jar.SetCookies(u, []*http.Cookie{recordToCookie(r)})
	}
	return jar, nil
}

func recordToCookie(r cookies.Record) *http.Cookie {
	return &http.Cookie{
		Name:     r.Name,
		Value:    r.Value,
		Domain:   r.Domain,
		Path:     r.Path,
		Expires:  r.Expires,
		Secure:   r.Secure,
		HttpOnly: r.HttpOnly,
		SameSite: http.SameSite(r.SameSite),
	}
}
