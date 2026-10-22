package render

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewListEnvelope(t *testing.T) {
	got := NewList([]string{"a", "b"})
	require.Equal(t, 2, got.Count)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{"count":2,"items":["a","b"]}`, string(raw))
}

func TestNewListNilSliceMarshalsAsEmptyArray(t *testing.T) {
	var items []string
	got := NewList(items)
	require.Equal(t, 0, got.Count)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{"count":0,"items":[]}`, string(raw))
}

func TestNewListObjectPayloadKeepsZeroCount(t *testing.T) {
	got := NewList(map[string]any{"Name": "x"})
	require.Equal(t, 0, got.Count)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{"count":0,"items":{"Name":"x"}}`, string(raw))
}

func TestRelAtPast(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		when time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5 minutes ago"},
		{now.Add(-2 * time.Hour), "2 hours ago"},
		{now.Add(-3 * 24 * time.Hour), "3 days ago"},
		{now.Add(-14 * 24 * time.Hour), "2 weeks ago"},
		{now.Add(-70 * 24 * time.Hour), "2 months ago"},
		{now.Add(-400 * 24 * time.Hour), "1 year ago"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, RelAt(c.when, now), c.when)
	}
}

func TestRelAtFuture(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	require.Equal(t, "in 3 days", RelAt(now.Add(3*24*time.Hour), now))
	require.Equal(t, "in 2 hours", RelAt(now.Add(2*time.Hour), now))
	require.Equal(t, "in 4 minutes", RelAt(now.Add(4*time.Minute), now))
}

func TestParseToleratesD2LShapes(t *testing.T) {
	for _, s := range []string{
		"2026-10-19T04:00:00.000Z",
		"2026-10-19T04:00:00Z",
		"2026-10-19T04:00:00+00:00",
		"2026-10-19T04:00:00",
		"2026-10-19",
	} {
		_, ok := Parse(s)
		require.True(t, ok, s)
	}
	for _, s := range []string{"", "null", "not-a-date"} {
		_, ok := Parse(s)
		require.False(t, ok, s)
	}
}

func TestWrapKeepsWordsIntact(t *testing.T) {
	got := Wrap("the quick brown fox jumps over the lazy dog", 12)
	require.Equal(t, "the quick\nbrown fox\njumps over\nthe lazy dog", got)
}

func TestWrapKeepsParagraphBreaks(t *testing.T) {
	got := Wrap("one two\n\nthree", 40)
	require.Equal(t, "one two\n\nthree", got)
}

func TestTruncate(t *testing.T) {
	require.Equal(t, "abc", Truncate("abc", 10))
	require.Equal(t, "ab…", Truncate("abcdef", 3))
	require.Equal(t, "a…", Truncate("abcdef", 2))
	require.Equal(t, "…", Truncate("abcdef", 1))
	require.Equal(t, "αβ…", Truncate("αβγδε", 3))
}

func TestCountAndPlural(t *testing.T) {
	require.Equal(t, "1 submission", Count(1, "submission"))
	require.Equal(t, "0 submissions", Count(0, "submission"))
	require.Equal(t, "3 submissions", Count(3, "submission"))
	require.Equal(t, "submission", Plural(1, "submission"))
	require.Equal(t, "submissions", Plural(0, "submission"))

	require.Equal(t, "2 quizzes", Count(2, "quiz"))
	require.Equal(t, "1 quiz", Count(1, "quiz"))
	require.Equal(t, "3 activities", Count(3, "activity"))
	require.Equal(t, "2 boxes", Count(2, "box"))
	require.Equal(t, "4 classes", Count(4, "class"))
	require.Equal(t, "5 matches", Count(5, "match"))
	require.Equal(t, "3 grades", Count(3, "grade"))
	require.Equal(t, "2 leaves", Count(2, "leaf"))
}

func TestDuration(t *testing.T) {
	require.Equal(t, "45s", Duration(45*time.Second))
	require.Equal(t, "12m", Duration(12*time.Minute))
	require.Equal(t, "2h", Duration(2*time.Hour))
	require.Equal(t, "2h 5m", Duration(2*time.Hour+5*time.Minute))
	require.Equal(t, "3d", Duration(72*time.Hour))
	require.Equal(t, "3d 2h", Duration(74*time.Hour))
}

func TestHumanBytes(t *testing.T) {
	require.Equal(t, "512 B", HumanBytes(512))
	require.Equal(t, "1.0 KiB", HumanBytes(1024))
	require.Equal(t, "1.5 KiB", HumanBytes(1536))
	require.Equal(t, "2.0 MiB", HumanBytes(2*1024*1024))
}

func TestKVAlignsKeys(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := KV(Row{Key: "Name", Value: "Bio"}, Row{Key: "Status", Value: "open"})
	require.Equal(t, "Name    Bio\nStatus  open\n", got)
}

func TestEmptyAndCountZero(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	require.Equal(t, "  nothing here\n", Empty("nothing here"))
}
