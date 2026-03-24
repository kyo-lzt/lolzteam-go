package lolzteam

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"

	"golang.org/x/net/proxy"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// --- Config ---

// RetryInfo provides context about a retry attempt, passed to OnRetry callback.
type RetryInfo struct {
	Attempt int
	Delay   time.Duration
	Err     error
	Method  string
	Path    string
}

// RetryConfig holds retry behavior settings.
// Use a non-nil pointer in Config to enable retries (nil = disabled).
type RetryConfig struct {
	MaxRetries int           // default: 3
	BaseDelay  time.Duration // default: 1s
	MaxDelay   time.Duration // default: 30s
}

// ProxyConfig holds proxy connection settings.
type ProxyConfig struct {
	URL string // e.g. "http://proxy:8080" or "socks5://proxy:1080"
}

// RateLimitConfig holds rate limiting settings.
type RateLimitConfig struct {
	RequestsPerMinute       int // default: per-client (Forum=300, Market=120)
	SearchRequestsPerMinute int // default: 0 (disabled); for Market: 20
}

// Config holds settings for creating Forum/Market clients.
type Config struct {
	Token     string
	BaseURL   string
	Timeout   time.Duration        // default: 30s
	Retry     *RetryConfig         // nil = disabled; non-nil = enabled with defaults for zero fields
	Proxy     *ProxyConfig         // nil = no proxy
	RateLimit *RateLimitConfig     // nil = use defaults
	OnRetry   func(info RetryInfo) // optional callback invoked before each retry sleep
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries: 3,
		BaseDelay:  time.Second,
		MaxDelay:   30 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.Retry != nil {
		if c.Retry.MaxRetries <= 0 {
			c.Retry.MaxRetries = 3
		}
		if c.Retry.BaseDelay <= 0 {
			c.Retry.BaseDelay = time.Second
		}
		if c.Retry.MaxDelay <= 0 {
			c.Retry.MaxDelay = 30 * time.Second
		}
	}
	return c
}

// --- Client & Request ---

type Client struct {
	baseURL            string
	token              string
	httpClient         *http.Client
	rateLimiter        *rateLimiter
	searchRateLimiter  *rateLimiter
	retryConfig        *retryConfig // nil = retry disabled
}

// RequestOptions describes a single API call.
type RequestOptions struct {
	Method    string
	Path      string
	Query     url.Values
	Body      url.Values      // form-urlencoded body
	Multipart *MultipartBody  // multipart/form-data body (for file uploads)
	RawJSON   any             // JSON body (e.g. batch endpoints)
	IsSearch  bool            // true for search endpoints (category group)
}

func NewClient(config Config) (*Client, error) {
	config = config.withDefaults()

	transport := &http.Transport{}
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = t.Clone()
	}

	if config.Proxy != nil && config.Proxy.URL != "" {
		proxyURL, err := url.Parse(config.Proxy.URL)
		if err != nil {
			return nil, &ConfigError{LolzteamError{Message: fmt.Sprintf("invalid proxy URL %q: %v", config.Proxy.URL, err)}}
		}

		switch proxyURL.Scheme {
		case "http", "https", "socks5":
			// valid
		default:
			return nil, &ConfigError{LolzteamError{Message: fmt.Sprintf("unsupported proxy scheme %q, must be http, https, or socks5", proxyURL.Scheme)}}
		}

		if proxyURL.Host == "" {
			return nil, &ConfigError{LolzteamError{Message: fmt.Sprintf("proxy URL %q has no host", config.Proxy.URL)}}
		}

		switch proxyURL.Scheme {
		case "socks5":
			dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
			if err != nil {
				return nil, &ConfigError{LolzteamError{Message: fmt.Sprintf("failed to create SOCKS5 dialer: %v", err)}}
			}
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		default:
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	rpm := 300 // safe default
	var searchRL *rateLimiter
	if config.RateLimit != nil {
		if config.RateLimit.RequestsPerMinute > 0 {
			rpm = config.RateLimit.RequestsPerMinute
		}
		if config.RateLimit.SearchRequestsPerMinute > 0 {
			searchRL = newRateLimiter(config.RateLimit.SearchRequestsPerMinute)
		}
	}

	c := &Client{
		baseURL: strings.TrimRight(config.BaseURL, "/"),
		token:   config.Token,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
		},
		rateLimiter:       newRateLimiter(rpm),
		searchRateLimiter: searchRL,
	}

	if config.Retry != nil {
		c.retryConfig = &retryConfig{
			maxRetries: config.Retry.MaxRetries,
			baseDelay:  config.Retry.BaseDelay,
			maxDelay:   config.Retry.MaxDelay,
			onRetry:    config.OnRetry,
		}
	}

	return c, nil
}

