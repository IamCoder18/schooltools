// Package d2lver discovers the D2L Valence API versions supported by the
// tenant at session load, and caches them for the lifetime of the
// process. It lets every command talk to the highest version the tenant
// supports without each call site repeating the discovery logic.
//
// If discovery fails (offline, expired session, transient API error), the
// package falls back to the hardcoded defaults — same URLs as before,
// just possibly older. So a broken discovery path never breaks a CLI
// command; it just keeps us on the version the previous release used.
package d2lver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/ua"
)

// Versions holds the highest supported product version per D2L Valence
// product family. Zero value = "not discovered yet, fall back to defaults".
type Versions struct {
	LP string // /d2l/api/lp/<lp>/...
	LE string // /d2l/api/le/<le>/...
	BAS string // /d2l/api/bas/<bas>/...
	EP string // /d2l/api/eP/<ep>/... (ePortfolio, off for CBE today)

	// Source is "discovered" when populated by a live call or
	// "fallback" when the package could not reach any discovery endpoint
	// and used the hardcoded defaults.
	Source string

	// DiscoveredAt is the wall-clock time the latest live discovery ran.
	DiscoveredAt time.Time
}

// Default returns the hardcoded fallback versions. Used when discovery
// fails or hasn't been run yet.
func Default() Versions {
	return Versions{
		LP:         ua.LPAPIVersion,
		LE:         ua.LEAPIVersion,
		BAS:        ua.BASAPIVersion,
		EP:         ua.EPAPIVersion,
		Source:     "fallback",
		DiscoveredAt: time.Time{},
	}
}

// package-level cache; one discovery call per process lifetime.
//
// Session load happens once per command invocation, so this cache is in
// practice "one call per CLI invocation." A multi-call CLI would share
// the cache; that's fine because the supported-version list is stable
// for the life of a tenant login.
var (
	cacheMu sync.RWMutex
	cache   Versions
)

// Get returns the cached versions if discovery has already happened,
// otherwise it runs discovery now using jar. The first concurrent caller
// wins; others block until the result is cached.
//
// If discovery fails entirely, the returned Versions have Source =
// "fallback" and every value equal to Default(). Callers can inspect the
// Source field for telemetry or to print a warning.
//
// Passing jar=nil is supported for code paths that don't have a session
// (e.g. unit tests). In that case Get just returns Default() and never
// tries to discover — there's no jar to discover against.
func Get(jar *cookiejar.Jar) Versions {
	cacheMu.RLock()
	if cache.Source != "" {
		v := cache
		cacheMu.RUnlock()
		return v
	}
	cacheMu.RUnlock()

	// First, try to load a previously-persisted version triple. CBE's
	// discovery answers don't change within a session, so the file is
	// good until next login.
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache.Source != "" {
		return cache
	}
	if v, ok := Load(); ok {
		cache = v
		return cache
	}
	if jar == nil {
		return Default()
	}
	cache = discover(jar)
	// Persist so the next CLI invocation doesn't re-discover.
	_ = Store(cache)
	return cache
}

// Cached returns the previously-discovered versions without touching the
// network. Reads in-memory cache first, then the persisted file, then
// falls back to Default() without attempting discovery. Used by the
// `versions` debug command.
//
// The distinction from Get(): Cached never runs network discovery, even
// when the persisted file is missing. Discovery only happens when an
// authenticated jar is fed into Get().
func Cached() Versions {
	cacheMu.RLock()
	if cache.Source != "" {
		v := cache
		cacheMu.RUnlock()
		return v
	}
	cacheMu.RUnlock()

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache.Source != "" {
		return cache
	}
	if v, ok := Load(); ok {
		cache = v
		return cache
	}
	return Default()
}

// Reset clears the cache. Used by tests.
func Reset() {
	cacheMu.Lock()
	cache = Versions{}
	cacheMu.Unlock()
}

// discover hits the per-product /versions/ endpoints and picks the
// highest supported version from each. Per-product failures don't kill
// the others — each product independently falls back to its default.
func discover(jar *cookiejar.Jar) Versions {
	v := Default()
	v.Source = "fallback"
	v.DiscoveredAt = time.Now().UTC()

	// Try each product with a short timeout. If one is slow / failing,
	// we don't want it to block the others indefinitely.
	type slot struct {
		product, name string
		dst           *string
	}
	slots := []slot{
		{"lp", "Learning Platform", &v.LP},
		{"le", "Learning Environment", &v.LE},
		{"bas", "Awards / Badges", &v.BAS},
		{"ep", "ePortfolio", &v.EP},
	}
	for _, s := range slots {
		got, ok := discoverOne(jar, s.product)
		if !ok {
			continue
		}
		*s.dst = got
	}

	// If any field moved off the default, mark source as discovered.
	if v.LP != ua.LPAPIVersion || v.LE != ua.LEAPIVersion || v.BAS != ua.BASAPIVersion || v.EP != ua.EPAPIVersion {
		v.Source = "discovered"
	}
	return v
}

