package lolzteam

import (
	"errors"
	"testing"
	"time"
)

func TestErrorHierarchyUnwrap(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isHttpErr bool
	}{
		{
			name:      "RateLimitError -> HttpError via Unwrap",
			err:       &RateLimitError{HttpError: HttpError{StatusCode: 429}},
			isHttpErr: true,
		},
		{
			name:      "AuthError -> HttpError via Unwrap",
			err:       &AuthError{HttpError: HttpError{StatusCode: 401}},
			isHttpErr: true,
		},
		{
			name:      "NotFoundError -> HttpError via Unwrap",
			err:       &NotFoundError{HttpError: HttpError{StatusCode: 404}},
			isHttpErr: true,
		},
		{
			name:      "ServerError -> HttpError via Unwrap",
			err:       &ServerError{HttpError: HttpError{StatusCode: 500}},
			isHttpErr: true,
		},
		{
			name:      "ConfigError is not HttpError",
			err:       &ConfigError{LolzteamError{Message: "bad"}},
			isHttpErr: false,
		},
		{
			name:      "NetworkError is not HttpError",
			err:       &NetworkError{LolzteamError: LolzteamError{Message: "fail"}, Err: errors.New("inner")},
			isHttpErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var httpErr *HttpError
			if got := errors.As(tt.err, &httpErr); got != tt.isHttpErr {
				t.Errorf("errors.As(*HttpError) = %v, want %v", got, tt.isHttpErr)
			}
		})
	}
}

func TestNewHttpErrorStatusCodes(t *testing.T) {
	tests := []struct {
		status   int
		wantType string
	}{
		{429, "*lolzteam.RateLimitError"},
		{401, "*lolzteam.AuthError"},
		{403, "*lolzteam.ForbiddenError"},
		{404, "*lolzteam.NotFoundError"},
		{500, "*lolzteam.ServerError"},
		{502, "*lolzteam.ServerError"},
		{503, "*lolzteam.ServerError"},
		{504, "*lolzteam.ServerError"},
		{511, "*lolzteam.ServerError"},
		{400, "*lolzteam.HttpError"},
		{418, "*lolzteam.HttpError"},
		{422, "*lolzteam.HttpError"},
	}

	for _, tt := range tests {
		t.Run(string(rune('0'+tt.status/100))+string(rune('0'+tt.status%100/10))+string(rune('0'+tt.status%10)), func(t *testing.T) {
			err := newHttpError(tt.status, []byte("body"), 0)

			switch tt.wantType {
			case "*lolzteam.RateLimitError":
				var target *RateLimitError
				if !errors.As(err, &target) {
					t.Errorf("status %d: expected *RateLimitError, got %T", tt.status, err)
				}
			case "*lolzteam.AuthError":
				var target *AuthError
				if !errors.As(err, &target) {
					t.Errorf("status %d: expected *AuthError, got %T", tt.status, err)
				}
			case "*lolzteam.ForbiddenError":
				var target *ForbiddenError
				if !errors.As(err, &target) {
					t.Errorf("status %d: expected *ForbiddenError, got %T", tt.status, err)
				}
			case "*lolzteam.NotFoundError":
				var target *NotFoundError
				if !errors.As(err, &target) {
					t.Errorf("status %d: expected *NotFoundError, got %T", tt.status, err)
				}
			case "*lolzteam.ServerError":
				var target *ServerError
				if !errors.As(err, &target) {
					t.Errorf("status %d: expected *ServerError, got %T", tt.status, err)
				}
			case "*lolzteam.HttpError":
				// Should be plain HttpError, not a subtype
				var rlErr *RateLimitError
				var authErr *AuthError
				var nfErr *NotFoundError
				var srvErr *ServerError
				if errors.As(err, &rlErr) || errors.As(err, &authErr) || errors.As(err, &nfErr) || errors.As(err, &srvErr) {
					t.Errorf("status %d: should be plain *HttpError, got %T", tt.status, err)
				}
			}
		})
	}
}

func TestRetryExhaustedErrorMessage(t *testing.T) {
	inner := &ServerError{HttpError: HttpError{
		LolzteamError: LolzteamError{Message: "oops"},
		StatusCode:    503,
	}}
	err := &RetryExhaustedError{Attempts: 3, Err: inner}

	want := "request failed after 3 attempts: server error (HTTP 503): oops"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestRetryExhaustedErrorUnwrap(t *testing.T) {
	inner := &RateLimitError{HttpError: HttpError{StatusCode: 429}, RetryAfter: time.Second}
	err := &RetryExhaustedError{Attempts: 2, Err: inner}

	// Unwrap should give us the inner error
	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Error("should unwrap to *RateLimitError")
	}

	// And through that, HttpError
	var httpErr *HttpError
	if !errors.As(err, &httpErr) {
		t.Error("should unwrap to *HttpError through RateLimitError")
	}
}

func TestNetworkErrorUnwrapChain(t *testing.T) {
	inner := errors.New("connection reset by peer")
	err := &NetworkError{LolzteamError: LolzteamError{Message: "req failed"}, Err: inner}

	if !errors.Is(err, inner) {
		t.Error("NetworkError should unwrap to inner error via errors.Is")
	}

	// Should not match HttpError
	var httpErr *HttpError
	if errors.As(err, &httpErr) {
		t.Error("NetworkError should not match *HttpError")
	}
}

func TestRateLimitErrorRetryAfterInBody(t *testing.T) {
	err := newHttpError(429, []byte("slow down"), 5*time.Second)

	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *RateLimitError, got %T", err)
	}
	if rlErr.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter = %v, want 5s", rlErr.RetryAfter)
	}
	if string(rlErr.Body) != "slow down" {
		t.Errorf("Body = %q, want %q", rlErr.Body, "slow down")
	}
}
