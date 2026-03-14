.PHONY: generate lint typecheck test

generate:
	go run ./cmd/codegen

lint:
	go vet ./...

typecheck:
	go build ./...

test:
	go test ./...
