package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// CommandScope defines when a command is available.
type CommandScope int

const (
	ScopeGlobal CommandScope = iota // always available
	ScopePR                         // only when a PR is selected
)

// Command defines a user action that can be triggered via slash command,
// keyboard shortcut, or MCP tool call.
type Command struct {
	Name        string       // slash command name (e.g. "approve")
	Description string       // shown in help/autocomplete
	KeyBinding  string       // optional ctrl combo (e.g. "ctrl+d"), empty = no binding
	Scope       CommandScope // controls autocomplete visibility
	Run         func(m *Model) (Model, tea.Cmd)
}

// commands returns all registered commands.
func commands() []Command {
	return []Command{
		{
			Name:        "next",
			Description: "Go to next PR",
			KeyBinding:  "ctrl+n",
			Scope:       ScopeGlobal,
			Run: func(m *Model) (Model, tea.Cmd) {
				m.navigatePR(1)
				return *m, nil
			},
		},
		{
			Name:        "prev",
			Description: "Go to previous PR",
			KeyBinding:  "ctrl+p",
			Scope:       ScopeGlobal,
			Run: func(m *Model) (Model, tea.Cmd) {
				m.navigatePR(-1)
				return *m, nil
			},
		},
		{
			Name:        "diff",
			Description: "Toggle diff overlay",
			KeyBinding:  "ctrl+d",
			Scope:       ScopePR,
			Run: func(m *Model) (Model, tea.Cmd) {
				if m.overlay == overlayDiff {
					m.overlay = overlayNone
					m.diffView.Focused = false
					m.resizeLayout()
					m.buildScrollback()
					return *m, m.input.Focus()
				}
				m.overlay = overlayDiff
				m.diffView.Focused = true
				m.input.Blur()
				m.resizeLayout()
				return *m, nil
			},
		},
		{
			Name:        "approve",
			Description: "Approve current PR",
			Scope:       ScopePR,
			Run: func(m *Model) (Model, tea.Cmd) {
				card := m.currentCard()
				if card == nil || card.Scoring || m.isOwnPR(card) {
					return *m, nil
				}
				repo, num := m.app.Repo, card.PR.Number
				m.confirm = &confirmDialog{
					description:  fmt.Sprintf("Approve PR #%d?", num),
					actionStatus: "Approving…",
					cmd:          approveCmd(repo, num),
				}
				return *m, nil
			},
		},
		{
			Name:        "merge",
			Description: "Merge current PR",
			Scope:       ScopePR,
			Run: func(m *Model) (Model, tea.Cmd) {
				card := m.currentCard()
				if card == nil || card.Scoring || !m.isOwnPR(card) {
					return *m, nil
				}
				if reason := card.PR.MergeBlockReason(); reason != "" {
					m.actionStatus = fmt.Sprintf("Cannot merge: %s", reason)
					m.actionDone = true
					return *m, nil
				}
				repo, num := m.app.Repo, card.PR.Number
				method := m.app.Config.Review.MergeMethod
				desc := fmt.Sprintf("Merge PR #%d? (%s + delete branch)", num, method)
				if warn := card.PR.MergeWarnReason(); warn != "" {
					desc += fmt.Sprintf(" [warning: %s]", warn)
				}
				m.confirm = &confirmDialog{
					description:  desc,
					actionStatus: "Merging…",
					cmd:          mergeCmd(repo, num, method),
				}
				return *m, nil
			},
		},
		{
			Name:        "reject",
			Description: "Request changes on current PR",
			Scope:       ScopePR,
			Run: func(m *Model) (Model, tea.Cmd) {
				card := m.currentCard()
				if card == nil || card.Scoring || card.Assessment == nil || m.isOwnPR(card) {
					return *m, nil
				}
				repo, num, notes := m.app.Repo, card.PR.Number, card.Assessment.ReviewNotes
				m.confirm = &confirmDialog{
					description:  fmt.Sprintf("Request changes on PR #%d?", num),
					actionStatus: "Requesting changes…",
					cmd:          requestChangesCmd(repo, num, notes),
				}
				return *m, nil
			},
		},
		{
			Name:        "comment",
			Description: "Post a comment on current PR",
			Scope:       ScopePR,
			Run: func(m *Model) (Model, tea.Cmd) {
				card := m.currentCard()
				if card == nil {
					return *m, nil
				}
				m.openCommentModal(card, false, "", 0)
				return *m, m.modal.textarea.Focus()
			},
		},
		{
			Name:        "bulk",
			Description: "Open bulk approve view",
			Scope:       ScopeGlobal,
			Run: func(m *Model) (Model, tea.Cmd) {
				m.tryEnterBulkApprove()
				return *m, nil
			},
		},
		{
			Name:        "refresh",
			Description: "Refresh current PR and check for new PRs",
			KeyBinding:  "ctrl+r",
			Scope:       ScopeGlobal,
			Run: func(m *Model) (Model, tea.Cmd) {
				card := m.currentCard()
				if card == nil {
					return *m, nil
				}
				m.actionStatus = "Refreshing…"
				m.actionDone = false
				return *m, tea.Batch(refreshPRCmd(card.PR, m.app), fetchPRListCmd(m.app.Repo))
			},
		},
		{
			Name:        "quit",
			Description: "Quit prx",
			KeyBinding:  "ctrl+q",
			Scope:       ScopeGlobal,
			Run: func(m *Model) (Model, tea.Cmd) {
				return *m, m.cleanupWorktrees()
			},
		},
		{
			Name:        "exit",
			Description: "Quit prx",
			Scope:       ScopeGlobal,
			Run: func(m *Model) (Model, tea.Cmd) {
				return *m, m.cleanupWorktrees()
			},
		},
	}
}

// commandMap builds lookup tables from the command registry.
// Called once; results can be cached on Model if needed.
func commandMap() (slashMap map[string]*Command, keyMap map[string]*Command) {
	cmds := commands()
	slashMap = make(map[string]*Command, len(cmds))
	keyMap = make(map[string]*Command)
	for i := range cmds {
		c := &cmds[i]
		slashMap[c.Name] = c
		if c.KeyBinding != "" {
			keyMap[c.KeyBinding] = c
		}
	}
	return
}

// handleSlashCommand checks if the input is a slash command and executes it.
func (m *Model) handleSlashCommand() (Model, tea.Cmd, bool) {
	input := strings.TrimSpace(m.input.Value())
	if !strings.HasPrefix(input, "/") {
		return *m, nil, false
	}
	name := strings.ToLower(strings.TrimPrefix(input, "/"))
	slashCmds, _ := commandMap()
	if cmd, ok := slashCmds[name]; ok {
		m.input.Reset()
		model, teaCmd := cmd.Run(m)
		return model, teaCmd, true
	}
	return *m, nil, false
}

// handleCommandKey checks if a key matches a command's KeyBinding.
func (m *Model) handleCommandKey(key string) (Model, tea.Cmd, bool) {
	_, keyCmds := commandMap()
	if cmd, ok := keyCmds[key]; ok {
		model, teaCmd := cmd.Run(m)
		return model, teaCmd, true
	}
	return *m, nil, false
}
