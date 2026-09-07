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
		sleep(backoff(i, opts.BaseDelayMs))
	}
	return zero, last
}