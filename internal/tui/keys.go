package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/chat"
)

// ---------------------------------------------------------------------------
// Top-level key dispatcher
// ---------------------------------------------------------------------------

// handleKey is the entry point for all key events in sceneReview.
// Priority order: modal > confirm dialog > perm dialog > global > panel-specific.
func (m *Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.modal.active {
		return m.handleModalKey(msg)
	}
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}
	if m.pendingPerm != nil {
		return m.handlePermKey(msg)
	}
	if model, cmd, handled := m.handleGlobalKey(msg); handled {
		return model, cmd
	}
	switch m.focus {
	case focusChat:
		return m.handleChatKey(msg)
	case focusAssessment:
		return m.handleAssessmentKey(msg)
	default: // focusDiff
		return m.handleDiffKey(msg)
	}
}

// ---------------------------------------------------------------------------
// Dialog key handlers
// ---------------------------------------------------------------------------

func (m *Model) handleModalKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		prev := m.modal.prevFocus
		m.modal = commentModal{}
		m.focus = prev
		m.diffView.Focused = (prev == focusDiff)
		m.resizeDiffView()
		return *m, nil
	case "enter", "ctrl+s":
		body := strings.TrimSpace(m.modal.textarea.Value())
		if body == "" {
			return *m, nil
		}
		card := m.currentCard()
		if card == nil {
			return *m, nil
		}
		isInline := m.modal.isInline
		filePath := m.modal.filePath
		fileLine := m.modal.fileLine
		commitSHA := m.modal.commitSHA
		prev := m.modal.prevFocus
		pendingItem := m.diffView.AddPendingComment(github.ReviewComment{
			Author: m.app.CurrentUser,
			Body:   body,
			Path:   filePath,
			Line:   fileLine,
		})
		m.modal = commentModal{}
		m.focus = prev
		m.diffView.Focused = (prev == focusDiff)
		m.resizeDiffView()
		if isInline {
			return *m, postInlineCommentCmd(m.app.Repo, card.PR.Number, commitSHA, filePath, fileLine, body, pendingItem)
		}
		return *m, postGlobalCommentCmd(m.app.Repo, card.PR.Number, body, pendingItem)
	default:
		var cmd tea.Cmd
		m.modal.textarea, cmd = m.modal.textarea.Update(msg)
		return *m, cmd
	}
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		cmd := m.confirm.cmd
		m.actionStatus = m.confirm.actionStatus
		m.actionDone = false
		m.confirm = nil
		return *m, cmd
	case "n", "esc":
		m.confirm = nil
	}
	return *m, nil
}

func (m *Model) handlePermKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.pendingPerm.respond(true)
		m.pendingPerm = nil
	case "n", "esc":
		m.pendingPerm.respond(false)
		m.pendingPerm = nil
	}
	return *m, nil
}

// ---------------------------------------------------------------------------
// Global keys (work in any panel)
// ---------------------------------------------------------------------------

// handleGlobalKey processes keys that apply regardless of panel focus.
// Returns (model, cmd, handled) — when handled is false the caller should
// continue to panel-specific dispatch.
func (m *Model) handleGlobalKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch msg.String() {
	case "q":
		if m.focus == focusChat {
			return *m, nil, false // let chat textarea receive it
		}
		return *m, m.cleanupWorktrees(), true
	case "ctrl+c":
		return *m, m.cleanupWorktrees(), true
	case "?":
		if m.focus == focusChat {
			return *m, nil, false
		}
		return *m, m.activateChat(), true
	case "tab":
		cmd := m.cycleTab()
		return *m, cmd, true
	case "ctrl+n":
		m.navigatePR(1)
		return *m, nil, true
	case "ctrl+p":
		m.navigatePR(-1)
		return *m, nil, true
	case "ctrl+b":
		m.tryEnterBulkApprove()
		return *m, nil, true
	case "ctrl+r":
		if card := m.currentCard(); card != nil {
			m.actionStatus = "Refreshing\u2026"
			m.actionDone = false
			return *m, tea.Batch(refreshPRCmd(card.PR, m.app), fetchPRListCmd(m.app.Repo)), true
		}
		return *m, nil, true
	case "ctrl+shift+r":
		return *m, m.hardReset(), true
	case "shift+left", "<":
		m.diffView.CollapseMore()
		return *m, nil, true
	case "shift+right", ">":
		m.diffView.ExpandMore()
		return *m, nil, true
	}
	return *m, nil, false
}

