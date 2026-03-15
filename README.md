# lolzteam-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/kyo-lzt/lolzteam-go/actions/workflows/ci.yml/badge.svg)](https://github.com/kyo-lzt/lolzteam-go/actions)

Go API wrapper for the [Lolzteam](https://lolz.live) Forum and Market APIs. **266 endpoints** (151 Forum + 115 Market), auto-generated from OpenAPI specifications. **Zero external dependencies** -- built entirely on the Go standard library.

## Features

- **Complete API coverage** -- 266 endpoints across Forum and Market clients
- **Auto-generated** -- clients and types generated from OpenAPI 3.1.0 specs, always in sync
- **Zero dependencies** -- uses only the Go standard library (`net/http`, `encoding/json`, etc.)
- **Context support** -- every method accepts `context.Context` for cancellation and timeouts
- **Proxy support** -- HTTP, HTTPS, and SOCKS5 via `http.Transport`
- **Retry logic** -- exponential backoff with jitter, respects `Retry-After` headers
- **Rate limiting** -- token bucket algorithm, mutex-based, thread-safe
- **Typed errors** -- structured error hierarchy with `errors.As` support

## Quick Start

```bash
go get github.com/kyo-lzt/lolzteam-go
```

Requires **Go 1.23+**.

## Usage

```go
package main

import (
    "context"
    "github.com/kyo-lzt/lolzteam-go"
    "github.com/kyo-lzt/lolzteam-go/forum"
    "github.com/kyo-lzt/lolzteam-go/market"
)

func main() {
    config := lolzteam.Config{Token: "your_token"}
    client, _ := lolzteam.NewClient(config)
    defer client.Close()

    ctx := context.Background()
    threads, _ := forum.NewClient(client).Threads.List(ctx, nil)
    items, _ := market.NewClient(client).CategorySearch.GetAll(ctx, nil)
}
```

Forum API groups: `Assets`, `Batch`, `Categories`, `Chatbox`, `Conversations`, `Forms`, `Forums`, `Links`, `Navigation`, `Notifications`, `OAuth`, `Pages`, `Posts`, `ProfilePosts`, `Search`, `Tags`, `Threads`, `Users`.

Market API groups: `AutoPayments`, `Batch`, `Cart`, `Category`, `CustomDiscounts`, `Imap`, `List`, `Managing`, `Payments`, `Profile`, `Proxy`, `Publishing`, `Purchasing`.

## Configuration

```go
config := lolzteam.Config{
    Token:             "your_token",
    ProxyURL:          "socks5://127.0.0.1:1080",
    MaxRetries:        5,               // default: 3
    RetryBaseDelay:    time.Second,      // default: 1s
    RetryMaxDelay:     30 * time.Second, // default: 30s
    RequestsPerMinute: 200,             // default: 300 (Forum), 120 (Market)
    Timeout:           30 * time.Second, // default: 30s
}
```

All fields except `Token` are optional and have sensible defaults.

## Retry Logic

Failed requests are retried automatically for transient errors. The delay uses exponential backoff with jitter. `Retry-After` header on 429 responses is respected.

| Status | Retried | Behavior |
|--------|---------|----------|
| 429 | Yes | Uses `Retry-After` header if present |
| 502, 503 | Yes | Exponential backoff with jitter |
| 401, 403 | No | Returned immediately |
| 404 | No | Returned immediately |
| Other | No | Returned immediately |

Delay formula: `min(baseDelay * 2^attempt + random(0, baseDelay), maxDelay)`

## Proxy

Pass a proxy URL in the config. Supported schemes: `http`, `https`, `socks5`.

```go
config := lolzteam.Config{
    Token:    "your_token",
    ProxyURL: "socks5://127.0.0.1:1080",
}
```

## Error Handling

All errors belong to a typed hierarchy. Use `errors.As` to match specific error types:

```go
var rateLimitErr *lolzteam.RateLimitError
if errors.As(err, &rateLimitErr) {
    fmt.Printf("rate limited, retry after %s\n", rateLimitErr.RetryAfter)
}

var httpErr *lolzteam.HttpError
if errors.As(err, &httpErr) {
    fmt.Printf("HTTP %d: %s\n", httpErr.StatusCode, httpErr.Body)
}
```

Error hierarchy:

```
LolzteamError
├── HttpError
│   ├── RateLimitError    (429)
│   ├── AuthError         (401, 403)
│   ├── NotFoundError     (404)
│   └── ServerError       (5xx)
├── NetworkError
└── ConfigError
```

## Rate Limits

The built-in rate limiter uses a token bucket algorithm. Mutex-based, thread-safe. When the bucket is empty, requests block until tokens refill -- no requests are dropped.

| Client | Default limit |
|--------|---------------|
| Forum  | 300 req/min   |
| Market | 120 req/min   |

## Code Generation

Clients and types are auto-generated from OpenAPI 3.1.0 specs in `schemas/`:

```bash
make generate
# or
go run ./cmd/codegen
```

| Input | Output |
|-------|--------|
| `schemas/forum.json` | `forum/client.go`, `forum/models.go` |
| `schemas/market.json` | `market/client.go`, `market/models.go` |

Generator source is in `cmd/codegen/` and `internal/codegen/`.

## Project Structure

```
schemas/                    OpenAPI 3.1.0 specifications
cmd/codegen/                Code generator entrypoint
internal/codegen/           Code generator implementation
client.go                   HTTP client, retry, rate limiter, proxy
errors.go                   Typed error hierarchy
forum/                      Generated Forum client and types
market/                     Generated Market client and types
Makefile
go.mod                      Module definition (zero dependencies)
```

## Commands

```bash
make generate    # Generate clients from OpenAPI specs
make lint        # Run go vet ./...
make typecheck   # Run go build ./...
make test        # Run go test ./...
```

## License

[MIT](LICENSE)
