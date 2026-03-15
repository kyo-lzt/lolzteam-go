package lolzteam

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- Config defaults ---

func TestConfigDefaults(t *testing.T) {
	cfg := Config{Token: "t"}.withDefaults()

	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryBaseDelay != time.Second {
		t.Errorf("RetryBaseDelay = %v, want 1s", cfg.RetryBaseDelay)
	}
	if cfg.RetryMaxDelay != 30*time.Second {
		t.Errorf("RetryMaxDelay = %v, want 30s", cfg.RetryMaxDelay)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
}

func TestConfigDefaultsPreserveExplicit(t *testing.T) {
	cfg := Config{
		Token:          "t",
		MaxRetries:     5,
		RetryBaseDelay: 2 * time.Second,
		RetryMaxDelay:  10 * time.Second,
		Timeout:        15 * time.Second,
	}.withDefaults()

	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}
	if cfg.RetryBaseDelay != 2*time.Second {
		t.Errorf("RetryBaseDelay = %v, want 2s", cfg.RetryBaseDelay)
	}
	if cfg.RetryMaxDelay != 10*time.Second {
		t.Errorf("RetryMaxDelay = %v, want 10s", cfg.RetryMaxDelay)
	}
	if cfg.Timeout != 15*time.Second {
		t.Errorf("Timeout = %v, want 15s", cfg.Timeout)
	}
}

func TestForumClientDefaultBaseURL(t *testing.T) {
	c, err := NewClient(Config{Token: "t", BaseURL: "https://prod-api.lolz.live", RequestsPerMinute: 300})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	if c.baseURL != "https://prod-api.lolz.live" {
		t.Errorf("forum baseURL = %q, want %q", c.baseURL, "https://prod-api.lolz.live")
	}
}

func TestMarketClientDefaultBaseURL(t *testing.T) {
	c, err := NewClient(Config{Token: "t", BaseURL: "https://prod-api.lzt.market", RequestsPerMinute: 120})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	if c.baseURL != "https://prod-api.lzt.market" {
		t.Errorf("market baseURL = %q, want %q", c.baseURL, "https://prod-api.lzt.market")
	}
}

// --- Error types ---

func TestNewHttpError429(t *testing.T) {
	err := newHttpError(429, []byte("too many"), 5*time.Second)

	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *RateLimitError, got %T", err)
	}
	if rlErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", rlErr.StatusCode)
	}
	if rlErr.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter = %v, want 5s", rlErr.RetryAfter)
	}

	// Unwrap should yield *HttpError
	var httpErr *HttpError
	if !errors.As(rlErr, &httpErr) {
		t.Error("RateLimitError should unwrap to *HttpError")
	}
}

func TestNewHttpError401(t *testing.T) {
	err := newHttpError(401, []byte("unauthorized"), 0)

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *AuthError for 401, got %T", err)
	}
	if authErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", authErr.StatusCode)
	}

	var httpErr *HttpError
	if !errors.As(authErr, &httpErr) {
		t.Error("AuthError should unwrap to *HttpError")
	}
}

func TestNewHttpError403(t *testing.T) {
	err := newHttpError(403, []byte("forbidden"), 0)

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *AuthError for 403, got %T", err)
	}
	if authErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", authErr.StatusCode)
	}
}

func TestNewHttpError404(t *testing.T) {
	err := newHttpError(404, []byte("not found"), 0)

	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected *NotFoundError for 404, got %T", err)
	}
	if nfErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", nfErr.StatusCode)
	}

	var httpErr *HttpError
	if !errors.As(nfErr, &httpErr) {
		t.Error("NotFoundError should unwrap to *HttpError")
	}
}

func TestNewHttpError500(t *testing.T) {
	err := newHttpError(500, []byte("internal"), 0)

	var srvErr *ServerError
	if !errors.As(err, &srvErr) {
		t.Fatalf("expected *ServerError for 500, got %T", err)
	}
	if srvErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", srvErr.StatusCode)
	}

	var httpErr *HttpError
	if !errors.As(srvErr, &httpErr) {
		t.Error("ServerError should unwrap to *HttpError")
	}
}

func TestNewHttpError502(t *testing.T) {
	err := newHttpError(502, []byte("bad gateway"), 0)

	var srvErr *ServerError
	if !errors.As(err, &srvErr) {
		t.Fatalf("expected *ServerError for 502, got %T", err)
	}
}

