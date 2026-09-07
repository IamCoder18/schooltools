package retry_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/retry"
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