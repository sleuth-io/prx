.PHONY: help format lint check-types prepush test run clean

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

clean: ## Clean build artifacts
	@rm -rf dist/ .pytest_cache/ .ruff_cache/
	@find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
