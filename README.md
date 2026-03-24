<div align="center">

# prx

### AI-powered PR triage for people who review code.asdasdfasdf

<br>

[![Downloads](https://img.shields.io/github/downloads/sleuth-io/prx/total?color=3B82F6)](https://github.com/sleuth-io/prx/releases)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-10B981.svg)](https://github.com/sleuth-io/prx/pulls)

</div>

![Demo](docs/demo.gif)

## What is prx?

prx is a terminal UI that helps you prioritize code review. It uses AI to score each PR on how much *human judgment* it requires - not to review the code for you, but to tell you where your time matters most.

**For each PR, prx shows:**
- Risk scores across configurable criteria (blast radius, intent clarity, irreversibility, domain knowledge, novelty)
- A weighted verdict: APPROVE, REVIEW, or INVESTIGATE
- Per-hunk annotations - trivial hunks auto-collapse so you see only what needs your brain
- Inline comments, review history, and CI status

**Chat-first interface:**
- The primary interface is a conversation — PR assessment is scrollback, actions are chat
- Ask questions about the code, the PR, or the risk assessment
- Take PR actions by asking ("approve this", "request changes", "add a comment saying...")
- Tune the scoring to your team: "treat test-only changes as low blast radius" or "I don't care about novelty for this repo" — Claude updates its criteria on the fly
- Slash commands for quick actions: `/approve`, `/merge`, `/diff`, `/bulk`

**Diff overlay (`ctrl+d`):**
- Full-screen diff with per-hunk risk annotations
- Navigate files (`]/[`), hunks (`}/{`), collapse/expand (`left/right`)
- Add inline or global comments (`c`), quote code into chat (`?`)

**Bulk approve (`/bulk`):**
- Approve multiple low-risk PRs in one pass

**Skills — self-aware help & extensibility:**
- Ask "how do I configure scoring?" or "what shortcuts are available?" — Claude loads the built-in user guide skill and answers from it
- Type `/user-guide` in chat to activate the built-in skill directly
- Add your own skills in `~/.config/prx/skills/` — each skill is a directory with a `SKILL.md` (YAML frontmatter for name + description, markdown body for instructions)

**Coming next: Personalized automated actions**
- Auto-approve trivial PRs
- Set up your own scoring criteria and automate from that
- Customize auto-merge rules

## Install

**Quick install (macOS/Linux):**

```bash
curl -fsSL https://raw.githubusercontent.com/sleuth-io/prx/main/install.sh | bash
```

Or download the latest binary manually from [Releases](https://github.com/sleuth-io/prx/releases).

**From source:**

```bash
git clone https://github.com/sleuth-io/prx.git
cd prx
make install
```

### Prerequisites

- [GitHub CLI](https://cli.github.com/) (`gh`) — authenticated
- [Claude Code](https://claude.ai/download) (`claude`) — for AI assessment

## Usage

```bash
# Run in the current repo
prx

# Run against a different repo
prx /path/to/repo
```

### Keyboard shortcuts

**Conversation (primary screen):**

| Key           | Action |
|---------------|--------|
| `enter`       | Send chat message or run slash command |
| `esc`         | Cancel streaming response / clear input |
| `ctrl+d`      | Open diff overlay |
| `ctrl+n`      | Next PR |
| `ctrl+p`      | Previous PR |
| `ctrl+r`      | Refresh PR data |
| `ctrl+q`      | Quit |

**Diff overlay (`ctrl+d`):**

| Key                 | Action |
|---------------------|--------|
| `j/k/up/down`       | Scroll |
| `]/[`               | Next/previous file |
| `}/{`               | Next/previous hunk |
| `left/right`        | Collapse/expand current item |
| `shift+left` / `<`  | Collapse all items |
| `shift+right` / `>` | Expand all items |
| `?`                 | Quote code into chat |
| `c`                 | Comment (global or inline) |
| `q` / `esc`         | Return to conversation |

**Slash commands** — type in the input bar:

`/approve` `/merge` `/reject` `/comment` `/diff` `/bulk` `/next` `/prev` `/refresh` `/quit`

### Bulk approve

Type `/bulk` to open the bulk approve screen. prx lists all PRs below your configured `approve_below` risk threshold and lets you select which to approve. This lets you clear a queue of trivial PRs in one pass before focusing on the ones that need real attention.

For the full reference — all shortcuts, scoring details, configuration format, and custom skills — see the [User Guide](internal/skills/builtins/user-guide/reference/guide.md). This guide is also available in-app: type `/user-guide` in the chat or ask "how do I configure scoring?"

## License

See LICENSE file for details.

---

<details>
<summary>Development</summary>

### Building from Source

```bash
make init           # Download dependencies
make build          # Build binary
make install        # Install to ~/.local/bin
```

### Testing

```bash
make test           # Run tests
make lint           # Run linter
make prepush        # Format, lint, test, build
```

### Releases

Tag and push to trigger automated release via GoReleaser:

```bash
git tag v0.1.0
git push origin v0.1.0
```

### Demo GIF

Requires [vhs](https://github.com/charmbracelet/vhs):

```bash
make demo
```

</details>
