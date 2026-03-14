package lolzteam

import (
	"context"
	"sync"
	"time"
)

type rateLimiter struct {
	mu               sync.Mutex
	tokens           float64
	maxTokens        float64
	refillRate       float64 // tokens per second
	lastRefillTime   time.Time
}

func newRateLimiter(requestsPerMinute int) *rateLimiter {
	max := float64(requestsPerMinute)
	return &rateLimiter{
		tokens:         max,
		maxTokens:      max,
		refillRate:     max / 60.0,
		lastRefillTime: time.Now(),
	}
}

func (r *rateLimiter) acquire(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(r.lastRefillTime).Seconds()
		r.tokens += elapsed * r.refillRate
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}
		r.lastRefillTime = now

		if r.tokens >= 1 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}

		// Calculate wait time until one token is available
		deficit := 1.0 - r.tokens
		waitDuration := time.Duration(deficit / r.refillRate * float64(time.Second))
		r.mu.Unlock()

		timer := time.NewTimer(waitDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Loop back to try acquiring
		}
	}
}
