.PHONY: help run build install test lint format clean prepush postpull

BINARY_NAME=prx
MAIN_PATH=./cmd/prx
BUILD_DIR=./dist

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the binary
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

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

init: ## Install dependencies
	@go mod tidy

prepush: format test build ## Run before pushing

postpull: init ## Run after pulling
