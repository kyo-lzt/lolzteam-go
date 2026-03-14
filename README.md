# lolzteam-go

Go API wrapper for Lolzteam Forum and Market. Clients and types are generated from OpenAPI specs. Zero external dependencies.

## Requirements

- Go 1.23+

## Setup

```bash
git clone https://github.com/kyo-lzt/lolzteam-go.git
cd lolzteam-go
```

## Code Generation

```bash
make generate
# or
go run ./cmd/codegen
```

Reads schemas from `schemas/forum.json` and `schemas/market.json`, generates typed clients into:

| What | Where |
|------|-------|
| Forum types | `forum/models.go` |
| Market types | `market/models.go` |
| Forum client | `forum/client.go` |
| Market client | `market/client.go` |

Generator source — `cmd/codegen/`.

## Project Structure

```
schemas/          — OpenAPI 3.1.0 specs
cmd/codegen/      — Code generator
*.go (root)       — HTTP client, retry, rate limiter, proxy, errors (package lolzteam)
forum/            — Generated Forum client and types
market/           — Generated Market client and types
Makefile
go.mod
```

## Commands

```bash
make generate    # Generate clients from schemas
make lint        # go vet ./...
make typecheck   # go build ./...
make test        # go test ./...
```

## License

MIT
