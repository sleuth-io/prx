# prx — Claude Code Rules

## Module & repo

- Module: `github.com/sleuth-io/prx`
- GitHub repo: `sleuth-io/prx`
- Entry point: `cmd/prx/main.go` — accepts optional `<path>` arg to set repo directory

## Architecture

### Key types

- `*app.App` — shared context (repo name, repo dir, current user, config, cache). Always pass this; never pass individual fields from it
- `*github.PR` — fully hydrated PR: diff, checks, reviews, inline comments, comments, requested reviewers
- `ai.Assessment` — Claude's risk scores (5 factors × `{score int, reason string}`) + `RiskSummary` + `ReviewNotes`

### Package layout

| Package | Responsibility |
|---|---|
| `internal/app` | Bootstrap: parallel init of repo, user, config |
| `internal/github` | All GitHub I/O via `gh` CLI; no direct API auth |
| `internal/ai` | Risk assessment via `claude -p` subprocess |
| `internal/tui` | Bubble Tea UI: `Model` (list) + `DiffView` (diff panel) |
| `internal/cache` | JSON assessment cache keyed by `sha256(diff + reviews)` |
| `internal/config` | TOML config with defaults; path: `~/.config/prx/config.toml` |
| `internal/logger` | File logger to `~/.cache/prx/prx.log` |

## Coding conventions

### GitHub calls
- Always use `gh` CLI (`os/exec`), never direct REST/GraphQL with tokens
- Run independent calls in parallel with `sync.WaitGroup` — see `FetchPRDetails` for the pattern
- Return typed structs, not raw `map[string]any` across package boundaries

### Bubble Tea
- Use `tea.Batch` to dispatch multiple commands in one `Update` return
- Messages are structs; suffix with `Msg` (e.g. `prScoredMsg`)
- Long I/O always runs in a `tea.Cmd` goroutine — never block `Update`
- `Model` embeds `DiffView` by value, not pointer; update via `m.diffView, cmd = ...`

### AI assessment
- Invocation: `claude -p <prompt> --output-format json --allowedTools Read,Bash,Glob`
- Run with `cmd.Dir = repoDir` so Claude can explore the codebase
- Output is a JSON envelope `{result: string, is_error: bool}`; extract inner JSON with `extractJSON()`
- Cache key: `cache.Key(repo, prNumber, diff, reviewsText(pr))` — **checks intentionally excluded** from cache key (check state shouldn't bust cache)

### Own PR vs others
- Detect via `card.PR.Author == app.CurrentUser`
- Own PRs: show `m` (merge), hide approve/request-changes
- Others' PRs: show `a` (approve), `r` (request-changes), hide merge

### DiffView collapsible items
- Files and comments are unified as `collapsible` items tracked by viewport line index
- `collapsibleAtOffset()` finds current item by `viewport.YOffset`; call fresh every keypress
- After `rebuildViewport()`, restore position with `collapsibleLineIdx()` to find new line index
- `left`/`right` arrows collapse/expand current item; `[`/`]` jump between files (when diff focused)

## Configuration

Config file: `~/.config/prx/config.toml`

```toml
[review]
model = "sonnet"          # "sonnet", "opus", "haiku"
merge_method = "merge"    # "merge", "squash", or "rebase"

[thresholds]
approve_below = 2.0
review_above = 3.5

[[criteria]]
name = "blast_radius"
label = "Blast"
description = "How much of the system could break?"
weight = 1.0
```

## Build & run

Always use `make` targets, never raw `go` commands directly.

```bash
make build    # go build ./... → outputs to dist/prx
make run      # go run ./cmd/prx [path]
make test     # go test ./...
make lint     # golangci-lint run
make prepush  # format + lint + test + build — run before every commit
```

- Built binary is at `dist/prx` — always use this, never the `prx` binary in the repo root
- To test manually: `dist/prx /home/mrdon/dev/pulse` (or any repo path)
- The first screen is Bulk Approve — type `n` to skip to the PR list
- Log file: `~/.cache/prx/prx.log`

## TUI testing

Use tmux to verify TUI behavior:

```bash
tmux new-session -d -s tui-test -x 220 -y 50
tmux send-keys -t tui-test "dist/prx /home/mrdon/dev/pulse" Enter
# wait for load, then skip bulk approve
tmux send-keys -t tui-test "n" ""
tmux capture-pane -t tui-test -p
```

## Performance goals

- First PR visible in **< 2 seconds** (list fetch + first PR details + spinner while scoring)
- All PR details fetched in parallel (diff, checks, reviews, inline comments, comments via 5 concurrent goroutines)
- Assessment cached by content hash; cache survives restarts at `~/.cache/prx/assessments.json`
