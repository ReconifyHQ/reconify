# Contributing to Reconify

Thank you for your interest in contributing to Reconify! This document provides guidelines and instructions for contributing.

## Development Setup

1. **Fork and clone the repository**
   ```bash
   git clone https://github.com/your-username/reconify.git
   cd reconify
   ```

2. **Install dependencies**
   ```bash
   go mod download
   ```

3. **Build the project**
   ```bash
   make build
   ```

4. **Run the pre-PR gate**
   ```bash
   make preflight
   ```

## Code Style

- Follow standard Go formatting (`make fmt-check`)
- Run `make preflight` before submitting PRs
- Write tests for new features
- Update documentation as needed

## Pull Request Process

1. Create a feature branch from `main`
2. Make your changes
3. Ensure the full pre-PR gate passes (`make preflight`)
4. If you need to isolate failures, run `make mod-check`, `make fmt-check`, `make lint`, `make security`, `make test`, `make build`, and `make bench-smoke`
5. Update documentation if needed
6. Submit a pull request with a clear description

## Commit Messages

Use clear, descriptive commit messages:
- Start with a verb (Add, Fix, Update, etc.)
- Keep the first line under 72 characters
- Add more details in the body if needed

Example:
```
Add CSV parser with decimal precision handling

Implements the core CSV parsing logic using shopspring/decimal
to avoid floating-point precision errors when converting to
minor units.
```

## Questions?

Open an issue for discussion or reach out to the maintainers.
