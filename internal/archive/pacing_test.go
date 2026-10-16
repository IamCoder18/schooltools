package archive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/content"
)

// fakeClock stands in for time.Now/time.Sleep so pacer arithmetic can be
// asserted exactly instead of within a tolerance wall-clock.
type fakeClock struct {
	mu     sync.Mutex
	t      time.Time
	slept  []time.Duration
	sleptN int
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.sleptN++
	c.t = c.t.Add(d)
}

// advance simulates time passing without a sleep, i.e. a slow response.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fakeClock) totalSlept() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum time.Duration
	for _, d := range c.slept {
		sum += d
	}
	return sum
}

// fixedJitter ignores the floor and returns a constant, so target arithmetic
// is exact: floor + 25ms.
func fixedJitter(time.Duration) time.Duration { return 25 * time.Millisecond }

func TestPacerFirstRequestIsNotDelayed(t *testing.T) {
	c := newFakeClock()
	p := newPacer(50 * time.Millisecond)
	p.now, p.sleep, p.jitter = c.now, c.sleep, fixedJitter

	p.Wait()
	if c.sleptN != 0 {
		t.Errorf("first Wait slept %d times, want 0 (nothing precedes it)", c.sleptN)
	}
}

func TestPacerEnforcesFloorPlusJitter(t *testing.T) {
	c := newFakeClock()
	p := newPacer(50 * time.Millisecond)
	p.now, p.sleep, p.jitter = c.now, c.sleep, fixedJitter

	for i := 0; i < 5; i++ {
		p.Wait()
	}
	// Every gap after the first must be floor + jitter = 75ms.
	if want := 4 * 75 * time.Millisecond; c.totalSlept() != want {
		t.Errorf("total slept = %v, want %v", c.totalSlept(), want)
	}
}

func TestPacerSkipsSleepWhenResponseAlreadySlow(t *testing.T) {
	c := newFakeClock()
	p := newPacer(50 * time.Millisecond)
	p.now, p.sleep, p.jitter = c.now, c.sleep, fixedJitter

	p.Wait()
	c.advance(500 * time.Millisecond) // a slow request already exceeded the floor
	p.Wait()
	if c.sleptN != 0 {
		t.Errorf("pacer slept %d times despite a 500ms gap, want 0 (pacing must not add to a slow response)", c.sleptN)
	}
}

func TestPacerSleepsOnlyRemainingTime(t *testing.T) {
	c := newFakeClock()
	p := newPacer(50 * time.Millisecond)
	p.now, p.sleep, p.jitter = c.now, c.sleep, fixedJitter

	p.Wait()
	c.advance(60 * time.Millisecond) // target is 75ms, so 15ms is still owed
	p.Wait()
	if got := c.totalSlept(); got != 15*time.Millisecond {
		t.Errorf("slept = %v, want 15ms (only the shortfall, not a fresh 75ms)", got)
	}
}

func TestPacerJitterVariesGaps(t *testing.T) {
	c := newFakeClock()
	p := newPacer(20 * time.Millisecond)
	p.now, p.sleep, p.jitter = c.now, c.sleep, randomGap

	for i := 0; i < 60; i++ {
		p.Wait()
	}

	c.mu.Lock()
	gaps := append([]time.Duration(nil), c.slept...)
	c.mu.Unlock()

	distinct := map[time.Duration]struct{}{}
	var belowFloor int
	for _, g := range gaps {
		distinct[g] = struct{}{}
		if g < 20*time.Millisecond {
			belowFloor++
		}
	}
	if belowFloor != 0 {
		t.Errorf("%d/%d gaps fell under the 20ms floor", belowFloor, len(gaps))
	}
	if len(distinct) < 5 {
		t.Errorf("only %d distinct gap values across %d requests; jitter is not doing anything", len(distinct), len(gaps))
	}
}

func TestPacerRandomGapStaysInRange(t *testing.T) {
	floor := 30 * time.Millisecond
	for i := 0; i < 500; i++ {
		g := randomGap(floor)
		if g < 0 || g >= floor {
			t.Fatalf("randomGap(%v) = %v, want [0, %v)", floor, g, floor)
		}
	}
	if randomGap(0) != 0 {
		t.Errorf("randomGap(0) must be 0")
	}
}

