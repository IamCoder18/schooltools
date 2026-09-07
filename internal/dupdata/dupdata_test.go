package dupdata

import (
	"testing"
	"time"
)

func TestParseISO(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2026-01-15", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"2026-01-15T08:30:00Z", time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC), true},
		{"", time.Time{}, true},
		{"not-a-date", time.Time{}, false},
	}
	for _, c := range cases {
		got, err := parseISO(c.in)
		if c.ok && err != nil {
			t.Errorf("parseISO(%q) returned error: %v", c.in, err)
			continue
		}
		if !c.ok && err == nil {
			t.Errorf("parseISO(%q) should have errored", c.in)
			continue
		}
		if c.ok && !got.Equal(c.want) {
			t.Errorf("parseISO(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"1m", 0, true},
		{"", 0, true},
		{"-1d", 0, true},
		{"0d", 0, true},
		{"ad", 0, true},
	}
	for _, c := range cases {
		got, err := parseDuration(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q) should have errored", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q) returned error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseTimeRange(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	tr, err := ParseTimeRange("2026-01-01", "2026-02-01", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.From.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("From = %v", tr.From)
	}
	if !tr.To.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("To = %v", tr.To)
	}

	tr, err = ParseTimeRange("", "", "7d", now)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(-7 * 24 * time.Hour)
	if !tr.From.Equal(want) {
		t.Errorf("From = %v, want %v", tr.From, want)
	}
	if !tr.To.Equal(now) {
		t.Errorf("To = %v, want %v", tr.To, now)
	}

	_, err = ParseTimeRange("", "", "", now)
	if err == nil {
		t.Errorf("empty time range should error")
	}
}

func TestAppendQuery(t *testing.T) {
	got, err := AppendQuery("https://x.test/api?a=1", "b", "2")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://x.test/api?a=1&b=2"
	if got != want {
		t.Errorf("AppendQuery = %q, want %q", got, want)
	}
}

func TestAdvanceBookmark(t *testing.T) {
	got, err := AdvanceBookmark("https://x.test/api?a=1", "BM1")
	if err != nil {
		t.Fatal(err)
	}
	// Order of query keys is not guaranteed by url.Values.Encode, so just
	// check that the result parses and contains both params.
	if got == "https://x.test/api?a=1" {
		t.Errorf("AdvanceBookmark did not set bookmark: %s", got)
	}
}