// Request executes an HTTP request with rate limiting and retry.
// result must be a pointer to the response struct for JSON unmarshaling.
func (c *Client) Request(ctx context.Context, opts RequestOptions, result any) error {
	if err := c.rateLimiter.acquire(ctx); err != nil {
		return err
	}

	if opts.IsSearch && c.searchRateLimiter != nil {
		if err := c.searchRateLimiter.acquire(ctx); err != nil {
			return err
		}
	}

	// Pre-encode multipart body once, before retry loop
	var multipartData []byte
	var multipartContentType string
	if opts.Multipart != nil {
		buf, ct, err := opts.Multipart.Encode()
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "failed to encode multipart body"}, Err: err}
		}
		multipartData = buf
		multipartContentType = ct
	}

	// Pre-encode JSON body once, before retry loop
	var rawJSONData []byte
	if opts.RawJSON != nil {
		var err error
		rawJSONData, err = json.Marshal(opts.RawJSON)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "failed to encode JSON body"}, Err: err}
		}
	}

	doRequest := func() error {
		reqURL := c.baseURL + opts.Path
		if len(opts.Query) > 0 {
			encoded := opts.Query.Encode()
			encoded = strings.ReplaceAll(encoded, "%5B", "[")
			encoded = strings.ReplaceAll(encoded, "%5D", "]")
			reqURL += "?" + encoded
		}

		var bodyReader io.Reader
		var contentType string

		if rawJSONData != nil {
			bodyReader = bytes.NewReader(rawJSONData)
			contentType = "application/json"
		} else if multipartData != nil {
			bodyReader = bytes.NewReader(multipartData)
			contentType = multipartContentType
		} else if len(opts.Body) > 0 {
			bodyReader = strings.NewReader(opts.Body.Encode())
			contentType = "application/x-www-form-urlencoded"
		}

		req, err := http.NewRequestWithContext(ctx, opts.Method, reqURL, bodyReader)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "failed to create request"}, Err: err}
		}

		req.Header.Set("Authorization", "Bearer "+c.token)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "request failed"}, Err: err}
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "failed to read response body"}, Err: err}
		}

		if resp.StatusCode >= 400 {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			return newHttpError(resp.StatusCode, body, retryAfter)
		}

		if result != nil {
			if err := json.Unmarshal(body, result); err != nil {
				return &NetworkError{
					LolzteamError: LolzteamError{Message: "failed to decode response"},
					Err:           err,
				}
			}
		}

		return nil
	}

	if c.retryConfig == nil {
		return doRequest()
	}
	return withRetry(ctx, *c.retryConfig, opts.Method, opts.Path, doRequest)
}

// RequestText executes an HTTP request and returns the response body as a raw string.
// Used for endpoints that return text/html or other non-JSON content types.
func (c *Client) RequestText(ctx context.Context, opts RequestOptions) (string, error) {
	if err := c.rateLimiter.acquire(ctx); err != nil {
		return "", err
	}

	if opts.IsSearch && c.searchRateLimiter != nil {
		if err := c.searchRateLimiter.acquire(ctx); err != nil {
			return "", err
		}
	}

	var result string
	doRequest := func() error {
		reqURL := c.baseURL + opts.Path
		if len(opts.Query) > 0 {
			encoded := opts.Query.Encode()
			encoded = strings.ReplaceAll(encoded, "%5B", "[")
			encoded = strings.ReplaceAll(encoded, "%5D", "]")
			reqURL += "?" + encoded
		}

		req, err := http.NewRequestWithContext(ctx, opts.Method, reqURL, nil)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "failed to create request"}, Err: err}
		}

		req.Header.Set("Authorization", "Bearer "+c.token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "request failed"}, Err: err}
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return &NetworkError{LolzteamError: LolzteamError{Message: "failed to read response body"}, Err: err}
		}

		if resp.StatusCode >= 400 {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			return newHttpError(resp.StatusCode, body, retryAfter)
		}

		result = string(body)
		return nil
	}

	if c.retryConfig == nil {
		if err := doRequest(); err != nil {
			return "", err
		}
		return result, nil
	}
	if err := withRetry(ctx, *c.retryConfig, opts.Method, opts.Path, doRequest); err != nil {
		return "", err
	}
	return result, nil
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	// Try numeric seconds first
	seconds, err := strconv.ParseFloat(value, 64)
	if err == nil {
		return time.Duration(seconds * float64(time.Second))
	}
	// Try HTTP-date format
	t, err := http.ParseTime(value)
	if err == nil {
		delay := time.Until(t)
		if delay > 0 {
			return delay
		}
		return 0
	}
	return 0
}

