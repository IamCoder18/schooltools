// Package authstats summarises ~/.config/schooltools/auth.log for human
// inspection. It's intentionally read-only — nothing here writes to disk or
// touches D2L. The output drives `schooltools auth log` and
// `schooltools session ttl`.
package authstats

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/aarav/schooltools/internal/authlog"
)

// Event is a single decoded auth.log row. We keep the raw map alongside the
// parsed fields so callers can surface anything we haven't enumerated.
type Event struct {
	Time    time.Time
	Name    string
	Fields  map[string]any
	RawLine string
}

// Summary is the aggregated view used by `schooltools auth log`.
type Summary struct {
	Path          string
	GeneratedAt   time.Time
	TotalEvents   int
	Counts        map[string]int
	FirstEvent    time.Time
	LastEvent     time.Time
	OldestLogin   time.Time
	NewestLogin   time.Time
	LoginGaps     []time.Duration // gap between successive login.success events
	LastFailures  []FailureEntry  // most recent N login.failure + session.invalid events
}

// FailureEntry is a single failure event surfaced in the summary.
type FailureEntry struct {
	Time    time.Time
	Name    string
	Reason  string
	Code    string
	Message string
}

// TTLResult is the structured output of `schooltools session ttl`. It measures
// the time between successive successful logins, which is an upper bound on
// the LMS session's actual server-side lifetime — the session must have been
// alive at the start of each gap and dead before the next login succeeded.
type TTLResult struct {
	GeneratedAt      time.Time
	Samples          int           // number of login.success → login.success gaps
	Min              time.Duration // shortest observed gap (tightest estimate of "at least this long")
	Max              time.Duration
	Mean             time.Duration
	Median           time.Duration
	LatestGap        time.Duration // gap ending at the most recent login
	LoginTimestamps  []time.Time
	Gaps             []time.Duration
	HoursOfUptime    time.Duration // sum of all gaps (proxy for total logged-in time)
}

// Load reads and decodes every line of the auth log.
func Load() ([]Event, error) {
	path := authlog.Path()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no auth log at %s — set --no-auth-log=false and run a command", path)
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseAuthLog(f)
}

// ParseAuthLog is the testable inner of Load. Exposed so the authstats_test
// package can drive it with synthetic log content.
func ParseAuthLog(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	// Some log lines can be long (cookie lists). Bump the buffer.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []Event
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		ev := Event{Fields: raw, RawLine: string(line)}
		if s, ok := raw["ts"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				ev.Time = t
			} else if t, err := time.Parse(time.RFC3339, s); err == nil {
				ev.Time = t
			}
		}
		if s, ok := raw["event"].(string); ok {
			ev.Name = s
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

// Summarise produces a Summary from a parsed event list.
func Summarise(events []Event) Summary {
	s := Summary{
		Counts:    map[string]int{},
		GeneratedAt: time.Now(),
	}
	if len(events) == 0 {
		return s
	}
	s.Path = authlog.Path()
	s.TotalEvents = len(events)
	s.FirstEvent = events[0].Time
	s.LastEvent = events[len(events)-1].Time

	var loginTimes []time.Time
	var failures []FailureEntry
	for _, ev := range events {
		s.Counts[ev.Name]++
		if ev.Name == "login.success" {
			loginTimes = append(loginTimes, ev.Time)
		}
		if ev.Name == "login.failure" || ev.Name == "session.invalid" || ev.Name == "session.heartbeat.failed" {
			failures = append(failures, FailureEntry{
				Time:    ev.Time,
				Name:    ev.Name,
				Reason:  stringField(ev.Fields, "reason"),
				Code:    stringField(ev.Fields, "code"),
				Message: stringField(ev.Fields, "message"),
			})
		}
	}
	if len(loginTimes) > 0 {
		s.OldestLogin = loginTimes[0]
		s.NewestLogin = loginTimes[len(loginTimes)-1]
		for i := 1; i < len(loginTimes); i++ {
			s.LoginGaps = append(s.LoginGaps, loginTimes[i].Sub(loginTimes[i-1]))
		}
	}
	// Keep the last 5 failures.
	if len(failures) > 5 {
		failures = failures[len(failures)-5:]
	}
	s.LastFailures = failures
	return s
}

// TTL computes observed session lifetime from login.success events.
func TTL(events []Event) TTLResult {
	r := TTLResult{GeneratedAt: time.Now()}
	var loginTimes []time.Time
	for _, ev := range events {
		if ev.Name != "login.success" || ev.Time.IsZero() {
			continue
		}
		loginTimes = append(loginTimes, ev.Time)
	}
	r.LoginTimestamps = append([]time.Time(nil), loginTimes...)
	if len(loginTimes) < 2 {
		return r
	}
	sort.Slice(loginTimes, func(i, j int) bool { return loginTimes[i].Before(loginTimes[j]) })
	for i := 1; i < len(loginTimes); i++ {
		gap := loginTimes[i].Sub(loginTimes[i-1])
		r.Gaps = append(r.Gaps, gap)
		r.HoursOfUptime += gap
	}
	r.Samples = len(r.Gaps)
	r.Min = r.Gaps[0]
	r.Max = r.Gaps[0]
	var total time.Duration
	for _, g := range r.Gaps {
		total += g
		if g < r.Min {
			r.Min = g
		}
		if g > r.Max {
			r.Max = g
		}
	}
	r.Mean = total / time.Duration(len(r.Gaps))
	r.Median = medianDuration(r.Gaps)
	r.LatestGap = r.Gaps[len(r.Gaps)-1]
	return r
}

func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
