# prx Reference Guide

## Scenes

| Scene | Purpose | Enter | Exit |
|-------|---------|-------|------|
| **Conversation** | Primary chat + review interface | Default | — |
| **Diff Overlay** | Full-screen diff viewer with comments | `ctrl+d` or `/diff` | `q` or `esc` |
| **Bulk Approve** | Approve multiple low-risk PRs at once | `ctrl+b` or `/bulk` | `n` or `esc` |

## Keyboard Shortcuts

### Conversation Scene (Primary)

| Key | Action |
|-----|--------|
| `enter` | Send chat message or run slash command |
| `esc` | Cancel streaming response, or clear input |
| `ctrl+d` | Open diff overlay |
| `ctrl+b` | Open bulk approve |
| `ctrl+n` | Next PR |
| `ctrl+p` | Previous PR |
| `ctrl+r` | Refresh current PR data from GitHub |
| `ctrl+a` | Toggle showing all merged PRs |
| `ctrl+q` | Quit prx |

### Diff Overlay

| Key | Action |
|-----|--------|
| `j` / `k` / `up` / `down` | Scroll |
| `]` / `[` | Jump to next/previous file |
| `}` / `{` | Jump to next/previous expanded hunk (skips collapsed) |
| `left` / `right` | Collapse/expand current item (expanding a comment also expands its parent hunk) |
| `shift+left` / `<` | Collapse one level (comments → groups → hunks → files) |
| `shift+right` / `>` | Expand one level (files → hunks → groups → comments) |
| `?` | Quote current line/hunk and return to chat |
| `c` | Open comment modal on current line |
| `q` / `esc` / `ctrl+q` | Return to conversation |

### Comment Modal (in diff overlay)

| Key | Action |
|-----|--------|
| `enter` / `ctrl+s` | Submit comment |
| `alt+enter` | New line in comment |
| `esc` | Cancel and close |

### Bulk Approve

| Key | Action |
|-----|--------|
| `j` / `k` / `up` / `down` | Move cursor |
| `space` / `x` | Toggle selection |
| `shift+a` | Select all / deselect all |
| `a` | Approve all selected PRs |
| `enter` | View selected PR in conversation |
| `n` / `esc` | Return to conversation |
| `q` / `ctrl+q` | Quit prx |

## Slash Commands

Type in the conversation input bar and press enter:

| Command | Action |
|---------|--------|
| `/next` | Go to next PR |
| `/prev` | Go to previous PR |
| `/diff` | Open diff overlay |
| `/approve` | Approve the PR (with confirmation) |
| `/merge` | Merge the PR (with confirmation, own PRs only) |
| `/reject` | Request changes (with confirmation) |
| `/flag` | Flag a merged PR with thumbs-down (post-merge only) |
| `/comment` | Open diff overlay with comment modal |
| `/bulk` | Open bulk approve screen |
| `/toggle-merged` | Toggle showing all merged PRs |
| `/refresh` | Refresh PR data and check for new PRs |
| `/quit` | Quit prx |

Any installed skill can also be activated as a slash command: `/skill-name`.

## Confirmation & Permission Dialogs

When an action requires confirmation (approve, merge, etc.), a banner appears:
- `y` — Confirm
- `n` / `esc` — Cancel

When Claude requests permission for a mutating action:
- `y` — Allow
- `n` / `esc` — Deny

## Post-Merge Review

prx fetches recently merged PRs authored by others that you haven't reviewed. These appear in the PR list alongside open PRs, marked as merged.

- `/approve` on a merged PR adds a thumbs-up reaction (post-merge acknowledgment)
- `/flag` on a merged PR adds a thumbs-down reaction (flags it for discussion)
- `ctrl+a` or `/toggle-merged` shows/hides merged PRs you've already reviewed

Post-merge PRs also appear in the bulk approve screen when they score below the approve threshold.

## Incremental Review

prx tracks what you've seen and highlights what's new on return visits.

