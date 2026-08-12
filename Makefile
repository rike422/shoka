CGO_ENABLED ?= 1
GO_TAGS     ?= fts5
GOFLAGS     ?= -tags=$(GO_TAGS)
export CGO_ENABLED GOFLAGS

.PHONY: all fmt tidy lint vet test build check clean

all: check

## Format Go sources
fmt:
	gofmt -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w -local github.com/rike422/shoka . || true

## Sync go.mod / go.sum
tidy:
	go mod tidy

## Lint with golangci-lint (install: https://golangci-lint.run/welcome/install/)
lint:
	@if [ -x .bin/golangci-lint ]; then \
		.bin/golangci-lint run --timeout=5m ./...; \
	else \
		golangci-lint run --timeout=5m ./...; \
	fi

## go vet
vet:
	go vet ./...

## Unit / integration tests
test:
	go test ./...

## Build CLI binary to dist/shoka (release-ish strip)
build:
	mkdir -p dist
	go build -trimpath -ldflags='-s -w' -o dist/shoka ./cmd/shoka

## fmt + tidy + lint + test + build
check: fmt tidy lint test build
	git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum dirty after tidy; commit changes" && exit 1)

clean:
	rm -rf dist