// --- Query/Form helpers ---

// StructToQuery converts a struct pointer to url.Values using `query` struct tags.
// Nil pointer fields are skipped.
func StructToQuery(v any) url.Values {
	return structToValues(v, "query")
}

// StructToForm converts a struct pointer to url.Values using `form` struct tags.
// Nil pointer fields are skipped.
func StructToForm(v any) url.Values {
	return structToValues(v, "form")
}

func structToValues(v any, tagName string) url.Values {
	values := url.Values{}
	if v == nil {
		return values
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return values
		}
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return values
	}

	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		tag := field.Tag.Get(tagName)
		if tag == "" || tag == "-" {
			continue
		}

		// Handle comma-separated tag options (e.g. `query:"name,omitempty"`)
		name, _, _ := strings.Cut(tag, ",")

		fieldVal := rv.Field(i)

		// Apply default value if the field is a nil pointer and has a default tag
		if fieldVal.Kind() == reflect.Ptr && fieldVal.IsNil() {
			if def := field.Tag.Get("default"); def != "" {
				values.Set(name, def)
				continue
			}
		}

		appendFieldValues(&values, name, fieldVal)
	}

	return values
}

func appendFieldValues(values *url.Values, name string, fieldVal reflect.Value) {
	switch fieldVal.Kind() {
	case reflect.Ptr:
		if fieldVal.IsNil() {
			return
		}
		appendFieldValues(values, name, fieldVal.Elem())

	case reflect.String:
		values.Set(name, fieldVal.String())

	case reflect.Int, reflect.Int64:
		values.Set(name, fmt.Sprintf("%d", fieldVal.Int()))

	case reflect.Float64:
		values.Set(name, fmt.Sprintf("%g", fieldVal.Float()))

	case reflect.Bool:
		if fieldVal.Bool() {
			values.Set(name, "1")
		} else {
			values.Set(name, "0")
		}

	case reflect.Slice:
		for j := range fieldVal.Len() {
			elem := fieldVal.Index(j)
			switch elem.Kind() {
			case reflect.String:
				values.Add(name, elem.String())
			case reflect.Int, reflect.Int64:
				values.Add(name, fmt.Sprintf("%d", elem.Int()))
			case reflect.Float64:
				values.Add(name, fmt.Sprintf("%g", elem.Float()))
			}
		}

	case reflect.Map:
		for _, key := range fieldVal.MapKeys() {
			keyStr := fmt.Sprintf("%s[%s]", name, key)
			val := fieldVal.MapIndex(key)
			if val.Kind() == reflect.Interface {
				val = val.Elem()
			}
			switch val.Kind() {
			case reflect.String:
				values.Set(keyStr, val.String())
			case reflect.Int, reflect.Int64:
				values.Set(keyStr, fmt.Sprintf("%d", val.Int()))
			case reflect.Float64:
				values.Set(keyStr, fmt.Sprintf("%g", val.Float()))
			case reflect.Bool:
				if val.Bool() {
					values.Set(keyStr, "1")
				} else {
					values.Set(keyStr, "0")
				}
			}
		}

	case reflect.Interface:
		if !fieldVal.IsNil() {
			appendFieldValues(values, name, fieldVal.Elem())
		}
	}
}

// --- Multipart helpers ---

// FileUpload represents a file to upload via multipart/form-data.
type FileUpload struct {
	Filename string
	Data     io.Reader
}

// MultipartBody holds fields and files for a multipart/form-data request.
type MultipartBody struct {
	fields map[string]string
	files  map[string]fileField
}

type fileField struct {
	filename string
	data     io.Reader
}

// Encode writes the multipart body into a byte slice and returns it along with the content type.
func (mb *MultipartBody) Encode() (data []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	defer func() {
		if closeErr := w.Close(); closeErr != nil && err == nil {
			data = nil
			contentType = ""
			err = fmt.Errorf("failed to close multipart writer: %w", closeErr)
		}
	}()

	for name, value := range mb.fields {
		if err := w.WriteField(name, value); err != nil {
			return nil, "", fmt.Errorf("failed to write multipart field %q: %w", name, err)
		}
	}

	for name, file := range mb.files {
		part, err := w.CreateFormFile(name, file.filename)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create multipart file %q: %w", name, err)
		}
		if _, err := io.Copy(part, file.data); err != nil {
			return nil, "", fmt.Errorf("failed to write multipart file %q: %w", name, err)
		}
	}

	return buf.Bytes(), w.FormDataContentType(), nil
}

