package limiter

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
)

// CallWithRetry calls fn with proactive rate limiting and retry on retryable
// errors (e.g. HTTP 429 rate limit responses).
//
// The rate limiter throttles outbound requests to stay within Slack's tier
// limits. If fn returns an error, retryAfter is called to determine whether
// the error is retryable; it should return a positive duration to retry after,
// or 0 (or negative) to indicate a non-retryable error.
//
// This keeps the limiter package free of slack-go dependencies — the caller
// provides the retry classification logic via the retryAfter callback.
//
// Example usage with slack-go:
//
//	rl := limiter.Tier3.Limiter()
//	result, err := limiter.CallWithRetry(ctx, rl, 2,
//	    func(err error) time.Duration {
//	        var rle *slack.RateLimitedError
//	        if errors.As(err, &rle) { return rle.RetryAfter }
//	        return 0
//	    },
//	    func() (*slack.Channel, error) {
//	        return client.GetConversationInfoContext(ctx, &input)
//	    },
//	)
func CallWithRetry[T any](
	ctx context.Context,
	rl *rate.Limiter,
	maxRetries int,
	retryAfter func(error) time.Duration,
	fn func() (T, error),
) (T, error) {
	var result T
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Proactive rate limiting — wait for a token before calling the API.
		if waitErr := rl.Wait(ctx); waitErr != nil {
			return result, fmt.Errorf("rate limiter context cancelled: %w", waitErr)
		}

		result, err = fn()
		if err == nil {
			return result, nil
		}

		// Check if this is a retryable error.
		backoff := retryAfter(err)
		if backoff <= 0 {
			// Non-retryable error — return immediately.
			return result, err
		}

		if attempt == maxRetries {
			// Exhausted retries — return the error as-is.
			return result, err
		}

		// Sleep for the backoff duration, then retry.
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return result, err
}
