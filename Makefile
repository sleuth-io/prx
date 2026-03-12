.PHONY: help format lint check-types prepush test run clean serve serve-bg dev-frontend dev-frontend-bg install-frontend build-frontend

.DEFAULT_GOAL := help

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

format: ## Format Python code
	@echo "Formatting..."
	@uv run ruff format
	@uv run ruff check --fix src/

lint: ## Run linters
	@echo "Linting..."
	@uv run ruff check src/

check-types: ## Run type checking
	@echo "Type checking..."
	@uv run ty check src/

prepush: format lint check-types ## Run before pushing (format, lint, typecheck)

test: ## Run tests
	@echo "Running tests..."
	@uv run pytest

run: ## Run reviews against the current repo
	@uv run reviews

serve: ## Start the web UI server (foreground)
	@uv run reviews serve

serve-bg: ## Start the web UI server in background (logs to /tmp/reviews-server.log)
	@uv run reviews serve > /tmp/reviews-server.log 2>&1 &
	@echo "Server started (PID $$!), logs at /tmp/reviews-server.log"

install-frontend: ## Install frontend dependencies
	@cd frontend && npm install

build-frontend: ## Build frontend for production
	@cd frontend && npm run build

dev-frontend: ## Start Vite dev server (foreground)
	@cd frontend && npm run dev

dev-frontend-bg: ## Start Vite dev server in background (logs to /tmp/reviews-frontend.log)
	@cd frontend && npm run dev > /tmp/reviews-frontend.log 2>&1 &
	@echo "Frontend dev server started, logs at /tmp/reviews-frontend.log"

clean: ## Clean build artifacts
	@rm -rf dist/ .pytest_cache/ .ruff_cache/
	@find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
