package lolzteam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type httpClient struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	rateLimiter *rateLimiter
	retryConfig retryConfig
}

// requestOptions describes a single API call.
type requestOptions struct {
	Method    string
	Path      string
	Query     url.Values
	Body      url.Values     // form-urlencoded body
	Multipart *multipartBody // multipart/form-data body (for file uploads)
	RawJSON   any            // JSON body (e.g. batch endpoints)
}

func newHTTPClient(config Config) *httpClient {
	config = config.withDefaults()

	transport := &http.Transport{}
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = t.Clone()
	}

	if config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			panic(fmt.Sprintf("lolzteam: invalid proxy URL %q: %v", config.ProxyURL, err))
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	rpm := config.RequestsPerMinute
	if rpm <= 0 {
		rpm = 300 // safe default
	}

	return &httpClient{
		baseURL: strings.TrimRight(config.BaseURL, "/"),
		token:   config.Token,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
		},
		rateLimiter: newRateLimiter(rpm),
		retryConfig: retryConfig{
			maxRetries: config.MaxRetries,
			baseDelay:  config.RetryBaseDelay,
			maxDelay:   config.RetryMaxDelay,
		},
	}
}

// request executes an HTTP request with rate limiting and retry.
// result must be a pointer to the response struct for JSON unmarshaling.
func (c *httpClient) request(ctx context.Context, opts requestOptions, result any) error {
	if err := c.rateLimiter.acquire(ctx); err != nil {
		return err
	}

	// Pre-encode multipart body once, before retry loop
	var multipartData []byte
	var multipartContentType string
	if opts.Multipart != nil {
		buf, ct, err := opts.Multipart.encode()
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

	return withRetry(ctx, c.retryConfig, func() error {
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
	})
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
