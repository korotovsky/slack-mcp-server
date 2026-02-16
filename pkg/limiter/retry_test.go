package limiter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// retryableError is a test error that simulates a rate limit response.
type retryableError struct {
	retryAfter time.Duration
}

func (e *retryableError) Error() string {
	return "rate limited"
}

// testRetryAfter is the retryAfter callback used in tests.
func testRetryAfter(err error) time.Duration {
	var re *retryableError
	if errors.As(err, &re) {
		return re.retryAfter
	}
	return 0
}

func TestCallWithRetry_Success(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx := context.Background()

	callCount := 0
	result, err := CallWithRetry(ctx, rl, 2, testRetryAfter, func() (string, error) {
		callCount++
		return "ok", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "ok", result)
	assert.Equal(t, 1, callCount)
}

func TestCallWithRetry_RetryOnRateLimit(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx := context.Background()

	callCount := 0
	result, err := CallWithRetry(ctx, rl, 2, testRetryAfter, func() (string, error) {
		callCount++
		if callCount <= 2 {
			return "", &retryableError{retryAfter: 10 * time.Millisecond}
		}
		return "ok", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "ok", result)
	assert.Equal(t, 3, callCount, "should count initial call + 2 retries")
}

func TestCallWithRetry_ExhaustedRetries(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx := context.Background()

	callCount := 0
	_, err := CallWithRetry(ctx, rl, 2, testRetryAfter, func() (string, error) {
		callCount++
		return "", &retryableError{retryAfter: 10 * time.Millisecond}
	})

	require.Error(t, err)
	var re *retryableError
	assert.ErrorAs(t, err, &re, "should return retryable error after exhausting retries")
	assert.Equal(t, 3, callCount, "should count initial call + 2 retries")
}

func TestCallWithRetry_NonRetryableError(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx := context.Background()

	callCount := 0
	_, err := CallWithRetry(ctx, rl, 2, testRetryAfter, func() (string, error) {
		callCount++
		return "", assert.AnError
	})

	require.Error(t, err)
	assert.Equal(t, assert.AnError, err, "should return non-retryable error immediately without retry")
	assert.Equal(t, 1, callCount)
}

func TestCallWithRetry_ContextCancelled(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	_, err := CallWithRetry(ctx, rl, 2, testRetryAfter, func() (string, error) {
		callCount++
		cancel()
		return "", &retryableError{retryAfter: 5 * time.Second}
	})

	require.Error(t, err)
	assert.Equal(t, 1, callCount, "should stop after context cancellation")
}

func TestCallWithRetry_RateLimiterThrottles(t *testing.T) {
	// Slow rate limiter: 1 request per 100ms
	rl := rate.NewLimiter(rate.Every(100*time.Millisecond), 1)
	ctx := context.Background()

	start := time.Now()
	callCount := 0
	result, err := CallWithRetry(ctx, rl, 0, testRetryAfter, func() (string, error) {
		callCount++
		return "ok", nil
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "ok", result)
	assert.Equal(t, 1, callCount)
	// The rate limiter should have let the first call through quickly (burst=1)
	assert.Less(t, elapsed, 50*time.Millisecond, "first call should use burst token")
}

func TestCallWithRetry_ZeroRetries(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx := context.Background()

	callCount := 0
	_, err := CallWithRetry(ctx, rl, 0, testRetryAfter, func() (string, error) {
		callCount++
		return "", &retryableError{retryAfter: 10 * time.Millisecond}
	})

	require.Error(t, err)
	var re *retryableError
	assert.ErrorAs(t, err, &re)
	assert.Equal(t, 1, callCount, "should make exactly 1 call with 0 retries")
}

func TestCallWithRetry_RetryThenSuccess(t *testing.T) {
	rl := rate.NewLimiter(rate.Inf, 1)
	ctx := context.Background()

	type result struct {
		ID string
	}

	callCount := 0
	start := time.Now()
	res, err := CallWithRetry(ctx, rl, 1, testRetryAfter, func() (*result, error) {
		callCount++
		if callCount == 1 {
			return nil, &retryableError{retryAfter: 50 * time.Millisecond}
		}
		return &result{ID: "C123"}, nil
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "C123", res.ID)
	assert.Equal(t, 2, callCount)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "should have slept for retryAfter duration")
}
