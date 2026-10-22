package archive

import (
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IamCoder18/schooltools/internal/content"
	"github.com/IamCoder18/schooltools/internal/retry"
)

// DefaultRequestInterval is the floor on the wall-clock gap between two
// consecutive outbound D2L requests.
//
// Archive is strictly sequential, so the natural request rate is already
// bounded by round-trip latency — on a fast link the only thing separating
// two requests is how quickly the previous one came back. The floor keeps
// that from becoming the limit, and the jitter is what keeps the resulting
// traffic from landing in an evenly-spaced, machine-looking pattern.
//
// The default is deliberately liberal: real gaps land in
// [DefaultRequestInterval, 2*DefaultRequestInterval), and because the pacer
// measures from the previous request's start, a slow response already
// satisfies the floor on its own and costs no extra wall-clock at all.
// Users who want the old behaviour exactly can pass 0.
const DefaultRequestInterval = 50 * time.Millisecond

// retryBaseDelayMs seeds the exponential backoff used for transient
// failures. retry.WithRetry owns the doubling and the jitter; this only
// sets the starting rung.
const retryBaseDelayMs = 250

// retryAttempts is deliberately small. Archive is a batch job the user can
// simply re-run, and the index is append-only, so a failed attempt is cheap
// to redo — there is no reason to sit in a long backoff loop.
const retryAttempts = 3

// pacer spaces out consecutive requests. It is safe for concurrent use,
// though the archive package does not fetch concurrently today.
type pacer struct {
	mu    sync.Mutex
	floor time.Duration
	last  time.Time

	// Seams so tests can assert on the arithmetic without sleeping.
	now    func() time.Time
	sleep  func(time.Duration)
	jitter func(time.Duration) time.Duration
}

func newPacer(floor time.Duration) *pacer {
	return &pacer{
		floor:  floor,
		now:    time.Now,
		sleep:  time.Sleep,
		jitter: randomGap,
	}
}

// Wait blocks until at least one randomised floor has elapsed since the
// previous call, then records this request as in flight. A nil pacer or a
// non-positive floor makes this a no-op.
func (p *pacer) Wait() {
	if p == nil || p.floor <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	target := p.floor + p.jitter(p.floor)
	if !p.last.IsZero() {
		if remaining := target - p.now().Sub(p.last); remaining > 0 {
			p.sleep(remaining)
		}
	}
	p.last = p.now()
}

// randomGap draws a uniform duration in [0, floor). Drawing the extra gap
// fresh on every call is the whole point: a fixed floor on its own just
// trades an unthrottled stream for an evenly-spaced one, which is easier to
// fingerprint, not harder.
func randomGap(floor time.Duration) time.Duration {
	if floor <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(floor)))
}

// statusError carries the HTTP status and any server-supplied retry hint so
// the retry predicate can tell a throttle from a permanent failure without
// matching on message text. msg is preserved verbatim so existing error
// output is unchanged.
type statusError struct {
	status     int
	retryAfter time.Duration
	msg        string
}

func (e *statusError) Error() string { return e.msg }

func newStatusError(res *http.Response, msg string) *statusError {
	return &statusError{
		status:     res.StatusCode,
		retryAfter: parseRetryAfter(res.Header.Get("Retry-After")),
		msg:        msg,
	}
}

// maxRetryAfter bounds how far a server-supplied Retry-After can stall a
// pass. Archive is strictly sequential, so honoring an unbounded hint —
// "Retry-After: 86400", or an HTTP-date far in the future — would hang the
// whole run with no progress output. Past the cap we retry on the normal
// backoff instead, which is the lesser evil. Clamping before converting to
// time.Duration also stops time.Duration(secs) * time.Second overflowing
// int64 and wrapping to a negative value the helper would then ignore.
const maxRetryAfter = 60 * time.Second

