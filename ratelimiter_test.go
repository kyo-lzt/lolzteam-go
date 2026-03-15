package lolzteam

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiterRefillsOverTime(t *testing.T) {
	rl := newRateLimiter(60) // 1 per sec

	// Drain all tokens
	for range 60 {
		if err := rl.acquire(context.Background()); err != nil {
			t.Fatalf("acquire error: %v", err)
		}
	}

	// Wait for ~1 token to refill
	time.Sleep(1100 * time.Millisecond)

	start := time.Now()
	if err := rl.acquire(context.Background()); err != nil {
		t.Fatalf("acquire error: %v", err)
	}
	elapsed := time.Since(start)

	// Should be near-instant since token refilled during sleep
	if elapsed > 100*time.Millisecond {
		t.Errorf("expected near-instant acquire after refill, took %v", elapsed)
	}
}

func TestRateLimiterConcurrentAcquires(t *testing.T) {
	rl := newRateLimiter(600) // 10 per sec

	// Drain most tokens, leaving some headroom
	for range 595 {
		if err := rl.acquire(context.Background()); err != nil {
			t.Fatalf("acquire error: %v", err)
		}
	}

	// Launch concurrent acquires — they should all complete (some may wait)
	const n = 5
	var wg sync.WaitGroup
	var completed int32

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rl.acquire(ctx); err != nil {
				return
			}
			atomic.AddInt32(&completed, 1)
		}()
	}

	wg.Wait()
	if got := atomic.LoadInt32(&completed); got != n {
		t.Errorf("completed = %d, want %d", got, n)
	}
}

func TestRateLimiterTokensCappedAtMax(t *testing.T) {
	rl := newRateLimiter(60) // max = 60 tokens

	// Wait a bit — tokens should not exceed max
	time.Sleep(100 * time.Millisecond)

	rl.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(rl.lastRefillTime).Seconds()
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}
	tokens := rl.tokens
	rl.mu.Unlock()

	if tokens > 60 {
		t.Errorf("tokens = %v, should not exceed maxTokens (60)", tokens)
	}
}

func TestRateLimiterTimeoutDuringWait(t *testing.T) {
	rl := newRateLimiter(1) // 1 per minute

	// Drain the single token
	if err := rl.acquire(context.Background()); err != nil {
		t.Fatalf("acquire error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := rl.acquire(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timed-out context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("should have returned quickly after timeout, took %v", elapsed)
	}
}

func TestNewRateLimiterInitialState(t *testing.T) {
	rl := newRateLimiter(120) // 2 per sec

	if rl.maxTokens != 120 {
		t.Errorf("maxTokens = %v, want 120", rl.maxTokens)
	}
	if rl.tokens != 120 {
		t.Errorf("initial tokens = %v, want 120 (starts full)", rl.tokens)
	}
	expectedRate := 120.0 / 60.0
	if rl.refillRate != expectedRate {
		t.Errorf("refillRate = %v, want %v", rl.refillRate, expectedRate)
	}
}
