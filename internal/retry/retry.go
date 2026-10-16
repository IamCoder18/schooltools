package retry

import (
	"errors"
	"math/rand"
	"net"
	"strings"
	"time"
)

var RetryableNetworkCodes = map[string]struct{}{
	"ECONNRESET":   {},
	"ETIMEDOUT":    {},
	"EAI_AGAIN":    {},
	"ENETUNREACH":  {},
	"EPIPE":        {},
}

type Options struct {
	Attempts    int
	BaseDelayMs int
	// DelayFor lets an error raise the floor on the next wait, for cases
	// where the server states its own retry hint (an HTTP Retry-After, a
	// rate-limit Retry-After). It can only lengthen the computed backoff,
	// never shorten it, so a caller cannot accidentally hammer sooner than
	// the exponential schedule already allows. Returning 0 leaves the
	// backoff untouched. Nil disables the hook.
	DelayFor func(attempt int, err error) time.Duration
}

func DefaultOptions() Options { return Options{Attempts: 3, BaseDelayMs: 250} }

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		// netErr.Temporary() was deprecated in Go 1.18; rely on Timeout()
		// plus the string-based fall-through below for transient DNS / fetch errors.
		if netErr.Timeout() {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "fetch failed") ||
		strings.Contains(msg, "socket hang up") ||
		strings.Contains(msg, "network is unreachable") {
		return true
	}
	return false
}

// IsRetryableCode matches the legacy TS string-code matcher.
func IsRetryableCode(code string) bool {
	_, ok := RetryableNetworkCodes[code]
	return ok
}

func backoff(attempt, base int) time.Duration {
	jitter := rand.Intn(base + 1)
	d := float64(base)*pow2(attempt) + float64(jitter)
	return time.Duration(d) * time.Millisecond
}

func pow2(n int) float64 {
	if n >= 30 {
		return 1 << 30
	}
	v := 1.0
	for i := 0; i < n; i++ {
		v *= 2
	}
	return v
}

func sleep(d time.Duration) { time.Sleep(d) }

// WithRetry calls fn up to opts.Attempts times, sleeping with exponential backoff
// + jitter between attempts. shouldRetry decides whether a given error is worth
// retrying; the default is any error containing known transient markers.
//
// The jitter in backoff matters as much as the backoff itself: without it
// every caller that fails at the same moment retries in lockstep and
// re-creates the thundering herd the backoff was meant to break up.
func WithRetry[T any](fn func() (T, error), opts Options, shouldRetry func(error) bool) (T, error) {
	if opts.Attempts == 0 {
		opts = DefaultOptions()
	}
	if shouldRetry == nil {
		shouldRetry = IsRetryable
	}
	var zero T
	var last error
	for i := 0; i < opts.Attempts; i++ {
		out, err := fn()
		if err == nil {
			return out, nil
		}
		last = err
		if i == opts.Attempts-1 || !shouldRetry(err) {
			return zero, err
		}
		d := backoff(i, opts.BaseDelayMs)
		if opts.DelayFor != nil {
			if hint := opts.DelayFor(i, err); hint > d {
				d = hint
			}
		}
		sleep(d)
	}
	return zero, last
}