// parseRetryAfter reads a Retry-After header in either the delta-seconds or
// HTTP-date form. An unparseable, absent, or over-cap value yields something
// that leaves the computed backoff in charge.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs >= 0 {
			if secs > int(maxRetryAfter/time.Second) {
				return maxRetryAfter
			}
			return time.Duration(secs) * time.Second
		}
	} else if errors.Is(err, strconv.ErrRange) && allDigits(v) {
		// Digits that overflow an int64. The server asked for a wait far
		// longer than we will ever honor, so clamp rather than fall through
		// to the backoff and retry a throttle immediately.
		return maxRetryAfter
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, maxRetryAfter)
		}
	}
	return 0
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// retryableError decides whether a failed archive request is worth another
// attempt.
//
// Only server-level throttling and server faults qualify. Transport errors
// are deliberately excluded: during an archive pass a transport failure
// almost always means the network is gone, and retrying it per-topic across
// a whole course is the exact behaviour the existing consecutive-transport-
// failure guard exists to stop. That guard aborts the pass promptly and is
// left in charge here.
//
// An expired session is excluded too — retrying it only burns attempts, and
// Run already treats it as an abort-the-pass signal. 404 is excluded for the
// same reason: the topic is gone, not flaky.
func retryableError(err error) bool {
	if err == nil || errors.Is(err, errSessionExpired) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.status == http.StatusTooManyRequests || se.status >= 500
	}
	return false
}

// paced runs fn under the pass's pacer and retry policy. fn must release any
// response body it opens before returning, since a retry re-enters it.
//
// Retries are independent of the request interval: turning pacing off with
// --request-interval=0 still backs off on a transient failure, because
// retrying a broken connection faster is never the right call.
func (o Options) paced(p *pacer, fn func() error) error {
	_, err := retry.WithRetry(func() (struct{}, error) {
		p.Wait()
		return struct{}{}, fn()
	}, retry.Options{
		Attempts:    retryAttempts,
		BaseDelayMs: retryBaseDelayMs,
		DelayFor: func(_ int, err error) time.Duration {
			var se *statusError
			if errors.As(err, &se) {
				return se.retryAfter
			}
			return 0
		},
	}, retryableError)
	return err
}

// limiter returns the pass-wide pacer, creating it from Options on first use
// so Run and Diff can share one rate limit across every course rather than
// resetting it per course. Nil RequestInterval takes the default; an
// explicit 0 disables pacing.
func (o Options) limiter() *pacer {
	if o.pacer != nil {
		return o.pacer
	}
	floor := DefaultRequestInterval
	if o.RequestInterval != nil {
		switch {
		case *o.RequestInterval > 0:
			floor = *o.RequestInterval
		case *o.RequestInterval == 0:
			floor = 0
		}
		// A negative interval is a typo. The CLI rejects one outright;
		// for library callers, fall back to the default rather than
		// silently disabling the protection pacing exists to provide. Zero
		// stays the only way to turn pacing off.
	}
	return newPacer(floor)
}

// ---- paced wrappers over the individual fetch helpers ---------------------
//
// The underlying fetch helpers stay free of pacing and retry logic; this is
// the single place a request acquires a gap and a retry policy. Keeping it
// here is what stops a change to one call site from accidentally pacing a
// request twice.

func (o Options) fetchToc(p *pacer, orgUnitID string) (content.TocResponse, error) {
	var out content.TocResponse
	err := o.paced(p, func() error {
		var ferr error
		out, ferr = fetchTocFor(o.baseURL(), orgUnitID, o.Jar)
		return ferr
	})
	return out, err
}

func (o Options) fetchTopic(p *pacer, orgUnitID string, topicID int) ([]byte, error) {
	var out []byte
	err := o.paced(p, func() error {
		var ferr error
		out, ferr = fetchTopicFor(o.baseURL(), o.Jar, orgUnitID, topicID)
		return ferr
	})
	return out, err
}

func (o Options) fetchBody(p *pacer, rawURL string) ([]byte, string, error) {
	var body []byte
	var ct string
	err := o.paced(p, func() error {
		var ferr error
		body, ct, ferr = downloadFileBody(o.Jar, rawURL, o.baseURL())
		return ferr
	})
	return body, ct, err
}

func (o Options) fetchCoursesPage(p *pacer, rawURL string) (d2lCoursesResponse, error) {
	var out d2lCoursesResponse
	err := o.paced(p, func() error {
		var ferr error
		out, ferr = fetchCoursesPage(o.Jar, rawURL)
		return ferr
	})
	return out, err
}