**How it works:**
- When you exit the diff overlay, prx snapshots all hunk content hashes and comment IDs
- On your next visit, if anything changed, the diff opens in **incremental mode**
- Snapshots are also saved when you take review actions (approve, comment, request-changes)
- State is persisted to `~/.cache/prx/review-state.json` (survives app restarts, 30-day eviction)

**What you see in incremental mode:**
- **New hunks** — expanded with green "(new)" badge
- **Unchanged hunks** — collapsed (dim style)
- **Hunks with new inline comments** — expanded for code context, comment has green "(new)" badge
- **New top-level comments** — comment group auto-expanded, comment has green "(new)" badge
- **Edited comments** — blue "(edited)" badge
- **File-level badges** — files with new comments show "(N new comments)" on the header
- **Scoring panel** — green "New" summary line (e.g. "2 new hunks, 3 new comments")

**Rebase resilience:** Content-hash matching means rebases that don't change code are invisible. A pure rebase produces identical hashes, so everything shows as unchanged.

**Reply threading:** Inline comment replies are indented with a ↳ prefix to distinguish them from root comments.

**Navigation:** `{`/`}` skip collapsed hunks, creating a natural review queue of only new content. Use `>` to expand everything back to the full diff.

**Post-merge PRs:** Reviewed merged PRs that receive new comments resurface in the PR list automatically.

## Assessment Panel

Each PR's assessment panel shows:
- Risk scores for each configured criterion (1-5 scale)
- Weighted verdict: APPROVE, REVIEW, or INVESTIGATE
- Review notes — a plain-language summary of what to focus on
- Key hunk preview — a snippet of the riskiest code change, with the file and line reference
- "New" line — when incremental state shows changes since last review

Inline images in PR descriptions are rendered as thumbnails in the terminal.

## Scoring

Each PR is scored on configurable criteria (default 5):

| Criterion | What it measures |
|-----------|-----------------|
| **Blast Radius** | How much of the system could break? Business impact > line count. |
| **Intent Clarity** | Is the WHY clear? Vague descriptions or unclear mappings score high. |
| **Irreversibility** | How hard is this to undo? Migrations, API changes, deleted logic score high. |
| **Domain Knowledge** | How much tribal knowledge is needed to review safely? |
| **Novelty** | New patterns, dependencies, or unfamiliar territory? |

Scores (1-5) are weighted and averaged into a verdict:

| Verdict | Meaning |
|---------|---------|
| **APPROVE** | Below `approve_below` threshold — safe to approve quickly |
| **REVIEW** | Between thresholds — needs normal review |
| **INVESTIGATE** | Above `review_above` threshold — needs careful attention |

## Configuration

Config file: `~/.config/prx/config.toml`

```toml
[review]
model = "sonnet"          # Claude model: "sonnet", "opus", "haiku"
merge_method = "merge"    # "merge", "squash", or "rebase"

[thresholds]
approve_below = 2.0       # PRs below this → APPROVE verdict
review_above = 3.5        # PRs above this → INVESTIGATE verdict

[[criteria]]
name = "blast_radius"
label = "Blast"
description = "How much of the system could break?"
weight = 1.0
```

All configuration is modifiable through chat — ask Claude to change the model, adjust thresholds, add/remove criteria, or change merge method.

### Custom Criteria

Each criterion needs:
- `name`: unique identifier (snake_case)
- `label`: short display label
- `description`: detailed description used in the AI scoring prompt
- `weight`: weighting factor (> 0, higher = more influence on overall score)

## Skills

Skills are specialized instruction sets that Claude can load on demand.

### Custom Skills

Create skills in `~/.config/prx/skills/`. Each skill is a directory with a `SKILL.md`:

```
~/.config/prx/skills/
  my-skill/
    SKILL.md
    reference/
      some-doc.md
```

The `SKILL.md` uses YAML frontmatter:

```markdown
---
name: my-skill
description: "What this skill helps with"
---

Instructions for Claude when this skill is activated.
```

Custom skills override built-in skills with the same name.
