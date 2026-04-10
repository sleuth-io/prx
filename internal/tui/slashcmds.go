package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/cache"
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
	Run         func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool)
}

// commands returns all registered commands.
func commands() []Command {
	return []Command{
		{
			Name:        "next",
			Description: "Go to next PR",
			KeyBinding:  "ctrl+n",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				m.navigatePR(1, s)
				return s, nil, true
			},
		},
		{
			Name:        "prev",
			Description: "Go to previous PR",
			KeyBinding:  "ctrl+p",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				m.navigatePR(-1, s)
				return s, nil, true
			},
		},
		{
			Name:        "diff",
			Description: "Toggle diff overlay",
			KeyBinding:  "ctrl+d",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				s.input.Blur()
				m.diffView.Focused = true
				ds := newDiffOverlayScene(s, m.width, m.height)
				// In incremental mode, skip key hunk scroll — user navigates with }
				if !m.diffView.IncrementalMode() {
					if card := m.currentCard(); card != nil && card.Assessment != nil && card.Assessment.KeyHunk != nil {
						kh := card.Assessment.KeyHunk
						m.diffView.ScrollToHunk(kh.File, kh.StartLine)
					}
				}
				return ds, nil, true
			},
		},
		{
			Name:        "approve",
			Description: "Approve current PR",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil || card.Scoring || m.isOwnPR(card) {
					return s, nil, true
				}
				repo, num := card.Ctx.Repo, card.PR.Number
				if card.PostMerge {
					s.confirm = &confirmDialog{
						description:  fmt.Sprintf("Approve (post-merge) PR #%d?", num),
						actionStatus: "Approving post-merge…",
						cmd:          addReactionCmd(repo, num, "+1", actionPostMergeApprove, m.app.CurrentUser),
					}
					return s, nil, true
				}
				s.confirm = &confirmDialog{
					description:  fmt.Sprintf("Approve PR #%d?", num),
					actionStatus: "Approving…",
					cmd:          approveCmd(repo, num, args),
				}
				return s, nil, true
			},
		},
		{
			Name:        "flag",
			Description: "Flag a merged PR (thumbs down)",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil || !card.PostMerge {
					return s, nil, true
				}
				repo, num := card.Ctx.Repo, card.PR.Number
				s.confirm = &confirmDialog{
					description:  fmt.Sprintf("Flag PR #%d?", num),
					actionStatus: "Flagging…",
					cmd:          addReactionCmd(repo, num, "-1", actionPostMergeFlag, m.app.CurrentUser),
				}
				return s, nil, true
			},
		},
		{
			Name:        "toggle-merged",
			Description: "Toggle showing all merged PRs",
			KeyBinding:  "ctrl+a",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				m.showAllMerged = !m.showAllMerged
				if m.showAllMerged {
					s.actionStatus = "Showing all merged PRs"
				} else {
					s.actionStatus = "Hiding reviewed merged PRs"
				}
				// If current card is now hidden, move to nearest visible
				// (may enter bulk approve if none remain).
				if card := m.currentCard(); card != nil && !m.isCardVisible(card) {
					m.skipToVisibleCard()
					m.loadCurrentDiff()
				}
				s.BuildScrollback(m)
				return m.scene, nil, true
			},
		},
		{
			Name:        "merge",
			Description: "Merge current PR",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil || card.Scoring || !m.isOwnPR(card) {
					return s, nil, true
				}
				if reason := card.PR.MergeBlockReason(); reason != "" {
					s.actionStatus = fmt.Sprintf("Cannot merge: %s", reason)
					s.actionDone = true
					return s, nil, true
				}
				repo, num := card.Ctx.Repo, card.PR.Number
				method := m.app.Config.Review.MergeMethod
				desc := fmt.Sprintf("Merge PR #%d? (%s + delete branch)", num, method)
				if warn := card.PR.MergeWarnReason(); warn != "" {
					desc += fmt.Sprintf(" [warning: %s]", warn)
				}
				s.confirm = &confirmDialog{
					description:  desc,
					actionStatus: "Merging…",
					cmd:          mergeCmd(repo, num, method),
				}
				return s, nil, true
			},
		},
		{
			Name:        "reject",
			Description: "Request changes on current PR",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil || card.Scoring || m.isOwnPR(card) {
					return s, nil, true
				}
				return runCommentOrReject(s, m, card, args, true)
			},
		},
		{
			Name:        "comment",
			Description: "Post a comment on current PR",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil {
					return s, nil, true
				}
				return runCommentOrReject(s, m, card, args, false)
			},
		},
		{
			Name:        "skip",
			Description: "Skip current PR (hide until un-skipped)",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil {
					return s, nil, true
				}
				key := cache.SkipKey(card.Ctx.Repo, card.PR.Number)
				m.app.SkipStore.Skip(key)
				s.actionStatus = fmt.Sprintf("Skipped PR #%d", card.PR.Number)
				s.actionDone = true
				m.skipToVisibleCard()
				m.loadCurrentDiff()
				s.BuildScrollback(m)
				return m.scene, nil, true
			},
		},
		{
			Name:        "unskip",
			Description: "Un-skip current PR",
			Scope:       ScopePR,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil {
					return s, nil, true
				}
				key := cache.SkipKey(card.Ctx.Repo, card.PR.Number)
				m.app.SkipStore.Unskip(key)
				s.actionStatus = fmt.Sprintf("Un-skipped PR #%d", card.PR.Number)
				s.actionDone = true
				s.BuildScrollback(m)
				return s, nil, true
			},
		},
		{
			Name:        "bulk",
			Description: "Open bulk approve view",
			KeyBinding:  "ctrl+b",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				m.tryEnterBulkApprove()
				return m.scene, nil, true
			},
		},
		{
			Name:        "refresh",
			Description: "Refresh current PR and check for new PRs",
			KeyBinding:  "ctrl+r",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				card := m.currentCard()
				if card == nil {
					return s, nil, true
				}
				s.actionStatus = "Refreshing…"
				s.actionDone = false
				cmds := []tea.Cmd{refreshPRCmd(card.PR, card.Ctx)}
				for _, r := range m.app.Repos {
					cmds = append(cmds, fetchPRListCmd(r), fetchMergedPRListCmd(r), fetchTrackedPRListCmd(r, m.reviewStore, nil))
				}
				return s, tea.Batch(cmds...), true
			},
		},
		{
			Name:        "quit",
			Description: "Quit prx",
			KeyBinding:  "ctrl+q",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				return s, m.cleanupWorktrees(), true
			},
		},
		{
			Name:        "exit",
			Description: "Quit prx",
			Scope:       ScopeGlobal,
			Run: func(s *ConversationScene, m *Model, args string) (Scene, tea.Cmd, bool) {
				return s, m.cleanupWorktrees(), true
			},
		},
	}
}

