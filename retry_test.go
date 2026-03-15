package lolzteam

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestIsTransientNetworkError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		transient bool
	}{
		{"nil", nil, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"ECONNRESET", syscall.ECONNRESET, true},
		{"timeout", &timeoutError{}, true},
		{"DNS error", &net.DNSError{Err: "no such host", Name: "example.com"}, false},
		{"ECONNREFUSED", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, false},
		{"unknown error", errors.New("something"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientNetworkError(tt.err)
			if got != tt.transient {
				t.Errorf("isTransientNetworkError = %v, want %v", got, tt.transient)
			}
		})
	}
}

// timeoutError implements net.Error with Timeout() == true.
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return false }

func TestWithRetryOnRetryCallback(t *testing.T) {
	var callbacks []RetryInfo
	cfg := retryConfig{
		maxRetries: 3,
		baseDelay:  time.Millisecond,
		maxDelay:   10 * time.Millisecond,
		onRetry: func(info RetryInfo) {
			callbacks = append(callbacks, info)
		},
	}

	calls := 0
	err := withRetry(context.Background(), cfg, "POST", "/api", func() error {
		calls++
		if calls < 3 {
			return &ServerError{HttpError: HttpError{StatusCode: 503}}
		}
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(callbacks) != 2 {
		t.Fatalf("expected 2 onRetry callbacks, got %d", len(callbacks))
	}

	for i, cb := range callbacks {
		if cb.Method != "POST" {
			t.Errorf("callback[%d].Method = %q, want POST", i, cb.Method)
		}
		if cb.Path != "/api" {
			t.Errorf("callback[%d].Path = %q, want /api", i, cb.Path)
		}
		if cb.Attempt != i {
			t.Errorf("callback[%d].Attempt = %d, want %d", i, cb.Attempt, i)
		}
		if cb.Delay <= 0 {
			t.Errorf("callback[%d].Delay = %v, want > 0", i, cb.Delay)
		}
		var srvErr *ServerError
		if !errors.As(cb.Err, &srvErr) {
			t.Errorf("callback[%d].Err type = %T, want *ServerError", i, cb.Err)
		}
	}
}

func TestWithRetryExhaustedWrapsLastError(t *testing.T) {
	err := withRetry(context.Background(), retryConfig{
		maxRetries: 2,
		baseDelay:  time.Millisecond,
		maxDelay:   5 * time.Millisecond,
	}, "GET", "/test", func() error {
		return &RateLimitError{HttpError: HttpError{StatusCode: 429}, RetryAfter: time.Millisecond}
	})

	var exhausted *RetryExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected *RetryExhaustedError, got %T", err)
	}
	if exhausted.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", exhausted.Attempts)
	}

	// Inner error should be the last RateLimitError
	var rlErr *RateLimitError
	if !errors.As(exhausted.Err, &rlErr) {
		t.Errorf("expected inner *RateLimitError, got %T", exhausted.Err)
	}

	// errors.As should walk through RetryExhaustedError
	var rlErr2 *RateLimitError
	if !errors.As(err, &rlErr2) {
		t.Error("errors.As should find *RateLimitError through RetryExhaustedError")
	}
}

func TestCalcDelayJitterIsNonDeterministic(t *testing.T) {
	cfg := retryConfig{
		maxRetries: 5,
		baseDelay:  100 * time.Millisecond,
		maxDelay:   10 * time.Second,
	}
	srvErr := &ServerError{HttpError: HttpError{StatusCode: 502}}

	// Run multiple times and check we get at least 2 distinct values (jitter).
	seen := make(map[time.Duration]struct{})
	for range 50 {
		d := calcDelay(srvErr, 0, cfg)
		seen[d] = struct{}{}
	}

	if len(seen) < 2 {
		t.Errorf("expected jitter to produce varying delays, got %d distinct values", len(seen))
	}
}

func TestCalcDelayRateLimitWithoutRetryAfterUsesBackoff(t *testing.T) {
	cfg := retryConfig{
		maxRetries: 3,
		baseDelay:  100 * time.Millisecond,
		maxDelay:   10 * time.Second,
	}
	rlErr := &RateLimitError{HttpError: HttpError{StatusCode: 429}, RetryAfter: 0}

	d := calcDelay(rlErr, 0, cfg)
	// Without RetryAfter, should use exponential backoff (base=100ms + jitter)
	if d < 100*time.Millisecond || d > 125*time.Millisecond {
		t.Errorf("delay = %v, want [100ms, 125ms] for backoff without Retry-After", d)
	}
}

func TestWithRetryContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := withRetry(ctx, retryConfig{
		maxRetries: 100,
		baseDelay:  100 * time.Millisecond,
		maxDelay:   time.Second,
	}, "GET", "/slow", func() error {
		return &ServerError{HttpError: HttpError{StatusCode: 503}}
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}
