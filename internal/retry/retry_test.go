package retry_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IamCoder18/schooltools/internal/retry"
	"github.com/stretchr/testify/assert"
)

func TestIsRetryableCode(t *testing.T) {
	for _, code := range []string{"ECONNRESET", "ETIMEDOUT", "EAI_AGAIN", "ENETUNREACH", "EPIPE"} {
		assert.True(t, retry.IsRetryableCode(code), code)
	}
	assert.False(t, retry.IsRetryableCode("ENOENT"))
}

func TestIsRetryableByMessage(t *testing.T) {
	for _, m := range []string{"fetch failed", "socket hang up", "network is unreachable"} {
		assert.True(t, retry.IsRetryable(errors.New(m)), m)
	}
	assert.False(t, retry.IsRetryable(errors.New("validation error")))
	assert.False(t, retry.IsRetryable(nil))
}

func TestWithRetrySucceedsImmediately(t *testing.T) {
	calls := 0
	out, err := retry.WithRetry(func() (string, error) {
		calls++
		return "ok", nil
	}, retry.Options{}, nil)
	assert.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Equal(t, 1, calls)
}

func TestWithRetryEventuallySucceeds(t *testing.T) {
	calls := 0
	out, err := retry.WithRetry(func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("fetch failed")
		}
		return "ok", nil
	}, retry.Options{Attempts: 5, BaseDelayMs: 1}, nil)
	assert.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Equal(t, 3, calls)
}

func TestWithRetryRespectsShouldRetry(t *testing.T) {
	calls := 0
	_, err := retry.WithRetry(func() (string, error) {
		calls++
		return "", errors.New("nope")
	}, retry.Options{Attempts: 5, BaseDelayMs: 1}, func(error) bool { return false })
	assert.Error(t, err)
	assert.Equal(t, 1, calls)
}

func TestWithRetryExhaustsAttempts(t *testing.T) {
	calls := 0
	_, err := retry.WithRetry(func() (string, error) {
		calls++
		return "", errors.New("fetch failed")
	}, retry.Options{Attempts: 3, BaseDelayMs: 1}, retry.IsRetryable)
	assert.Error(t, err)
	assert.Equal(t, 3, calls)
	// Ensure it doesn't sleep too long for the test.
	_ = time.Millisecond
}

func TestDelayForRaisesTheFloor(t *testing.T) {
	calls := 0
	start := time.Now()
	out, err := retry.WithRetry(func() (string, error) {
		calls++
		if calls < 2 {
			return "", errors.New("fetch failed")
		}
		return "ok", nil
	}, retry.Options{
		Attempts:    2,
		BaseDelayMs: 1,
		DelayFor:    func(int, error) time.Duration { return 300 * time.Millisecond },
	}, retry.IsRetryable)
	assert.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Equal(t, 2, calls)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
}

func TestDelayForCannotShortenBackoff(t *testing.T) {
	calls := 0
	start := time.Now()
	_, err := retry.WithRetry(func() (string, error) {
		calls++
		return "", errors.New("fetch failed")
	}, retry.Options{
		Attempts:    2,
		BaseDelayMs: 300,
		DelayFor:    func(int, error) time.Duration { return time.Nanosecond },
	}, retry.IsRetryable)
	assert.Error(t, err)
	assert.Equal(t, 2, calls)
	// Substituting the 1ns hint for the backoff would finish in ~0s.
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
}

func TestDelayForSurvivesDefaultAttempts(t *testing.T) {
	// Attempts is left at its zero value, so the defaults are substituted
	// field by field. A whole-struct substitution would drop DelayFor here.
	var gotHint bool
	_, err := retry.WithRetry(func() (string, error) {
		return "", errors.New("fetch failed")
	}, retry.Options{
		BaseDelayMs: 1,
		DelayFor: func(int, error) time.Duration {
			gotHint = true
			return time.Nanosecond
		},
	}, retry.IsRetryable)
	assert.Error(t, err)
	assert.True(t, gotHint, "DelayFor was discarded when Attempts was defaulted")
}