package lolzteam

import (
	"fmt"
	"time"
)

// LolzteamError is the base error for all library errors.
type LolzteamError struct {
	Message string
}

func (e *LolzteamError) Error() string {
	return e.Message
}

// HttpError represents an HTTP response error.
type HttpError struct {
	LolzteamError
	StatusCode int
	Body       []byte
}

func (e *HttpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// RateLimitError indicates a 429 Too Many Requests response.
type RateLimitError struct {
	HttpError
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited (retry after %s)", e.RetryAfter)
	}
	return "rate limited"
}

func (e *RateLimitError) Unwrap() error {
	return &e.HttpError
}

// AuthError indicates a 401 or 403 response.
type AuthError struct {
	HttpError
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("auth error (HTTP %d): %s", e.StatusCode, e.Message)
}

func (e *AuthError) Unwrap() error {
	return &e.HttpError
}

// NotFoundError indicates a 404 response.
type NotFoundError struct {
	HttpError
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("not found: %s", e.Message)
}

func (e *NotFoundError) Unwrap() error {
	return &e.HttpError
}

// ServerError indicates a 5xx response.
type ServerError struct {
	HttpError
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server error (HTTP %d): %s", e.StatusCode, e.Message)
}

func (e *ServerError) Unwrap() error {
	return &e.HttpError
}

// ConfigError indicates invalid client configuration.
type ConfigError struct {
	LolzteamError
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("config error: %s", e.Message)
}

// NetworkError represents connection, DNS, or timeout failures.
type NetworkError struct {
	LolzteamError
	Err error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error: %s", e.Err)
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

// newHttpError returns the appropriate typed error based on status code.
func newHttpError(statusCode int, body []byte, retryAfter time.Duration) error {
	base := HttpError{
		LolzteamError: LolzteamError{Message: string(body)},
		StatusCode:    statusCode,
		Body:          body,
	}

	switch {
	case statusCode == 429:
		return &RateLimitError{HttpError: base, RetryAfter: retryAfter}
	case statusCode == 401 || statusCode == 403:
		return &AuthError{HttpError: base}
	case statusCode == 404:
		return &NotFoundError{HttpError: base}
	case statusCode >= 500:
		return &ServerError{HttpError: base}
	default:
		return &base
	}
}