// commandMap builds lookup tables from the command registry.
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

// runCommentOrReject handles the shared logic for /comment and /reject.
// With args: /comment posts immediately; /reject opens a confirm dialog.
// Without args: opens the diff overlay comment modal.
func runCommentOrReject(s *ConversationScene, m *Model, card *PRCard, args string, requestChanges bool) (Scene, tea.Cmd, bool) {
	repo, num := card.Ctx.Repo, card.PR.Number
	if args != "" {
		if requestChanges {
			s.confirm = &confirmDialog{
				description:  fmt.Sprintf("Request changes on PR #%d?", num),
				actionStatus: "Requesting changes…",
				cmd:          requestChangesCmd(repo, num, args),
			}
			return s, nil, true
		}
		s.actionStatus = "Posting comment…"
		return s, postGlobalCommentCmd(repo, num, args, nil), true
	}
	// No args — enter diff overlay with comment modal open
	s.input.Blur()
	m.diffView.Focused = true
	ds := newDiffOverlayScene(s, m.width, m.height)
	ds.openCommentModal(card, false, "", 0, requestChanges)
	return ds, ds.modal.textarea.Focus(), true
}

// ActionToolNames returns the MCP-prefixed action tool names filtered by ownership.
// This replaces the hardcoded tool lists in sendChatCmd.
func ActionToolNames(isOwnPR bool) []string {
	if isOwnPR {
		return []string{"mcp__prx__comment_on_pr", "mcp__prx__merge_pr", "mcp__prx__skip_pr"}
	}
	return []string{"mcp__prx__comment_on_pr", "mcp__prx__approve_pr", "mcp__prx__request_changes", "mcp__prx__skip_pr"}
}
