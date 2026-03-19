.PHONY: help run build install test lint format clean release demo prepush postpull

BINARY_NAME=prx
MAIN_PATH=./cmd/prx
BUILD_DIR=./dist
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X github.com/sleuth-io/prx/internal/buildinfo.Version=$(VERSION) -X github.com/sleuth-io/prx/internal/buildinfo.Commit=$(COMMIT) -X github.com/sleuth-io/prx/internal/buildinfo.Date=$(DATE)"

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the binary
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

run: build ## Build and run
	@$(BUILD_DIR)/$(BINARY_NAME)

install: build ## Install to ~/.local/bin
	@mkdir -p $$HOME/.local/bin
	@cp $(BUILD_DIR)/$(BINARY_NAME) $$HOME/.local/bin/
	@echo "Installed to $$HOME/.local/bin/$(BINARY_NAME)"

test: ## Run tests
	@go test ./...

lint: ## Run linter
	@golangci-lint run

format: ## Format code
	@gofmt -s -w .
	@go mod tidy

clean: ## Clean build artifacts
	@rm -rf $(BUILD_DIR)

release: ## Create release with goreleaser
	@goreleaser release --clean

init: ## Install dependencies
	@go mod tidy

demo: build ## Generate demo GIF (requires vhs)
	@which vhs > /dev/null || (echo "vhs not found. Install from https://github.com/charmbracelet/vhs" && exit 1)
	@PATH="$(CURDIR)/$(BUILD_DIR):$$PATH" vhs docs/demo.tape
	@echo "Generated: docs/demo.gif"

prepush: format lint test build ## Run before pushing

postpull: init ## Run after pulling
