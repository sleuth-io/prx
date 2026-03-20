package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/chat"
)

// ---------------------------------------------------------------------------
// Top-level key dispatcher
// ---------------------------------------------------------------------------

// handleKey is the entry point for all key events.
// Priority: modal > diff overlay > confirm > perm > ctrl commands > textarea
func (m *Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Comment modal (in diff overlay)
	if m.modal.active {
		return m.handleModalKey(msg)
	}

	// Diff overlay
	if m.overlay == overlayDiff {
		return m.handleDiffOverlayKey(msg)
	}

	// Confirm dialog
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}

	// Permission dialog
	if m.pendingPerm != nil {
		return m.handlePermKey(msg)
	}

	// Esc: cancel streaming or clear input/quote
	if msg.String() == "esc" {
		card := m.currentCard()
		if card != nil && card.Streaming {
			if card.chatCancel != nil {
				card.chatCancel()
				card.chatCancel = nil
			}
			return *m, nil
		}
		m.input.Reset()
		m.updateInputHeight()
		return *m, nil
	}

	// Enter: handle slash commands or send chat message
	if msg.String() == "enter" {
		if model, cmd, handled := m.handleSlashCommand(); handled {
			return model, cmd
		}
		return *m, m.sendChatMessage()
	}

	// Ctrl/special key commands (work regardless of input state)
	if model, cmd, handled := m.handleCtrlKey(msg); handled {
		return model, cmd
	}

	// Everything else goes to textarea
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateInputHeight()
	return *m, cmd
}

// ---------------------------------------------------------------------------
// Dialog key handlers
// ---------------------------------------------------------------------------

func (m *Model) handleModalKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.modal = commentModal{}
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
		pendingItem := m.diffView.AddPendingComment(github.ReviewComment{
			Author: m.app.CurrentUser,
			Body:   body,
			Path:   filePath,
			Line:   fileLine,
		})
		m.modal = commentModal{}
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
// Ctrl/special key commands
// ---------------------------------------------------------------------------

func (m *Model) handleCtrlKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()

	// Hard reset is special — not a registerable command
	if key == "ctrl+shift+r" {
		return *m, m.hardReset(), true
	}

	// Try registered command key bindings
	return m.handleCommandKey(key)
}

// ---------------------------------------------------------------------------
// Diff overlay keys
// ---------------------------------------------------------------------------

func (m *Model) handleDiffOverlayKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.overlay = overlayNone
		m.diffView.Focused = false
		m.resizeLayout()
		m.buildScrollback()
		return *m, m.input.Focus()
	case "?":
		// Quote current line into textarea and jump to chat
		quote := m.diffView.CurrentQuote()
		m.overlay = overlayNone
		m.diffView.Focused = false
		if quote != nil {
			var prefix string
			if quote.RawContent != "" {
				prefix = fmt.Sprintf("> %s:%d\n> %s\n\n", quote.File, quote.Line, quote.RawContent)
			} else {
				prefix = fmt.Sprintf("> %s:%d\n\n", quote.File, quote.Line)
			}
			m.input.SetValue(prefix)
			m.input.CursorEnd()
			m.updateInputHeight()
		}
		m.resizeLayout()
		m.buildScrollback()
		return *m, m.input.Focus()
	case "c":
		if card := m.currentCard(); card != nil {
			path, line := m.diffView.CurrentLineTarget()
			m.openCommentModal(card, path != "", path, line)
			return *m, m.modal.textarea.Focus()
		}
	case "shift+left", "<":
		m.diffView.CollapseMore()
		return *m, nil
	case "shift+right", ">":
		m.diffView.ExpandMore()
		return *m, nil
	case "left":
		m.diffView.CollapseCurrentFile()
		return *m, nil
	case "right":
		m.diffView.ExpandCurrentFile()
		return *m, nil
	}
	var cmd tea.Cmd
	m.diffView, cmd = m.diffView.Update(msg)
	return *m, cmd
}

// ---------------------------------------------------------------------------
// Chat message sending
// ---------------------------------------------------------------------------

func (m *Model) sendChatMessage() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}
	body := strings.TrimSpace(m.input.Value())
	if body == "" || card.Streaming {
		return nil
	}
	m.input.Reset()
	m.updateInputHeight()
	card.chatMessages = append(card.chatMessages, chat.Message{Role: "user", Content: body})
	card.Streaming = true
	card.ChatStatus = "Starting Claude..."
	m.buildScrollback()

	var cmds []tea.Cmd
	if card.worktreePath == "" {
		card.ChatStatus = "Creating worktree..."
		cmds = append(cmds, createWorktreeCmd(m.app.RepoDir, card.PR.HeadRefName, card.PR.Number))
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		card.chatCancel = cancel
		cmds = append(cmds, sendChatCmd(ctx, card.worktreePath, card.PR, card.Assessment, card.chatMessages,
			nil, m.app.Config.Review.Model, m.app.Repo, m.isOwnPR(card),
			m.permSocketPath, m.program))
	}
	return tea.Batch(cmds...)
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
	m.overlay = overlayNone
	m.startupDone = false
	m.startupLog = []startupEntry{
		{text: fmt.Sprintf("Signed in as %s", m.app.CurrentUser), done: true},
		{text: fmt.Sprintf("Fetching open PRs from %s", m.app.Repo)},
	}
	cmds = append(cmds, m.spinner.Tick, fetchPRListCmd(m.app.Repo))
	return tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// Comment modal
// ---------------------------------------------------------------------------

func (m *Model) openCommentModal(card *PRCard, isInline bool, path string, line int) {
	ta := textarea.New()
	ta.Placeholder = "Write your comment..."
	ta.SetWidth(m.width - 4)
	ta.SetHeight(4)
	m.modal = commentModal{
		active:    true,
		isInline:  isInline,
		filePath:  path,
		fileLine:  line,
		commitSHA: card.PR.HeadSHA,
		textarea:  ta,
	}
}
