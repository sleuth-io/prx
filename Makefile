.PHONY: help build install test lint format clean prepush postpull

# Default target
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

# Build variables
BINARY_NAME=reviews
MAIN_PATH=./cmd/reviews
BUILD_DIR=./dist
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Date=$(DATE)"

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(BINARY_NAME)"

install: build ## Install binary to ~/.local/bin
	@echo "Installing $(BINARY_NAME)..."
	@mkdir -p $$HOME/.local/bin
	@cp $(BUILD_DIR)/$(BINARY_NAME) $$HOME/.local/bin/
	@echo "$(BINARY_NAME) installed to $$HOME/.local/bin/$(BINARY_NAME)"

test: ## Run tests
	@echo "Running tests..."
	@OUTPUT=$$(go test -race -cover ./... 2>&1 | grep -v 'no such tool "covdata"'); \
	RESULT=$$?; \
	if echo "$$OUTPUT" | grep -q "^FAIL"; then \
		echo "$$OUTPUT"; \
		exit 1; \
	else \
		PASSED=$$(echo "$$OUTPUT" | grep -c "^ok"); \
		echo "All $$PASSED packages passed"; \
	fi

lint: ## Run linters (requires golangci-lint)
	@echo "Running linters..."
	@GOBIN=$$(go env GOPATH)/bin; \
	if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run; \
	elif [ -x "$$GOBIN/golangci-lint" ]; then \
		"$$GOBIN/golangci-lint" run; \
	else \
		echo "golangci-lint not found. Run 'make postpull' to install." && exit 1; \
	fi

format: ## Format code
	@echo "Formatting code..."
	@gofmt -s -w .
	@go mod tidy

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@go clean

run: build ## Build and run the binary
	@$(BUILD_DIR)/$(BINARY_NAME)

init: ## Initialize development environment
	@echo "Initializing development environment..."
	@echo "Installing golangci-lint..."
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$$(go env GOPATH)/bin" v2.8.0
	@echo "Downloading dependencies..."
	@go mod download
	@echo "Development environment initialized"

prepush: format lint test build ## Run before pushing (format, lint, test, build)

postpull: init ## Run after pulling (install tools and download dependencies)
