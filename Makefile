.PHONY: help build build-all dev test lint clean docker-build docker-up docker-down install

help: ## Show this help message
	@echo 'Reconify Monorepo - Available Commands:'
	@echo ''
	@echo 'Development:'
	@echo '  make dev:api          - Start API in development mode'
	@echo '  make dev:dashboard    - Start dashboard in development mode'
	@echo ''
	@echo 'Build:'
	@echo '  make build            - Build all components'
	@echo '  make build:cli        - Build CLI only'
	@echo '  make build:api        - Build API only'
	@echo '  make build:dashboard  - Build dashboard only'
	@echo '  make build:docs       - Build docs site only'
	@echo ''
	@echo 'Testing:'
	@echo '  make test             - Run all tests'
	@echo '  make test:cli         - Run CLI tests only'
	@echo ''
	@echo 'Docker:'
	@echo '  make docker-build     - Build all Docker images'
	@echo '  make docker-up        - Start all services'
	@echo '  make docker-down      - Stop all services'
	@echo '  make docker-logs      - View Docker logs'
	@echo ''
	@echo 'Installation:'
	@echo '  make install          - Install CLI globally'
	@echo ''
	@echo 'Clean:'
	@echo '  make clean            - Clean all build artifacts'

# Development
dev: dev:api ## Start all dev servers (alias for dev:api)

dev:api: ## Start API in development mode
	pnpm --filter api dev

dev:dashboard: ## Start dashboard in development mode
	pnpm --filter dashboard dev

dev:docs: ## Start docs site in development mode
	pnpm --filter docs dev

# Build
build: ## Build all components
	@echo "Building CLI..."
	@make -C cli build
	@echo "Building Node.js packages..."
	@pnpm -r build

build:cli: ## Build CLI only
	make -C cli build

build:api: ## Build API only
	pnpm --filter api build

build:dashboard: ## Build dashboard only
	pnpm --filter dashboard build

build:docs: ## Build docs site only
	pnpm --filter docs build

build-all: build:cli ## Build CLI for all platforms
	make -C cli build-all

# Testing
test: ## Run all tests
	@echo "Running CLI tests..."
	@make -C cli test
	@echo "Running Node.js tests..."
	@pnpm -r test

test:cli: ## Run CLI tests only
	make -C cli test

# Linting
lint: ## Run all linters
	@echo "Linting CLI..."
	@make -C cli lint
	@echo "Linting Node.js packages..."
	@pnpm -r lint

# Docker
docker-build: build:cli ## Build all Docker images
	@echo "Building CLI binary for Docker..."
	@make -C cli build
	@echo "Copying CLI binary for Docker build..."
	@cp cli/reconify cli/reconify 2>/dev/null || true
	@echo "Building Docker images..."
	@docker-compose build

docker-up: ## Start all Docker services
	docker-compose up -d

docker-down: ## Stop all Docker services
	docker-compose down

docker-logs: ## View Docker logs
	docker-compose logs -f

# Installation
install: build:cli ## Install CLI globally
	make -C cli install

# Clean
clean: ## Clean all build artifacts
	@echo "Cleaning CLI..."
	@make -C cli clean
	@echo "Cleaning Node.js packages..."
	@pnpm -r clean
	@echo "Removing Docker volumes..."
	@docker-compose down -v 2>/dev/null || true

.DEFAULT_GOAL := help