func discoverOne(jar *cookiejar.Jar, product string) (string, bool) {
	url := ua.D2LBase + "/d2l/api/" + product + "/versions/"
	res, err := httpclient.FollowRedirects(url, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return "", false
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return "", false
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		_ = res.Body.Close()
		return "", false
	}
	_ = res.Body.Close()

	// Two response shapes are common:
	//   - single product: {"ProductCode":"lp","LatestVersion":"1.63","SupportedVersions":[...]}
	//   - all products:   [{...},{...},...]
	// Try the single-object shape first; fall back to the array.
	var single struct {
		ProductCode        string   `json:"ProductCode"`
		LatestVersion      string   `json:"LatestVersion"`
		SupportedVersions  []string `json:"SupportedVersions"`
	}
	if err := json.Unmarshal(body, &single); err == nil && single.ProductCode == product {
		return highest(single.SupportedVersions, single.LatestVersion), true
	}
	var many []struct {
		ProductCode       string   `json:"ProductCode"`
		LatestVersion     string   `json:"LatestVersion"`
		SupportedVersions []string `json:"SupportedVersions"`
	}
	if err := json.Unmarshal(body, &many); err != nil {
		return "", false
	}
	for _, p := range many {
		if p.ProductCode == product {
			return highest(p.SupportedVersions, p.LatestVersion), true
		}
	}
	return "", false
}

// highest returns the numerically-latest version in the list, or latest
// as the tiebreaker when the list is empty / nil. The comparison is by
// (major, minor) integers so "1.47" < "1.63" < "2.5" sorts correctly.
func highest(supported []string, latest string) string {
	if len(supported) == 0 {
		return latest
	}
	all := append([]string(nil), supported...)
	sort.Slice(all, func(i, j int) bool {
		return compareVersion(all[i], all[j]) < 0
	})
	return all[len(all)-1]
}

// compareVersion orders two D2L-style version strings (e.g. "1.47", "1.63",
// "2.5"). Returns -1/0/+1 by (major, minor). Unknown shapes sort low.
func compareVersion(a, b string) int {
	am, an := splitVersion(a)
	bm, bn := splitVersion(b)
	if am != bm {
		if am < bm {
			return -1
		}
		return 1
	}
	if an < bn {
		return -1
	}
	if an > bn {
		return 1
	}
	return 0
}

func splitVersion(s string) (int, int) {
	parts := strings.SplitN(s, ".", 2)
	major, _ := strconv.Atoi(parts[0])
	var minor int
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor
}

// String renders the versions map for `--show-version` style debugging.
func (v Versions) String() string {
	return fmt.Sprintf("lp=%s le=%s bas=%s ep=%s (source=%s, at=%s)",
		v.LP, v.LE, v.BAS, v.EP, v.Source, v.DiscoveredAt.Format(time.RFC3339))
}

// (intentionally removed): session.EnsureSession calls d2lver.Get
// directly with the jar it just loaded. Keeping the call site in
// session (which knows how to load the jar) avoids the
// session ↔ d2lver import cycle that would otherwise require lifting
// LoadJar into a third package.

// Disk path: cached versions live next to session.json / token.json so
// they survive across CLI invocations and don't require re-discovery on
// every command. The on-disk file is best-effort; missing or corrupt
// files just fall through to re-discovery.
//
// path() lives here (rather than in session) to keep the import cycle
// one-way: session imports d2lver for Get(). If d2lver imported session
// for SessionPath, we'd get a cycle. We compute the path independently.
// If session's layout ever changes, this needs to change in lock-step
// (the two functions are intentionally simple to audit side-by-side).
func path() string {
	dir := configDir()
	return filepath.Join(dir, "d2lver.json")
}

// configDir mirrors session.configDirWithoutState — but since that
// helper isn't exported and we don't want to take a session dependency,
// we compute it ourselves. Both paths use $XDG_CONFIG_HOME (or
// $HOME/.config) as the root.
func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "schooltools")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".schooltools"
	}
	return filepath.Join(home, ".config", "schooltools")
}

// Load reads the persisted versions file. Returns (zero, false) when
// the file is missing or corrupt; the caller should then run
// discovery and call Store with the result.
func Load() (Versions, bool) {
	raw, err := os.ReadFile(path())
	if err != nil {
		return Versions{}, false
	}
	var v Versions
	if err := json.Unmarshal(raw, &v); err != nil {
		return Versions{}, false
	}
	if v.LP == "" || v.LE == "" || v.BAS == "" || v.EP == "" {
		return Versions{}, false
	}
	v.Source = "persisted"
	return v, true
}

// Store writes the versions to the persisted file atomically (temp +
// rename). Best-effort; failures are swallowed because we'd rather run
// discovery again next launch than crash.
func Store(v Versions) error {
	if v.Source == "persisted" || v.Source == "" {
		// Refuse to persist a meaningless value.
		v.Source = "discovered"
	}
	v.DiscoveredAt = time.Now().UTC()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	final := path()
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
