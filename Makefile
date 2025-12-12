.PHONY: all build test test-race test-short vet staticcheck lint stress-test clean help

# Default target
all: lint test

# Build all packages
build:
	@echo "Building..."
	go build ./internal/...
	go build ./cmd/...

# Run tests
test:
	@echo "Running tests..."
	go test -v -timeout=10m ./internal/...

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	go test -race -timeout=30m ./internal/...

# Run short tests only
test-short:
	@echo "Running short tests..."
	go test -short -timeout=5m ./internal/...

# Run go vet
vet:
	@echo "Running go vet..."
	go vet ./internal/...

# Run staticcheck
staticcheck:
	@echo "Running staticcheck..."
	@which staticcheck > /dev/null || (echo "Installing staticcheck..." && go install honnef.co/go/tools/cmd/staticcheck@latest)
	staticcheck ./internal/...

# Run all linters
lint: vet staticcheck
	@echo "All linters passed!"

# Run stress tests
stress-test:
	@echo "Running stress tests..."
	./scripts/stress-test.sh

# Run quick verification (what CI should run)
ci: lint test-race
	@echo "CI checks passed!"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	go clean -cache -testcache
	rm -f ./cmd/*/fuse-stream-mvp

# Show help
help:
	@echo "Available targets:"
	@echo "  all          - Run linters and tests (default)"
	@echo "  build        - Build all packages"
	@echo "  test         - Run all tests"
	@echo "  test-race    - Run tests with race detector"
	@echo "  test-short   - Run short tests only"
	@echo "  vet          - Run go vet"
	@echo "  staticcheck  - Run staticcheck"
	@echo "  lint         - Run all linters (vet + staticcheck)"
	@echo "  stress-test  - Run stress test harness"
	@echo "  ci           - Run CI checks (lint + test-race)"
	@echo "  clean        - Clean build artifacts"
	@echo "  help         - Show this help message"