// cycleTab advances panel focus: assessment → diff → chat → assessment.
// Tabbing to chat activates it if not already open.
func (m *Model) cycleTab() tea.Cmd {
	switch m.focus {
	case focusAssessment:
		m.focus = focusDiff
		m.diffView.Focused = true
		m.chatView.Focused = false
		m.chatView.Blur()
	case focusDiff:
		m.diffView.Focused = false
		if !m.chatActive {
			return m.activateChat()
		}
		m.focus = focusChat
		m.chatView.Focused = true
		return m.chatView.Focus()
	default: // focusChat
		m.focus = focusAssessment
		m.chatView.Focused = false
		m.chatView.Blur()
	}
	return nil
}

// hardReset cancels all in-flight work, clears cache, and restarts from scratch.
func (m *Model) hardReset() tea.Cmd {
	var cmds []tea.Cmd
	for _, card := range m.cards {
		if card.chatCancel != nil {
			card.chatCancel()
		}
		if card.worktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(m.app.RepoDir, card.worktreePath))
		}
	}
	m.app.Cache.Clear()
	m.cards = nil
	m.total = 0
	m.fetching = 0
	m.scoring = 0
	m.current = 0
	m.actionStatus = ""
	m.actionDone = false
	m.chatActive = false
	m.focus = focusAssessment
	m.diffView.Focused = false
	cmds = append(cmds, m.spinner.Tick, fetchPRListCmd(m.app.Repo))
	return tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// Panel key handlers
// ---------------------------------------------------------------------------

func (m *Model) handleAssessmentKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "n":
		m.navigatePR(1)
	case "p":
		m.navigatePR(-1)
	case "j", "down":
		if m.assessmentPanel.AtBottom() {
			m.focus = focusDiff
			m.diffView.Focused = true
			m.chatView.Focused = false
			m.chatView.Blur()
		} else {
			m.assessmentPanel.ScrollDown(1)
		}
	case "k", "up":
		m.assessmentPanel.ScrollUp(1)
	case "a":
		if card := m.currentCard(); card != nil && !card.Scoring && !m.isOwnPR(card) {
			repo, num := m.app.Repo, card.PR.Number
			m.confirm = &confirmDialog{
				description:  fmt.Sprintf("Approve PR #%d?", num),
				actionStatus: "Approving\u2026",
				cmd:          approveCmd(repo, num),
			}
		}
	case "m":
		if card := m.currentCard(); card != nil && !card.Scoring && m.isOwnPR(card) {
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
				actionStatus: "Merging\u2026",
				cmd:          mergeCmd(repo, num, method),
			}
		}
	case "r":
		if card := m.currentCard(); card != nil && !card.Scoring && card.Assessment != nil && !m.isOwnPR(card) {
			repo, num, notes := m.app.Repo, card.PR.Number, card.Assessment.ReviewNotes
			m.confirm = &confirmDialog{
				description:  fmt.Sprintf("Request changes on PR #%d?", num),
				actionStatus: "Requesting changes\u2026",
				cmd:          requestChangesCmd(repo, num, notes),
			}
		}
	case "right":
		if !m.bodyExpanded {
			m.bodyExpanded = true
			m.rebuildAssessment()
		}
	case "left":
		if m.bodyExpanded {
			m.bodyExpanded = false
			m.rebuildAssessment()
		}
	case "c":
		if card := m.currentCard(); card != nil {
			m.openCommentModal(card, false, "", 0)
			return *m, m.modal.textarea.Focus()
		}
	}
	return *m, nil
}

func (m *Model) handleDiffKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "left":
		m.diffView.CollapseCurrentFile()
		return *m, nil
	case "right":
		m.diffView.ExpandCurrentFile()
		return *m, nil
	case "k", "up":
		if m.diffView.AtTop() {
			m.focus = focusAssessment
			m.diffView.Focused = false
			return *m, nil
		}
	case "c":
		if card := m.currentCard(); card != nil {
			path, line := m.diffView.CurrentLineTarget()
			m.openCommentModal(card, path != "", path, line)
			return *m, m.modal.textarea.Focus()
		}
	}
	var cmd tea.Cmd
	m.diffView, cmd = m.diffView.Update(msg)
	return *m, cmd
}