var fileUploadType = reflect.TypeOf(FileUpload{})

// StructToMultipart converts a struct pointer to a MultipartBody.
// Uses `form` struct tags. Fields of type FileUpload or *FileUpload become file parts.
// Other fields become text parts.
func StructToMultipart(v any) *MultipartBody {
	mb := &MultipartBody{
		fields: make(map[string]string),
		files:  make(map[string]fileField),
	}

	if v == nil {
		return mb
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return mb
		}
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return mb
	}

	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		tag := field.Tag.Get("form")
		if tag == "" || tag == "-" {
			continue
		}

		name, _, _ := strings.Cut(tag, ",")
		fieldVal := rv.Field(i)

		// Apply default value if the field is a nil pointer and has a default tag
		if fieldVal.Kind() == reflect.Ptr && fieldVal.IsNil() {
			if def := field.Tag.Get("default"); def != "" {
				mb.fields[name] = def
				continue
			}
		}

		appendMultipartField(mb, name, field.Type, fieldVal)
	}

	return mb
}

func appendMultipartField(mb *MultipartBody, name string, fieldType reflect.Type, fieldVal reflect.Value) {
	// Dereference pointer
	if fieldType.Kind() == reflect.Ptr {
		if fieldVal.IsNil() {
			return
		}
		fieldType = fieldType.Elem()
		fieldVal = fieldVal.Elem()
	}

	// Check if it's a FileUpload
	if fieldType == fileUploadType {
		fu := fieldVal.Interface().(FileUpload)
		if fu.Data != nil {
			mb.files[name] = fileField{
				filename: fu.Filename,
				data:     fu.Data,
			}
		}
		return
	}

	// Regular field -> text part
	switch fieldVal.Kind() {
	case reflect.String:
		mb.fields[name] = fieldVal.String()
	case reflect.Int, reflect.Int64:
		mb.fields[name] = fmt.Sprintf("%d", fieldVal.Int())
	case reflect.Float64:
		mb.fields[name] = fmt.Sprintf("%g", fieldVal.Float())
	case reflect.Bool:
		if fieldVal.Bool() {
			mb.fields[name] = "1"
		} else {
			mb.fields[name] = "0"
		}
	}
}

// --- Rate limiter ---

type rateLimiter struct {
	mu             sync.Mutex
	tokens         float64
	maxTokens      float64
	refillRate     float64 // tokens per second
	lastRefillTime time.Time
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

// --- Retry ---

type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	onRetry    func(info RetryInfo)
}

func withRetry(ctx context.Context, cfg retryConfig, method, path string, fn func() error) error {
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

		if cfg.onRetry != nil {
			cfg.onRetry(RetryInfo{
				Attempt: attempt,
				Delay:   delay,
				Err:     lastErr,
				Method:  method,
				Path:    path,
			})
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return &RetryExhaustedError{Attempts: cfg.maxRetries, Err: lastErr}
}

func isRetryable(err error) bool {
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return true
	}

	var serverErr *ServerError
	if errors.As(err, &serverErr) {
		// Only retry 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout.
		// 500 Internal Server Error is not retried — it typically indicates a bug, not a transient issue.
		switch serverErr.StatusCode {
		case 502, 503, 504:
			return true
		default:
			return false
		}
	}

	var networkErr *NetworkError
	if errors.As(err, &networkErr) {
		return isTransientNetworkError(networkErr.Err)
	}

	return false
}

// isTransientNetworkError returns true only for transient network errors
// that are worth retrying (timeouts, connection resets, unexpected EOF).
// Permanent errors like DNS resolution failures, connection refused,
// and TLS handshake errors are not retried.
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// io.ErrUnexpectedEOF — connection dropped mid-response
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// syscall.ECONNRESET — connection reset by peer
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	// net.Error with Timeout() == true — request/dial timeout
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Permanent errors — do not retry
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return false
		}
	}

	var tlsErr *tls.RecordHeaderError
	if errors.As(err, &tlsErr) {
		return false
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}

	// Unknown network error — don't retry to be safe
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

	// Add jitter up to baseDelay
	jitterBase := int64(cfg.baseDelay)
	if jitterBase > 0 {
		delay += time.Duration(rand.Int64N(jitterBase))
	}

	return delay
}
