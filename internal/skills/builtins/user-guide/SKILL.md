---
name: user-guide
description: "Comprehensive guide to prx features, shortcuts, and configuration. Use when the user asks about prx itself — how to use it, what shortcuts are available, or how to configure scoring."
---

# prx User Guide

prx is a conversation-first terminal UI for prioritizing code review. It uses AI to score each PR on how much *human judgment* it requires.

## How It Works

1. prx fetches your open PRs and scores each one on configurable risk criteria
2. You see the assessment as scrollback content in a chat interface
3. You interact via chat messages, slash commands, or keyboard shortcuts
4. A diff overlay and bulk approve screen are available as secondary views

## Scenes

- **Conversation** (primary): Chat + PR assessment. Type messages or slash commands.
- **Diff Overlay** (`ctrl+d` or `/diff`): Full-screen diff with file/hunk navigation and inline comments.
- **Bulk Approve** (`/bulk`): Select and approve multiple low-risk PRs at once.

## Common Questions

**How do I navigate between PRs?**
`ctrl+n` next, `ctrl+p` previous. Or type `/next` and `/prev`.

**How do I see the diff?**
`ctrl+d` opens the diff overlay. `q` or `esc` returns to conversation. On return visits, the diff automatically shows only what's new — unchanged hunks are collapsed.

**What is incremental review?**
prx tracks what you've seen by snapshotting hunk and comment content hashes when you exit the diff. On your next visit (after a refresh or app restart), if anything changed, the diff opens in incremental mode: new hunks are expanded with a green "new" badge, unchanged hunks collapse, and new comments get a green badge. Edited comments get a blue "edited" badge. The scoring panel shows a green "New" summary line. Use `>` to expand everything back to the full diff, or `{`/`}` to jump between only the expanded (new) hunks.

**How do I approve/merge/reject?**
Type `/approve`, `/merge`, or `/reject`. Or just ask in chat: "approve this PR". All actions require confirmation.

**How do I add a comment?**
Type `/comment` to open the comment modal in the diff view. Or press `c` while in the diff overlay to comment on the current line.

**How do I change the scoring model?**
Ask in chat: "change the model to opus". Or use the `set_model` config tool.

**How do I adjust what gets auto-approved?**
Ask in chat: "set approve threshold to 2.5" or "set review threshold to 4.0". Config file: `~/.config/prx/config.toml` under `[thresholds]`.

**How do I customize scoring criteria?**
Ask in chat: "add a criterion for security" or "remove the novelty criterion" or "increase blast radius weight to 2.0". Criteria live in `~/.config/prx/config.toml` under `[[criteria]]`.

**How do I change the merge method?**
Ask in chat: "change merge method to squash". Options: `merge`, `squash`, `rebase`.

**What do the verdicts mean?**
- **APPROVE**: score below `approve_below` (default 2.0) — safe to approve quickly
- **REVIEW**: between thresholds — needs normal review
- **INVESTIGATE**: score above `review_above` (default 3.5) — needs careful attention

**How do I add custom skills?**
Create a directory in `~/.config/prx/skills/` with a `SKILL.md` file. See the reference guide for the format.

## Full Reference

For the complete keyboard shortcut tables, all slash commands, scoring criteria details, configuration format, and custom skills documentation, read the bundled resource `reference/guide.md`.