func TestPacerZeroFloorNeverSleeps(t *testing.T) {
	c := newFakeClock()
	p := newPacer(0)
	p.now, p.sleep, p.jitter = c.now, c.sleep, randomGap

	for i := 0; i < 10; i++ {
		p.Wait()
	}
	if c.sleptN != 0 {
		t.Errorf("zero-floor pacer slept %d times, want 0", c.sleptN)
	}
}

// TestPacerNilAndNegativeAreSafe asserts nothing: a panic in a Go test is
// already a failure, so calling both guards is the whole test.
func TestPacerNilAndNegativeAreSafe(_ *testing.T) {
	var nilPacer *pacer
	nilPacer.Wait()
	newPacer(-time.Second).Wait()
}

func TestOptionsLimiterIntervalResolution(t *testing.T) {
	d := func(v time.Duration) *time.Duration { return &v }

	if got := (Options{}).limiter().floor; got != DefaultRequestInterval {
		t.Errorf("nil RequestInterval -> floor %v, want %v", got, DefaultRequestInterval)
	}
	if got := (Options{RequestInterval: d(0)}).limiter().floor; got != 0 {
		t.Errorf("explicit 0 -> floor %v, want 0 (pacing disabled)", got)
	}
	if got := (Options{RequestInterval: d(120 * time.Millisecond)}).limiter().floor; got != 120*time.Millisecond {
		t.Errorf("120ms -> floor %v, want 120ms", got)
	}
	if got := (Options{RequestInterval: d(-time.Second)}).limiter().floor; got != 0 {
		t.Errorf("negative -> floor %v, want 0", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("5"); got != 5*time.Second {
		t.Errorf("delta-seconds: got %v, want 5s", got)
	}
	if got := parseRetryAfter("0"); got != 0 {
		t.Errorf("zero seconds: got %v, want 0", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("empty: got %v, want 0", got)
	}
	if got := parseRetryAfter("   "); got != 0 {
		t.Errorf("whitespace: got %v, want 0", got)
	}
	if got := parseRetryAfter("not-a-date"); got != 0 {
		t.Errorf("garbage: got %v, want 0", got)
	}
	if got := parseRetryAfter("-3"); got != 0 {
		t.Errorf("negative: got %v, want 0", got)
	}
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 3*time.Second {
		t.Errorf("http-date: got %v, want roughly 3s", got)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("past http-date: got %v, want 0", got)
	}
}

func TestParseRetryAfterIsCapped(t *testing.T) {
	// An unbounded server hint must not be able to stall a sequential pass.
	for _, v := range []string{"61", "86400", "10000000000", "99999999999999999999"} {
		if got := parseRetryAfter(v); got != maxRetryAfter {
			t.Errorf("parseRetryAfter(%q) = %v, want the %v cap", v, got, maxRetryAfter)
		}
	}
	if got := parseRetryAfter("60"); got != maxRetryAfter {
		t.Errorf("parseRetryAfter(\"60\") = %v, want %v (at the cap, not over)", got, maxRetryAfter)
	}
	if got := parseRetryAfter("59"); got != 59*time.Second {
		t.Errorf("parseRetryAfter(\"59\") = %v, want 59s (under the cap, untouched)", got)
	}
	// A far-future HTTP-date must clamp rather than returning its full delta.
	far := time.Now().Add(72 * time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(far); got != maxRetryAfter {
		t.Errorf("parseRetryAfter(future date) = %v, want the %v cap", got, maxRetryAfter)
	}
	// A near-future date must survive intact.
	near := time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(near); got <= 0 || got > 5*time.Second {
		t.Errorf("parseRetryAfter(near date) = %v, want roughly 5s", got)
	}
}

func TestRetryableErrorClassification(t *testing.T) {
	se := func(status int) error { return &statusError{status: status, msg: "x"} }

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429 throttled", se(429), true},
		{"500 server error", se(500), true},
		{"503 unavailable", se(503), true},
		{"404 gone", se(404), false},
		{"400 bad request", se(400), false},
		{"401 unauthenticated", se(401), false},
		{"session expired", errSessionExpired, false},
		{"transport failure", &transportError{err: context.DeadlineExceeded}, false},
		{"plain error", context.Canceled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableError(tc.err); got != tc.want {
				t.Errorf("retryableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// throttleFixture serves a course whose single topic returns `failFirst`
// throttle responses before succeeding, and counts topic requests.
type throttleFixture struct {
	calls    int
	failWith int
	first    int
	header   string
}

func (tf *throttleFixture) wrap(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) > 0 && containsTopicPath(r.URL.Path) {
			tf.calls++
			if tf.calls <= tf.first {
				if tf.header != "" {
					w.Header().Set("Retry-After", tf.header)
				}
				w.WriteHeader(tf.failWith)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
}

func containsTopicPath(p string) bool {
	return strings.Contains(p, "/content/topics/")
}

func singleTopicFixture(t *testing.T, tf *throttleFixture) (*httptest.Server, string) {
	t.Helper()
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("5555"), Name: "Lab", Code: "LAB", IsActive: true}}
	urlStr := "/content/enforced/5555-T1/notes.pdf"
	f.tocs["5555"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 9500, Title: "Notes", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "HTML"},
	}}}}
	f.topics[9500] = map[string]any{"TopicId": 9500, "Title": "Notes", "TypeIdentifier": "HTML"}
	_ = urlStr

	srv := httptest.NewServer(tf.wrap(f.handler()))
	t.Cleanup(srv.Close)
	return srv, "5555"
}

func TestRunRetriesThrottledTopic(t *testing.T) {
	tf := &throttleFixture{failWith: http.StatusTooManyRequests, first: 2}
	srv, courseID := singleTopicFixture(t, tf)

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, CourseIDs: []string{courseID}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TopicsFetched != 1 {
		t.Errorf("TopicsFetched = %d, want 1 (a 429 must not lose the topic)", res.TopicsFetched)
	}
	if res.TopicsFailed != 0 {
		t.Errorf("TopicsFailed = %d, want 0", res.TopicsFailed)
	}
	if tf.calls != 3 {
		t.Errorf("topic requests = %d, want 3 (2 throttled + 1 success)", tf.calls)
	}
}

func TestRunRetriesServerErrorThenSucceeds(t *testing.T) {
	tf := &throttleFixture{failWith: http.StatusBadGateway, first: 1}
	srv, courseID := singleTopicFixture(t, tf)

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, CourseIDs: []string{courseID}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TopicsFetched != 1 || tf.calls != 2 {
		t.Errorf("fetched=%d calls=%d, want fetched=1 calls=2", res.TopicsFetched, tf.calls)
	}
}

func TestRunDoesNotRetryPermanentFailure(t *testing.T) {
	tf := &throttleFixture{failWith: http.StatusNotFound, first: 99}
	srv, courseID := singleTopicFixture(t, tf)

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, CourseIDs: []string{courseID}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TopicsFetched != 0 {
		t.Errorf("TopicsFetched = %d, want 0", res.TopicsFetched)
	}
	if res.TopicsFailed != 1 {
		t.Errorf("TopicsFailed = %d, want 1", res.TopicsFailed)
	}
	if tf.calls != 1 {
		t.Errorf("topic requests = %d, want exactly 1 (404 is permanent; retrying it is hammering)", tf.calls)
	}
}

func TestRunGivesUpAfterRetryBudget(t *testing.T) {
	tf := &throttleFixture{failWith: http.StatusServiceUnavailable, first: 99}
	srv, courseID := singleTopicFixture(t, tf)

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, CourseIDs: []string{courseID}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TopicsFailed != 1 {
		t.Errorf("TopicsFailed = %d, want 1", res.TopicsFailed)
	}
	if tf.calls != retryAttempts {
		t.Errorf("topic requests = %d, want %d (the retry budget, then give up)", tf.calls, retryAttempts)
	}
}

func TestRunHonoursRetryAfter(t *testing.T) {
	tf := &throttleFixture{failWith: http.StatusTooManyRequests, first: 1, header: "1"}
	srv, courseID := singleTopicFixture(t, tf)

	start := time.Now()
	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, CourseIDs: []string{courseID}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TopicsFetched != 1 {
		t.Errorf("TopicsFetched = %d, want 1", res.TopicsFetched)
	}
	if elapsed < time.Second {
		t.Errorf("elapsed = %v, want >= 1s (Retry-After: 1 must outrank the 250ms backoff)", elapsed)
	}
}

// TestRunPacesRequestsAcrossCourses pins the property that the pacer is
// pass-wide: the gap between the last topic of one course and the TOC of the
// next must be paced too. A per-course pacer would leave that boundary
// unpaced, so the assertion is on the minimum observed gap across every
// request rather than on total elapsed time.
func TestRunPacesRequestsAcrossCourses(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{
		{OrgUnitId: json.Number("1111"), Name: "A", Code: "A", IsActive: true},
		{OrgUnitId: json.Number("2222"), Name: "B", Code: "B", IsActive: true},
	}

	const topicsPerCourse = 6
	var received []time.Time
	var mu sync.Mutex
	mark := func() {
		mu.Lock()
		received = append(received, time.Now())
		mu.Unlock()
	}

	for course, base := range map[string]int{"1111": 7000, "2222": 8000} {
		var toc []content.TocTopic
		for i := 0; i < topicsPerCourse; i++ {
			id := base + i
			toc = append(toc, content.TocTopic{
				TopicId: id, Title: "t", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "HTML",
			})
			f.topics[id] = map[string]any{"TopicId": id, "Title": "t", "TypeIdentifier": "HTML"}
		}
		f.tocs[course] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: toc}}}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/content/") || strings.Contains(r.URL.Path, "/mycourses") {
			mark()
		}
		f.handler().ServeHTTP(w, r)
	}))
	defer srv.Close()

	interval := 20 * time.Millisecond
	res, err := Run(Options{
		Root:            t.TempDir(),
		Jar:             newJar(t),
		BaseURL:         srv.URL,
		RequestInterval: &interval,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CoursesScanned != 2 {
		t.Fatalf("CoursesScanned = %d, want 2", res.CoursesScanned)
	}
	if want := 2 * topicsPerCourse; res.TopicsFetched != want {
		t.Fatalf("TopicsFetched = %d, want %d", res.TopicsFetched, want)
	}

	mu.Lock()
	defer mu.Unlock()
	// 2 TOC requests + 12 topic requests + 1 course list.
	if len(received) != 2*topicsPerCourse+3 {
		t.Fatalf("recorded %d requests, want %d", len(received), 2*topicsPerCourse+3)
	}
	smallest := time.Duration(1<<62 - 1)
	for i := 1; i < len(received); i++ {
		if gap := received[i].Sub(received[i-1]); gap < smallest {
			smallest = gap
		}
	}
	// Slack absorbs measurement noise between two handler-side clock reads;
	// a per-course pacer reset would show up here as a ~0ms boundary gap.
	if floor := interval - 5*time.Millisecond; smallest < floor {
		t.Errorf("smallest gap between consecutive requests = %v, want >= %v (pacing must survive the course boundary)", smallest, floor)
	}
}

func TestRunWithoutPacingIsFast(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("1111"), Name: "C", Code: "C", IsActive: true}}

	const topics = 25
	var toc []content.TocTopic
	for i := 0; i < topics; i++ {
		id := 8000 + i
		toc = append(toc, content.TocTopic{
			TopicId: id, Title: "t", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "HTML",
		})
		f.topics[id] = map[string]any{"TopicId": id, "Title": "t", "TypeIdentifier": "HTML"}
	}
	f.tocs["1111"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: toc}}}

	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	zero := time.Duration(0)
	start := time.Now()
	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, RequestInterval: &zero})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TopicsFetched != topics {
		t.Fatalf("TopicsFetched = %d, want %d", res.TopicsFetched, topics)
	}
	// 25 topics unthrottled must not cost anywhere near the default floor.
	if budget := time.Duration(topics) * DefaultRequestInterval; elapsed >= budget {
		t.Errorf("elapsed = %v with pacing disabled, want < %v", elapsed, budget)
	}
}
