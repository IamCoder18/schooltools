package authstats_test

import (
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/authstats"
)

const sample = `{"ts":"2026-09-04T03:14:34.000Z","event":"saml.captured"}
{"ts":"2026-09-04T03:22:42.000Z","event":"login.success"}
{"ts":"2026-09-04T03:26:05.000Z","event":"login.start"}
{"ts":"2026-09-04T03:26:07.000Z","event":"login.failure","code":"LOGIN_ERROR","message":"boom"}
{"ts":"2026-09-04T04:12:11.000Z","event":"login.success"}
{"ts":"2026-09-04T21:48:54.000Z","event":"login.success"}
{"ts":"2026-09-04T21:48:54.000Z","event":"session.invalid","reason":"missing"}
{"ts":"2026-09-04T22:21:52.000Z","event":"login.success"}
{"ts":"2026-09-04T22:22:00.000Z","event":"session.heartbeat.ok","latency_ms":654}`

func TestParseAndSummarise(t *testing.T) {
	events, err := authstats.ParseAuthLog(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 {
		t.Fatalf("want 9 events, got %d", len(events))
	}
	s := authstats.Summarise(events)
	if s.TotalEvents != 9 {
		t.Errorf("TotalEvents = %d", s.TotalEvents)
	}
	if s.Counts["login.success"] != 4 {
		t.Errorf("login.success count = %d", s.Counts["login.success"])
	}
	if len(s.LastFailures) != 2 {
		t.Errorf("LastFailures = %d, want 2 (login.failure + session.invalid)", len(s.LastFailures))
	}
}

func TestTTL(t *testing.T) {
	events, err := authstats.ParseAuthLog(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	ttl := authstats.TTL(events)
	if ttl.Samples != 3 {
		t.Errorf("Samples = %d, want 3", ttl.Samples)
	}
	// Gaps: 03:22→04:12 ≈ 50min, 04:12→21:48 ≈ 17h36m, 21:48→22:21 ≈ 33min
	if ttl.Min > 35*time.Minute {
		t.Errorf("Min = %s, want ~33m", ttl.Min)
	}
	if ttl.Max < 17*time.Hour {
		t.Errorf("Max = %s, want ~17h", ttl.Max)
	}
	// The most recent gap is 22:21→21:48 ≈ -33min? No — gap is positive:
	// 22:21 - 21:48 = 33m. Latest gap should be the smallest.
	if ttl.LatestGap > 45*time.Minute {
		t.Errorf("LatestGap = %s", ttl.LatestGap)
	}
}