func TestNewHttpErrorGeneric(t *testing.T) {
	err := newHttpError(400, []byte("bad request"), 0)

	var httpErr *HttpError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HttpError for 400, got %T", err)
	}
	if httpErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", httpErr.StatusCode)
	}
	if string(httpErr.Body) != "bad request" {
		t.Errorf("Body = %q, want %q", httpErr.Body, "bad request")
	}

	// Should NOT match more specific types
	var rlErr *RateLimitError
	if errors.As(err, &rlErr) {
		t.Error("400 should not match *RateLimitError")
	}
	var authErr *AuthError
	if errors.As(err, &authErr) {
		t.Error("400 should not match *AuthError")
	}
}

func TestNetworkError(t *testing.T) {
	inner := errors.New("connection refused")
	err := &NetworkError{
		LolzteamError: LolzteamError{Message: "request failed"},
		Err:           inner,
	}

	if !errors.Is(err, inner) {
		t.Error("NetworkError should unwrap to inner error")
	}

	msg := err.Error()
	if msg != "network error: connection refused" {
		t.Errorf("Error() = %q, want %q", msg, "network error: connection refused")
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "LolzteamError",
			err:  &LolzteamError{Message: "something"},
			want: "something",
		},
		{
			name: "HttpError",
			err:  &HttpError{LolzteamError: LolzteamError{Message: "bad"}, StatusCode: 418},
			want: "HTTP 418: bad",
		},
		{
			name: "RateLimitError with retry",
			err:  &RateLimitError{RetryAfter: 3 * time.Second},
			want: "rate limited (retry after 3s)",
		},
		{
			name: "RateLimitError without retry",
			err:  &RateLimitError{},
			want: "rate limited",
		},
		{
			name: "AuthError",
			err: &AuthError{HttpError: HttpError{
				LolzteamError: LolzteamError{Message: "denied"},
				StatusCode:    403,
			}},
			want: "auth error (HTTP 403): denied",
		},
		{
			name: "NotFoundError",
			err: &NotFoundError{HttpError: HttpError{
				LolzteamError: LolzteamError{Message: "/missing"},
				StatusCode:    404,
			}},
			want: "not found: /missing",
		},
		{
			name: "ServerError",
			err: &ServerError{HttpError: HttpError{
				LolzteamError: LolzteamError{Message: "oops"},
				StatusCode:    503,
			}},
			want: "server error (HTTP 503): oops",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Retry logic ---

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"RateLimitError", &RateLimitError{}, true},
		{"ServerError", &ServerError{}, true},
		{"NetworkError/transient", &NetworkError{Err: io.ErrUnexpectedEOF}, true},
		{"NetworkError/permanent", &NetworkError{Err: errors.New("some error")}, false},
		{"AuthError", &AuthError{}, false},
		{"NotFoundError", &NotFoundError{}, false},
		{"HttpError", &HttpError{StatusCode: 400}, false},
		{"plain error", errors.New("something"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.retryable {
				t.Errorf("isRetryable = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestWithRetrySuccess(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), retryConfig{
		maxRetries: 3,
		baseDelay:  time.Millisecond,
		maxDelay:   10 * time.Millisecond,
	}, "GET", "/test", func() error {
		calls++
		return nil
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestWithRetryEventualSuccess(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), retryConfig{
		maxRetries: 3,
		baseDelay:  time.Millisecond,
		maxDelay:   10 * time.Millisecond,
	}, "GET", "/test", func() error {
		calls++
		if calls < 3 {
			return &ServerError{HttpError: HttpError{StatusCode: 500}}
		}
		return nil
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestWithRetryExhaustsRetries(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), retryConfig{
		maxRetries: 2,
		baseDelay:  time.Millisecond,
		maxDelay:   10 * time.Millisecond,
	}, "GET", "/test", func() error {
		calls++
		return &ServerError{HttpError: HttpError{StatusCode: 503}}
	})

	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}

	var srvErr *ServerError
	if !errors.As(err, &srvErr) {
		t.Errorf("expected *ServerError, got %T", err)
	}
}

func TestWithRetryNonRetryableStopsImmediately(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), retryConfig{
		maxRetries: 5,
		baseDelay:  time.Millisecond,
		maxDelay:   10 * time.Millisecond,
	}, "GET", "/test", func() error {
		calls++
		return &AuthError{HttpError: HttpError{StatusCode: 401}}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-retryable should stop immediately)", calls)
	}
}

func TestWithRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := withRetry(ctx, retryConfig{
		maxRetries: 100,
		baseDelay:  50 * time.Millisecond,
		maxDelay:   100 * time.Millisecond,
	}, "GET", "/test", func() error {
		calls++
		return &ServerError{HttpError: HttpError{StatusCode: 500}}
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- HTTP client request ---

func TestHTTPClientSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "secret-token",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        1,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	err = c.Request(context.Background(), RequestOptions{
		Method: "GET",
		Path:   "/test",
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

func TestHTTPClientHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found"}`))
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "t",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        1,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	err = c.Request(context.Background(), RequestOptions{
		Method: "GET",
		Path:   "/missing",
	}, nil)

	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestHTTPClientJSONDecoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "t",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        1,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	var result map[string]string
	err = c.Request(context.Background(), RequestOptions{
		Method: "GET",
		Path:   "/data",
	}, &result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["hello"] != "world" {
		t.Errorf("result = %v, want {hello: world}", result)
	}
}

func TestHTTPClientRateLimitRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "t",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        3,
		RetryBaseDelay:    time.Millisecond,
		RetryMaxDelay:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	err = c.Request(context.Background(), RequestOptions{
		Method: "GET",
		Path:   "/limited",
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

// --- parseRetryAfter ---

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"0.5", 500 * time.Millisecond},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got := parseRetryAfter(tt.value)
			if got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// --- calcDelay ---

func TestCalcDelayExponentialBackoff(t *testing.T) {
	cfg := retryConfig{
		maxRetries: 5,
		baseDelay:  100 * time.Millisecond,
		maxDelay:   10 * time.Second,
	}
	srvErr := &ServerError{HttpError: HttpError{StatusCode: 500}}

	// attempt 0 -> ~100ms (+ up to 25% jitter)
	d0 := calcDelay(srvErr, 0, cfg)
	if d0 < 100*time.Millisecond || d0 > 125*time.Millisecond {
		t.Errorf("attempt 0 delay = %v, want [100ms, 125ms]", d0)
	}

	// attempt 1 -> ~200ms (+ up to 25% jitter)
	d1 := calcDelay(srvErr, 1, cfg)
	if d1 < 200*time.Millisecond || d1 > 250*time.Millisecond {
		t.Errorf("attempt 1 delay = %v, want [200ms, 250ms]", d1)
	}
}

func TestCalcDelayRespectMaxDelay(t *testing.T) {
	cfg := retryConfig{
		maxRetries: 10,
		baseDelay:  time.Second,
		maxDelay:   5 * time.Second,
	}
	srvErr := &ServerError{HttpError: HttpError{StatusCode: 500}}

	d := calcDelay(srvErr, 8, cfg)
	// After capping at maxDelay (5s), jitter adds up to 25%
	if d > 5*time.Second+5*time.Second/4 {
		t.Errorf("delay %v exceeds maxDelay + 25%% jitter", d)
	}
}

func TestCalcDelayUsesRetryAfterForRateLimit(t *testing.T) {
	cfg := retryConfig{
		maxRetries: 3,
		baseDelay:  100 * time.Millisecond,
		maxDelay:   30 * time.Second,
	}
	rlErr := &RateLimitError{
		HttpError:  HttpError{StatusCode: 429},
		RetryAfter: 2 * time.Second,
	}

	d := calcDelay(rlErr, 0, cfg)
	if d != 2*time.Second {
		t.Errorf("delay = %v, want 2s (from Retry-After)", d)
	}
}

func TestCalcDelayRateLimitCappedByMaxDelay(t *testing.T) {
	cfg := retryConfig{
		maxRetries: 3,
		baseDelay:  100 * time.Millisecond,
		maxDelay:   time.Second,
	}
	rlErr := &RateLimitError{
		HttpError:  HttpError{StatusCode: 429},
		RetryAfter: 60 * time.Second,
	}

	d := calcDelay(rlErr, 0, cfg)
	if d != time.Second {
		t.Errorf("delay = %v, want 1s (capped by maxDelay)", d)
	}
}

// --- Proxy validation ---

func TestProxyInvalidScheme(t *testing.T) {
	_, err := NewClient(Config{
		Token:    "t",
		ProxyURL: "ftp://proxy:8080",
	})
	if err == nil {
		t.Fatal("expected error for unsupported proxy scheme")
	}
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected *ConfigError, got %T: %v", err, err)
	}
}

func TestProxyMissingHost(t *testing.T) {
	_, err := NewClient(Config{
		Token:    "t",
		ProxyURL: "http://",
	})
	if err == nil {
		t.Fatal("expected error for proxy URL with no host")
	}
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected *ConfigError, got %T: %v", err, err)
	}
}

func TestProxyValidURL(t *testing.T) {
	tests := []struct {
		name     string
		proxyURL string
	}{
		{"http", "http://proxy:8080"},
		{"https", "https://proxy:8080"},
		{"socks5", "socks5://proxy:1080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(Config{
				Token:    "t",
				ProxyURL: tt.proxyURL,
			})
			if err != nil {
				t.Fatalf("unexpected error for %s proxy: %v", tt.name, err)
			}
			if c == nil {
				t.Fatal("NewClient returned nil")
			}
		})
	}
}

func TestConfigErrorMessage(t *testing.T) {
	err := &ConfigError{LolzteamError{Message: "bad proxy"}}
	want := "config error: bad proxy"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// --- Rate limiter ---

func TestRateLimiterAllowsBurst(t *testing.T) {
	rl := newRateLimiter(600) // 10 per sec
	start := time.Now()
	for i := 0; i < 10; i++ {
		if err := rl.acquire(context.Background()); err != nil {
			t.Fatalf("acquire error: %v", err)
		}
	}
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Errorf("burst of 10 requests took %v, expected near-instant", elapsed)
	}
}

func TestRateLimiterDelaysWhenExhausted(t *testing.T) {
	rl := newRateLimiter(60) // 1 per sec
	// Drain all tokens
	for i := 0; i < 60; i++ {
		if err := rl.acquire(context.Background()); err != nil {
			t.Fatalf("acquire error: %v", err)
		}
	}
	start := time.Now()
	if err := rl.acquire(context.Background()); err != nil {
		t.Fatalf("acquire error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 500*time.Millisecond {
		t.Errorf("expected delay after exhaustion, got %v", elapsed)
	}
}

func TestRateLimiterCancellation(t *testing.T) {
	rl := newRateLimiter(1) // 1 per minute
	// Drain the single token
	if err := rl.acquire(context.Background()); err != nil {
		t.Fatalf("acquire error: %v", err)
	}
	// Cancel immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rl.acquire(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- StructToQuery ---

func TestStructToQueryBasic(t *testing.T) {
	type Params struct {
		Name  *string `query:"name"`
		Page  *int    `query:"page"`
		Empty *string `query:"empty"`
	}
	name := "test"
	page := 2
	vals := StructToQuery(&Params{Name: &name, Page: &page})
	if vals.Get("name") != "test" {
		t.Errorf("name = %q, want %q", vals.Get("name"), "test")
	}
	if vals.Get("page") != "2" {
		t.Errorf("page = %q, want %q", vals.Get("page"), "2")
	}
	if vals.Has("empty") {
		t.Error("empty should not be present when nil")
	}
}

func TestStructToQueryBool(t *testing.T) {
	type Params struct {
		Active  *bool `query:"active"`
		Deleted *bool `query:"deleted"`
	}
	yes := true
	no := false
	vals := StructToQuery(&Params{Active: &yes, Deleted: &no})
	if vals.Get("active") != "1" {
		t.Errorf("active = %q, want %q", vals.Get("active"), "1")
	}
	if vals.Get("deleted") != "0" {
		t.Errorf("deleted = %q, want %q", vals.Get("deleted"), "0")
	}
}

func TestStructToQueryNil(t *testing.T) {
	vals := StructToQuery(nil)
	if len(vals) != 0 {
		t.Errorf("expected empty values for nil, got %v", vals)
	}
}

func TestStructToFormBasic(t *testing.T) {
	type Body struct {
		Title   *string `form:"title"`
		Content *string `form:"content"`
	}
	title := "hello"
	content := "world"
	vals := StructToForm(&Body{Title: &title, Content: &content})
	if vals.Get("title") != "hello" {
		t.Errorf("title = %q, want %q", vals.Get("title"), "hello")
	}
	if vals.Get("content") != "world" {
		t.Errorf("content = %q, want %q", vals.Get("content"), "world")
	}
}

// --- HTTP server error tests ---

func TestHTTPClientServerError503Retry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("service unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "t",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        3,
		RetryBaseDelay:    time.Millisecond,
		RetryMaxDelay:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	err = c.Request(context.Background(), RequestOptions{
		Method: "GET",
		Path:   "/service",
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestHTTPClientNonRetryable401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "t",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        3,
		RetryBaseDelay:    time.Millisecond,
		RetryMaxDelay:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	err = c.Request(context.Background(), RequestOptions{
		Method: "GET",
		Path:   "/secret",
	}, nil)

	if err == nil {
		t.Fatal("expected error for 401")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-retryable should not retry)", calls)
	}

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Errorf("expected *AuthError, got %T", err)
	}
}

func TestHTTPClientFormBody(t *testing.T) {
	var gotContentType string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Token:             "t",
		BaseURL:           srv.URL,
		RequestsPerMinute: 600,
		MaxRetries:        1,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	body := url.Values{"title": {"hello"}, "content": {"world"}}
	err = c.Request(context.Background(), RequestOptions{
		Method: "POST",
		Path:   "/posts",
		Body:   body,
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if !strings.Contains(gotBody, "title=hello") {
		t.Errorf("body = %q, expected to contain title=hello", gotBody)
	}
}
