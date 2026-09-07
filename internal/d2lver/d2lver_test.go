package d2lver

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.47", "1.47", 0},
		{"1.47", "1.63", -1},
		{"1.63", "1.47", 1},
		{"1.97", "1.10", 1}, // 97 > 10 numerically (we don't compare string-wise)
		{"2.0", "1.99", 1},
		{"2.5", "2.5", 0},
		{"1.47", "2.0", -1},
		// Recovery from malformed strings: split on "." returns 0 for the
		// invalid part, so "1.x" sorts before "1.10".
		{"1.9", "1.10", -1},
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestHighest(t *testing.T) {
	cases := []struct {
		supported []string
		latest    string
		want      string
	}{
		{nil, "1.5", "1.5"},
		{[]string{}, "1.5", "1.5"},
		{[]string{"1.47"}, "1.47", "1.47"},
		{[]string{"1.47", "1.63", "1.5"}, "1.63", "1.63"},
		{[]string{"1.50", "1.49", "1.48"}, "1.50", "1.50"},
		{[]string{"1.97", "2.0"}, "2.0", "2.0"},
		// Major bumps dominate.
		{[]string{"1.99", "2.0", "1.0"}, "2.0", "2.0"},
	}
	for _, c := range cases {
		if got := highest(c.supported, c.latest); got != c.want {
			t.Errorf("highest(%v, %q) = %q, want %q", c.supported, c.latest, got, c.want)
		}
	}
}

func TestHighestSortsNumerically(t *testing.T) {
	// Strings of the supported list might be in any order. Make sure we
	// sort by (major, minor) integers, not lexicographically.
	in := []string{"1.47", "1.50", "1.5", "1.63", "1.97", "2.5"}
	want := "2.5"
	if got := highest(in, in[0]); got != want {
		t.Errorf("highest sorted wrongly: got %q, want %q", got, want)
	}
}

func TestSplitVersion(t *testing.T) {
	cases := []struct {
		in       string
		wantMaj  int
		wantMin  int
	}{
		{"1.47", 1, 47},
		{"2.0", 2, 0},
		{"10.4", 10, 4},
		{"1", 1, 0},
		{"", 0, 0},
		{"abc", 0, 0},
	}
	for _, c := range cases {
		maj, min := splitVersion(c.in)
		if maj != c.wantMaj || min != c.wantMin {
			t.Errorf("splitVersion(%q) = (%d, %d), want (%d, %d)", c.in, maj, min, c.wantMaj, c.wantMin)
		}
	}
}

func TestDefault(t *testing.T) {
	d := Default()
	if d.LP == "" || d.LE == "" || d.BAS == "" || d.EP == "" {
		t.Errorf("Default has empty fields: %+v", d)
	}
	if d.Source != "fallback" {
		t.Errorf("Default Source = %q, want %q", d.Source, "fallback")
	}
	if !d.DiscoveredAt.IsZero() {
		t.Errorf("Default DiscoveredAt = %v, want zero", d.DiscoveredAt)
	}
}

func TestVersionsStringIncludesSource(t *testing.T) {
	d := Default()
	s := d.String()
	for _, want := range []string{"lp=", "le=", "bas=", "ep=", "source=fallback"} {
		if !contains(s, want) {
			t.Errorf("String() missing %q in %q", want, s)
		}
	}
}

// TestResetClearsCache exercises the public reset path.
func TestResetClearsCache(t *testing.T) {
	Reset()
	if cache.Source != "" {
		t.Errorf("cache should be empty after Reset, got Source=%q", cache.Source)
	}
}

// TestCachedReturnsDefaultWhenEmpty makes sure Cached() never triggers
// discovery and never panics. Used by the `versions` debug command.
func TestCachedReturnsDefaultWhenEmpty(t *testing.T) {
	Reset()
	// Point $HOME at a fresh tempdir so there's no on-disk file left
	// over from a previous run that would change Cached's behavior.
	origHome := os.Getenv("HOME")
	origCfg := os.Getenv("XDG_CONFIG_HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer func() {
		t.Setenv("HOME", origHome)
		t.Setenv("XDG_CONFIG_HOME", origCfg)
	}()
	Reset() // re-check now that paths moved
	v := Cached()
	if v.Source != "fallback" {
		t.Errorf("Cached with empty cache + no disk file should be Default, got Source=%q", v.Source)
	}
	if v.LP == "" || v.LE == "" || v.BAS == "" || v.EP == "" {
		t.Errorf("Cached returned unexpected: %+v", v)
	}
}

// TestStoreLoadRoundTrip verifies the persisted file path: write a
// known Versions, read it back, confirm Source is "persisted" and the
// fields match. Uses a t.TempDir redirect to keep tests hermetic.
func TestStoreLoadRoundTrip(t *testing.T) {
	// Override configDir so we don't touch the real $XDG path.
	origHome := os.Getenv("HOME")
	origCfg := os.Getenv("XDG_CONFIG_HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer func() {
		t.Setenv("HOME", origHome)
		t.Setenv("XDG_CONFIG_HOME", origCfg)
	}()
	Reset()
	v := Versions{
		LP: "1.63", LE: "1.97", BAS: "1.6", EP: "2.5",
		Source: "discovered", DiscoveredAt: time.Now().UTC(),
	}
	if err := Store(v); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, ok := Load()
	if !ok {
		t.Fatal("Load returned !ok after Store")
	}
	if got.LP != v.LP || got.LE != v.LE || got.BAS != v.BAS || got.EP != v.EP {
		t.Errorf("Load() = %+v, want %+v", got, v)
	}
	if got.Source != "persisted" {
		t.Errorf("loaded Source = %q, want persisted", got.Source)
	}
}

// TestLoadReturnsFalseOnMissingFile makes sure a corrupt or missing
// file cleanly returns ("", false) so the caller falls back to
// discovery.
func TestLoadReturnsFalseOnMissingFile(t *testing.T) {
	origHome := os.Getenv("HOME")
	origCfg := os.Getenv("XDG_CONFIG_HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")
	defer func() {
		t.Setenv("HOME", origHome)
		t.Setenv("XDG_CONFIG_HOME", origCfg)
	}()
	Reset()
	if _, ok := Load(); ok {
		t.Errorf("Load() returned ok with no file")
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// avoid reflect unused-warning
var _ = reflect.DeepEqual
