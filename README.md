# lolzteam-go

Fully typed Go API wrapper for [Lolzteam](https://lolz.live) Forum and Market APIs.

**151 Forum endpoints + 115 Market endpoints** — all generated from the official OpenAPI schemas.

## Installation

```bash
go get github.com/kyo-lzt/lolzteam-go
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"

	lolzteam "github.com/kyo-lzt/lolzteam-go"
)

func main() {
	ctx := context.Background()

	// Forum
	forum, err := lolzteam.NewForumClient(lolzteam.Config{Token: "your_token"})
	if err != nil {
		panic(err)
	}

	threads, _ := forum.Threads.List(ctx, &lolzteam.ThreadsListParams{ForumID: ptr(876)})
	fmt.Println(threads.Threads[0].ThreadTitle)

	// Market
	market, err := lolzteam.NewMarketClient(lolzteam.Config{Token: "your_token"})
	if err != nil {
		panic(err)
	}

	items, _ := market.Category.Steam(ctx, &lolzteam.CategorySteamParams{Pmin: ptr(100)})
	fmt.Println(items.Items[0].Title)

	item, _ := market.Managing.Get(ctx, 123456)
	fmt.Println(item.Item.Title)
}

func ptr[T any](v T) *T { return &v }
```

## Features

- **Fully typed** — generated request/response structs from OpenAPI schemas
- **Zero dependencies** — stdlib `net/http` only, no external packages
- **Auto-retry** — 429 (respects `Retry-After`), 502, 503 with exponential backoff + jitter
- **Rate limiting** — built-in token bucket (Forum: 300 req/min, Market: 120 req/min)
- **Proxy support** — via custom `http.Transport`
- **File uploads** — `multipart/form-data` for avatar/background endpoints
- **Context support** — all methods accept `context.Context`

## Configuration

```go
forum, err := lolzteam.NewForumClient(lolzteam.Config{
	// Required
	Token: "your_bearer_token",

	// Optional: custom base URL
	BaseURL: "https://api.lolz.live",

	// Optional: proxy (http, https, socks5)
	ProxyURL: "socks5://proxy:1080",

	// Optional: retry config
	MaxRetries:     5,              // default: 3
	RetryBaseDelay: 2 * time.Second,  // default: 1s
	RetryMaxDelay:  60 * time.Second, // default: 30s

	// Optional: rate limit override
	RequestsPerMinute: 60,

	// Optional: request timeout
	Timeout: 15 * time.Second, // default: 30s
})
if err != nil {
	log.Fatal(err)
}
```

## Error Handling

```go
import "errors"

item, err := market.Managing.Get(ctx, 999999)
if err != nil {
	var rateLimitErr *lolzteam.RateLimitError
	var authErr *lolzteam.AuthError
	var notFoundErr *lolzteam.NotFoundError
	var serverErr *lolzteam.ServerError
	var networkErr *lolzteam.NetworkError

	switch {
	case errors.As(err, &rateLimitErr):
		fmt.Printf("Rate limited, retry after %ds\n", rateLimitErr.RetryAfter)
	case errors.As(err, &authErr):
		fmt.Println("Invalid token")
	case errors.As(err, &notFoundErr):
		fmt.Println("Item not found")
	case errors.As(err, &serverErr):
		fmt.Printf("Server error: %d\n", serverErr.StatusCode)
	case errors.As(err, &networkErr):
		fmt.Println("Network error:", networkErr)
	}
}
```

## API Groups

### ForumClient

| Group | Methods | Description |
|-------|---------|-------------|
| `forum.Threads` | 22 | Threads CRUD, follow, bump, move |
| `forum.Posts` | 15 | Posts CRUD, like, report |
| `forum.Users` | 26 | Users, avatar/background upload, settings |
| `forum.Conversations` | 23 | Private conversations |
| `forum.ProfilePosts` | 18 | Profile posts and comments |
| `forum.Chatbox` | 12 | Chat messages |
| `forum.Forums` | 9 | Forum listing, follow |
| `forum.Search` | 7 | Search threads, posts, users |
| `forum.Tags` | 4 | Content tags |
| `forum.Notifications` | 3 | Notifications |
| `forum.Categories` | 2 | Categories |
| `forum.Forms` | 2 | Forms |
| `forum.Links` | 2 | Link forums |
| `forum.Pages` | 2 | Pages |
| `forum.Assets` | 1 | CSS assets |
| `forum.Batch` | 1 | Batch requests |
| `forum.Navigation` | 1 | Navigation |
| `forum.OAuth` | 1 | OAuth token |

### MarketClient

| Group | Methods | Description |
|-------|---------|-------------|
| `market.Managing` | 40 | Account management, edit, steam values |
| `market.Category` | 28 | Category search (Steam, Fortnite, etc.) |
| `market.Payments` | 12 | Payments, invoices, balance |
| `market.List` | 6 | User items, orders, favorites |
| `market.Purchasing` | 5 | Buy, confirm, discount requests |
| `market.Publishing` | 4 | Publish accounts for sale |
| `market.CustomDiscounts` | 4 | Custom discount management |
| `market.Cart` | 3 | Shopping cart |
| `market.AutoPayments` | 3 | Auto-payment setup |
| `market.Profile` | 3 | User profile |
| `market.Proxy` | 3 | Proxy management |
| `market.Imap` | 2 | IMAP email management |
| `market.Batch` | 1 | Batch requests |

## Code Generation

All client code and types are **generated from OpenAPI 3.1.0 schemas** — not written by hand.

```bash
make generate
# or
go run ./cmd/codegen
```

This reads `schemas/forum.json` and `schemas/market.json`, resolves all `$ref` pointers, and emits typed Go files. Generated code is committed to the repo — no codegen step needed at install time.

### Where types are generated

| What | Where |
|------|-------|
| Generator source | `cmd/codegen/` |
| Forum types | `forum_types.go` |
| Market types | `market_types.go` |
| Forum API groups | `forum_*.go` (18 files) |
| Market API groups | `market_*.go` (14 files) |

## Project Structure

```
lolzteam-go/
  schemas/              OpenAPI 3.1.0 specs (forum.json, market.json)
  cmd/codegen/          Code generator (reads OpenAPI -> emits Go code)
  forum_client.go       ForumClient + service wiring
  forum_*.go            Generated Forum service files and types
  market_client.go      MarketClient + service wiring
  market_*.go           Generated Market service files and types
  client.go             Base HTTP client
  config.go             Client configuration
  errors.go             Error hierarchy
  retry.go              Retry logic
  ratelimit.go          Token bucket rate limiter
  query.go              Query string encoding
  multipart.go          Multipart form encoding
  Makefile
  go.mod
```

## Development

```bash
make generate    # Regenerate clients from OpenAPI schemas
make lint        # go vet ./...
make typecheck   # go build ./...
make test        # go test ./...
```

## License

MIT
