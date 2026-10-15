package systemd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitTimerFieldsBothDatetimes(t *testing.T) {
	f := []string{
		"Wed", "2026-10-14", "20:30:55", "MDT", "29min",
		"Wed", "2026-10-14", "20:00:55", "MDT", "23s", "ago",
		"schooltools-archive.timer", "schooltools-archive.service",
	}
	next, left, last, passed, unit, activates, ok := splitTimerFields(f)
	if !ok {
		t.Fatal("expected ok for a complete data row")
	}
	if next != "Wed 2026-10-14 20:30:55 MDT" {
		t.Errorf("next = %q", next)
	}
	if left != "29min" {
		t.Errorf("left = %q", left)
	}
	if last != "Wed 2026-10-14 20:00:55 MDT" {
		t.Errorf("last = %q", last)
	}
	if passed != "23s ago" {
		t.Errorf("passed = %q", passed)
	}
	if unit != "schooltools-archive.timer" || activates != "schooltools-archive.service" {
		t.Errorf("unit/activates = %q/%q", unit, activates)
	}
}

func TestSplitTimerFieldsNeverFired(t *testing.T) {
	f := []string{
		"Wed", "2026-10-14", "21:00:00", "MDT", "30min",
		"-", "-",
		"schooltools-archive.timer", "schooltools-archive.service",
	}
	next, left, last, passed, unit, activates, ok := splitTimerFields(f)
	if !ok {
		t.Fatal("expected ok for a never-fired row")
	}
	if next != "Wed 2026-10-14 21:00:00 MDT" {
		t.Errorf("next = %q, want the upcoming run", next)
	}
	if left != "30min" {
		t.Errorf("left = %q", left)
	}
	if last != "" || passed != "" {
		t.Errorf("last/passed = %q/%q, want empty", last, passed)
	}
	if unit != "schooltools-archive.timer" || activates != "schooltools-archive.service" {
		t.Errorf("unit/activates = %q/%q (placeholder LAST must not shift the tail)", unit, activates)
	}
}

func TestSplitTimerFieldsPlaceholderPassed(t *testing.T) {
	f := []string{
		"Wed", "2026-10-14", "20:30:55", "MDT", "29min",
		"Wed", "2026-10-14", "20:00:55", "MDT", "-",
		"schooltools-archive.timer", "schooltools-archive.service",
	}
	next, left, last, passed, unit, activates, ok := splitTimerFields(f)
	if !ok {
		t.Fatal("expected ok")
	}
	if next != "Wed 2026-10-14 20:30:55 MDT" || left != "29min" {
		t.Errorf("next/left = %q/%q", next, left)
	}
	if last != "Wed 2026-10-14 20:00:55 MDT" {
		t.Errorf("last = %q", last)
	}
	if passed != "" {
		t.Errorf("passed = %q, want empty for a placeholder", passed)
	}
	if unit != "schooltools-archive.timer" || activates != "schooltools-archive.service" {
		t.Errorf("unit/activates = %q/%q, want the real unit names", unit, activates)
	}
}

func TestSplitTimerFieldsMultiTokenPassed(t *testing.T) {
	f := []string{
		"Thu", "2026-10-15", "15:31:45", "MDT", "19h",
		"Wed", "2026-10-14", "15:31:45", "MDT", "4h", "29min", "ago",
		"launchpadlib-cache-clean.timer", "launchpadlib-cache-clean.service",
	}
	_, _, _, passed, unit, activates, ok := splitTimerFields(f)
	if !ok {
		t.Fatal("expected ok")
	}
	if passed != "4h 29min ago" {
		t.Errorf("passed = %q, want %q", passed, "4h 29min ago")
	}
	if unit != "launchpadlib-cache-clean.timer" || activates != "launchpadlib-cache-clean.service" {
		t.Errorf("unit/activates = %q/%q", unit, activates)
	}
}

func TestSplitTimerFieldsRejectsGarbage(t *testing.T) {
	for _, f := range [][]string{
		{},
		{"NEXT", "LEFT", "LAST", "PASSED", "UNIT", "ACTIVATES"},
		{"-", "-", "schooltools-archive.timer", "schooltools-archive.service"},
		{"1", "timers", "listed."},
	} {
		if _, _, _, _, _, _, ok := splitTimerFields(f); ok {
			t.Errorf("splitTimerFields(%v) = ok, want not-ok", f)
		}
	}
}

func TestIsDayOfWeek(t *testing.T) {
	for _, s := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		if !isDayOfWeek(s) {
			t.Errorf("isDayOfWeek(%q) = false", s)
		}
	}
	for _, s := range []string{"", "NEXT", "n/a", "Wed.", "1"} {
		if isDayOfWeek(s) {
			t.Errorf("isDayOfWeek(%q) = true", s)
		}
	}
}

func TestLegacyExecStart(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"current unit", "[Service]\nExecStart=/usr/bin/schooltools archive update --no-auto-refresh\n", false},
		{"bare archive", "[Service]\nExecStart=/usr/bin/schooltools archive\n", true},
		{"no exec start", "[Unit]\nDescription=schooltools archive\n", true},
		{"prune exec start", "[Service]\nExecStart=/usr/bin/schooltools archive prune --delete\n", true},
		{"description mention only", "[Unit]\nDescription=runs archive update\n[Service]\nExecStart=/usr/bin/schooltools archive\n", true},
		{"commented mention", "# ExecStart=/usr/bin/schooltools archive update\n[Service]\nExecStart=/usr/bin/schooltools archive\n", true},
	}
	for _, tc := range cases {
		if got := legacyExecStart(write(tc.name, tc.content)); got != tc.want {
			t.Errorf("legacyExecStart(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	if legacyExecStart(filepath.Join(dir, "does-not-exist")) {
		t.Error("a missing unit file should not be reported as legacy")
	}
}