func (m *Model) handleChatKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.chatView.Streaming {
			if card := m.currentCard(); card != nil && card.chatCancel != nil {
				card.chatCancel()
				card.chatCancel = nil
			}
			return *m, nil
		}
		m.chatActive = false
		m.chatView.Focused = false
		m.chatView.Blur()
		m.focus = focusDiff
		m.diffView.Focused = true
		return *m, nil
	case "enter":
		return *m, m.sendChatMessage()
	}
	var cmd tea.Cmd
	m.chatView, cmd = m.chatView.Update(msg)
	return *m, cmd
}

// ---------------------------------------------------------------------------
// Chat activation & message sending
// ---------------------------------------------------------------------------

func (m *Model) activateChat() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}
	path, line := m.diffView.CurrentLineTarget()
	if path != "" {
		card.chatContext = &ai.DiffContext{File: path, Line: line}
	} else {
		card.chatContext = nil
	}
	m.chatActive = true
	m.focus = focusChat
	m.diffView.Focused = false
	m.chatView.Focused = true
	m.chatView.SetContext(card.chatContext)
	m.chatView.WelcomeText = buildChatWelcome(card, card.chatContext)
	m.chatView.SetMessages(card.chatMessages)
	m.resizeDiffView()

	var cmds []tea.Cmd
	cmds = append(cmds, m.chatView.Focus())
	if card.worktreePath == "" {
		m.chatView.Status = "Creating worktree..."
		cmds = append(cmds, createWorktreeCmd(m.app.RepoDir, card.PR.HeadRefName, card.PR.Number))
	}
	return tea.Batch(cmds...)
}

func (m *Model) sendChatMessage() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}
	body := m.chatView.InputValue()
	if body == "" || m.chatView.Streaming {
		return nil
	}
	m.chatView.ResetInput()
	card.chatMessages = append(card.chatMessages, chat.Message{Role: "user", Content: body})
	m.chatView.SetMessages(card.chatMessages)
	m.chatView.Streaming = true
	m.chatView.Status = "Starting Claude..."
	if card.worktreePath != "" {
		ctx, cancel := context.WithCancel(context.Background())
		card.chatCancel = cancel
		return sendChatCmd(ctx, card.worktreePath, card.PR, card.Assessment, card.chatMessages,
			card.chatContext, m.app.Config.Review.Model, m.app.Repo, m.isOwnPR(card),
			m.permSocketPath, m.program)
	}
	return nil
}

func buildChatWelcome(card *PRCard, ctx *ai.DiffContext) string {
	var sb strings.Builder
	if card.Assessment != nil {
		var topName, topReason string
		topScore := 0
		for name, f := range card.Assessment.Factors {
			if f.Score > topScore {
				topScore = f.Score
				topName = name
				topReason = f.Reason
			}
		}
		if topScore >= 3 {
			fmt.Fprintf(&sb, "Highest risk: %s (%d/5) — %s\n\n", topName, topScore, topReason)
		}
	}
	if ctx != nil && ctx.File != "" {
		if ctx.Line > 0 {
			fmt.Fprintf(&sb, "You're looking at %s:%d. ", ctx.File, ctx.Line)
		} else {
			fmt.Fprintf(&sb, "You're looking at %s. ", ctx.File)
		}
	}
	sb.WriteString("What questions do you have?")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Comment modal
// ---------------------------------------------------------------------------

func (m *Model) openCommentModal(card *PRCard, isInline bool, path string, line int) {
	ta := textarea.New()
	ta.Placeholder = "Write your comment..."
	ta.SetWidth(m.width - 4)
	ta.SetHeight(4)
	prev := m.focus
	m.modal = commentModal{
		active:    true,
		isInline:  isInline,
		filePath:  path,
		fileLine:  line,
		commitSHA: card.PR.HeadSHA,
		prevFocus: prev,
		textarea:  ta,
	}
	m.focus = focusModal
	m.resizeDiffView()
}
