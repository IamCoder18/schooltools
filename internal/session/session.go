package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/httpclient"
)

var sessionCookieNames = regexp.MustCompile(`^(d2lSessionVal|d2lSecureSessionVal|d2l\.SESSIONID|d2l\.session|d2lSameSiteCanaryA|d2lSameSiteCanaryB)$`)

func SessionPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "schooltools", "session.json")
}

func isExpired(r cookies.Record) bool {
	return r.IsExpired(time.Now())
}

func isPersisted(r cookies.Record) bool {
	return !isExpired(r)
}

type Diagnostic struct {
	Path               string
	Exists             bool
	Mode               os.FileMode
	CookieCount        int
	CookieNames        []string
	ExpiredCookieNames []string
}

func Diagnose() Diagnostic {
	path := SessionPath()
	st, err := os.Stat(path)
	if err != nil {
		return Diagnostic{Path: path, Exists: false}
	}
	mode := st.Mode().Perm()
	records, _ := loadFileRaw()
	var names, expired []string
	for _, c := range records {
		names = append(names, c.Name)
		if isExpired(c) {
			expired = append(expired, c.Name)
		}
	}
	return Diagnostic{
		Path:               path,
		Exists:             true,
		Mode:               mode,
		CookieCount:        len(records),
		CookieNames:        names,
		ExpiredCookieNames: expired,
	}
}

func Save(records []cookies.Record) error {
	fresh := make([]cookies.Record, 0, len(records))
	for _, r := range records {
		if isPersisted(r) {
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
	return os.WriteFile(SessionPath(), data, 0o600)
}

func Load() ([]cookies.Record, error) {
	records, err := loadFileRaw()
	if err != nil {
		return nil, err
	}
	out := records[:0]
	for _, r := range records {
		if isPersisted(r) {
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
		return nil, fmt.Errorf("could not read session file %s: %v", SessionPath(), err)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("session file %s is corrupt (%v). Delete it and run 'schooltools login' again.", SessionPath(), err)
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

func Clear() error {
	err := os.Remove(SessionPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func HasValidSession(records []cookies.Record) bool {
	if len(records) == 0 {
		return false
	}
	now := time.Now()
	for _, r := range records {
		if !sessionCookieNames.MatchString(r.Name) {
			continue
		}
		if !endsWithCBEDomain(r.Domain) {
			continue
		}
		if r.IsExpired(now) {
			continue
		}
		return true
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

// LoadJar constructs a fresh cookiejar from the persisted records and returns
// it. Mirrors the TS `loadJar()` used by every authenticated command.
func LoadJar() (*cookiejar.Jar, error) {
	jar, err := httpclient.NewJar()
	if err != nil {
		return nil, err
	}
	records, err := Load()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if isExpired(r) {
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