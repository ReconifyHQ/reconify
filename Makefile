.PHONY: help build build-all test lint clean install

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
	@echo '  make test       - Run all tests'
	@echo '  make lint       - Run linters'
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

# Linting
lint: ## Run linters
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# Installation
install: ## Install CLI to GOPATH/bin
	go install $(LDFLAGS) ./cmd/reconify

# Clean
clean: ## Clean build artifacts
	rm -f reconify coverage.out
	rm -rf dist/

.DEFAULT_GOAL := help
