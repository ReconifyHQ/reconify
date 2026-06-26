.PHONY: help build build-all test lint fmt-check mod-check security preflight clean install bench-smoke bench-deterministic bench-realistic bench-full

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

help: ## Show this help message
	@echo 'Reconify CLI - Available Commands:'
	@echo ''
	@echo 'Build:'
	@echo '  make build      - Build CLI binary'
	@echo '  make build-all  - Cross-compile for all platforms'
	@echo '  make install    - Install CLI to $$GOPATH/bin'
	@echo ''
	@echo 'Testing:'
	@echo '  make test       - Run all tests with race detection and coverage'
	@echo '  make fmt-check  - Fail if Go files need gofmt'
	@echo '  make mod-check  - Verify modules and fail on go.mod/go.sum drift'
	@echo '  make lint       - Run linters'
	@echo '  make security   - Run govulncheck and gosec'
	@echo '  make preflight  - Run the full pre-PR quality and security gate'
	@echo '  make bench-smoke         - Run small correctness benchmarks'
	@echo '  make bench-deterministic - Run deterministic 1-N benchmarks'
	@echo '  make bench-realistic     - Run realistic synthetic benchmarks'
	@echo '  make bench-full          - Run larger benchmark suite'
	@echo ''
	@echo 'Clean:'
	@echo '  make clean      - Clean build artifacts'

# Build
build: ## Build CLI binary
	go build $(LDFLAGS) -o reconify ./cmd/reconify

build-all: build ## Cross-compile CLI for all platforms
	@echo "Building CLI for multiple platforms..."
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/reconify-linux-amd64 ./cmd/reconify
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/reconify-darwin-amd64 ./cmd/reconify
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/reconify-darwin-arm64 ./cmd/reconify
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/reconify-windows-amd64.exe ./cmd/reconify

# Testing
test: ## Run all tests
	go test -v -race -coverprofile=coverage.out ./...

fmt-check: ## Fail if Go files need gofmt
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || \
		(echo "Go files need gofmt:"; gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)

mod-check: ## Verify modules and fail on go.mod/go.sum drift
	go mod tidy -diff
	go mod verify

# Linting
lint: ## Run linters
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

security: ## Run vulnerability and security checks
	@if command -v govulncheck > /dev/null; then \
		govulncheck ./...; \
	elif test -x "$$(go env GOPATH)/bin/govulncheck"; then \
		"$$(go env GOPATH)/bin/govulncheck" ./...; \
	else \
		echo "govulncheck not installed. Install with: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	fi
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run --enable-only=gosec ./...; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

preflight: mod-check fmt-check lint security test build bench-smoke ## Run full pre-PR gate

# Benchmarks
bench-smoke: ## Run small correctness benchmarks
	./benchmarks/deterministic/run.sh --rows 1000
	./benchmarks/realistic/run.sh --rows 1000

bench-deterministic: ## Run deterministic 1-N benchmarks
	./benchmarks/deterministic/run.sh --rows 100000

bench-realistic: ## Run realistic synthetic benchmarks
	./benchmarks/realistic/run.sh --rows 100000

bench-full: ## Run larger benchmark suite
	./benchmarks/deterministic/run.sh --rows 1000000
	./benchmarks/realistic/run.sh --rows 1000000

# Installation
install: ## Install CLI to GOPATH/bin
	go install $(LDFLAGS) ./cmd/reconify

# Clean
clean: ## Clean build artifacts
	rm -f reconify coverage.out
	rm -rf dist/
	rm -rf benchmarks/.out/

.DEFAULT_GOAL := help
