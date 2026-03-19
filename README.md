<div align="center">

# prx

### AI-powered PR triage for people who review code.

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

**From the TUI you can:**
- Approve, request changes, or merge PRs
- Add comments (global or inline on specific lines)
- Chat with Claude about the code in context
- Navigate between PRs, files, and hunks with keyboard shortcuts

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

| Key             | Action |
|-----------------|--------|
| `tab`           | Cycle between panels |
| `j/k/up/down`   | Scroll up/down |
| `n/p`           | Next/previous PR |
| `]/[`           | Next/previous file |
| `}/{`           | Next/previous hunk |
| `left/right` | Collapse/expand |
| `a`             | Approve PR |
| `m`             | Merge PR (own PRs) |
| `r`             | Request changes |
| `c`             | Comment (global or inline) |
| `?`             | Open AI chat |
| `q`             | Quit |


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
