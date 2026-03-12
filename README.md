# Reviews

AI-powered PR risk triage tool. Scores open pull requests by blast radius using Claude, then presents them in a swipe-style web UI for fast human review.

## How it works

1. Fetches open PRs from a GitHub repo (via `gh` CLI)
2. Claude assesses each PR using codebase exploration tools (read files, search code, list files)
3. Scores five risk factors: blast radius, test coverage, sensitivity, complexity, scope
4. Presents cards sorted by risk in a full-screen TikTok-style UI

## Quick start

```bash
# Install dependencies
uv sync
cd frontend && npm install && cd ..

# Start backend (auto-detects repo from cwd, or pass explicitly)
cd ~/dev/your-repo && uv run --project ~/dev/reviews reviews serve

# Start frontend dev server (separate terminal)
make dev-frontend

# Open http://localhost:5173
```

## CLI usage

```bash
# Score PRs and print a table
cd ~/dev/your-repo && uv run --project ~/dev/reviews reviews triage

# Or specify a repo explicitly
uv run reviews triage --repo sleuth-io/pulse
```

## Requirements

- Python 3.12+
- Node.js 18+
- `gh` CLI (authenticated)
- `ANTHROPIC_API_KEY` environment variable

## Architecture

- **Backend**: FastAPI + SQLite (via SQLModel) + pydantic-ai for Claude agent
- **Frontend**: Vue 3 + TypeScript + Vite
- **Scoring**: pydantic-ai agent with tools to explore the local codebase
- **Actions**: Approve/reject via GitHub review API (`gh pr review`)
