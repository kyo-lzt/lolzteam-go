package lolzteam

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

func withRetry(ctx context.Context, cfg retryConfig, fn func() error) error {
	var lastErr error

	for attempt := range cfg.maxRetries {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !isRetryable(lastErr) {
			return lastErr
		}

		// Last attempt — don't sleep, just return
		if attempt == cfg.maxRetries-1 {
			break
		}

		delay := calcDelay(lastErr, attempt, cfg)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return lastErr
}

func isRetryable(err error) bool {
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return true
	}

	var serverErr *ServerError
	if errors.As(err, &serverErr) {
		return true
	}

	var networkErr *NetworkError
	if errors.As(err, &networkErr) {
		return true
	}

	return false
}

func calcDelay(err error, attempt int, cfg retryConfig) time.Duration {
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) && rateLimitErr.RetryAfter > 0 {
		d := rateLimitErr.RetryAfter
		if d > cfg.maxDelay {
			d = cfg.maxDelay
		}
		return d
	}

	// Exponential backoff: baseDelay * 2^attempt
	delay := cfg.baseDelay
	for range attempt {
		delay *= 2
	}
	if delay > cfg.maxDelay {
		delay = cfg.maxDelay
	}

	// Add jitter up to 25%
	jitterBase := int64(delay) / 4
	if jitterBase > 0 {
		delay += time.Duration(rand.Int64N(jitterBase))
	}

	return delay
}
