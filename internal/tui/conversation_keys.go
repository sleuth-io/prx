package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/logger"
)

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (s *ConversationScene) handleKey(msg tea.KeyMsg, m *Model) (Scene, tea.Cmd) {
	// Confirm dialog
	if s.confirm != nil {
		return s.handleConfirmKey(msg)
	}

	// Permission dialog
	if m.pendingPerm != nil {
		return s.handlePermKey(msg, m)
	}

	// Esc: cancel streaming or clear input/quote
	if msg.String() == "esc" {
		card := m.currentCard()
		if card != nil && card.Chat.IsStreaming() {
			card.Chat.CancelStreaming()
			return s, nil
		}
		s.input.Reset()
		s.updateInputHeight()
		return s, nil
	}

	// Enter: handle slash commands or send chat message
	if msg.String() == "enter" {
		if scene, cmd, handled := s.handleSlashCommand(m); handled {
			return scene, cmd
		}
		return s, s.sendChatMessage(m)
	}

	// Viewport scrolling
	switch msg.String() {
	case "pgup":
		s.viewport.HalfPageUp()
		return s, nil
	case "pgdown":
		s.viewport.HalfPageDown()
		return s, nil
	}

	// Ctrl/special key commands (work regardless of input state)
	if scene, cmd, handled := s.handleCtrlKey(msg, m); handled {
		return scene, cmd
	}

	// Everything else goes to textarea
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.updateInputHeight()
	return s, cmd
}

// ---------------------------------------------------------------------------
// Mouse handling
// ---------------------------------------------------------------------------

func (s *ConversationScene) handleMouse(msg tea.MouseMsg, m *Model) (Scene, tea.Cmd) {
	logger.Info("mouse: button=%d x=%d y=%d ctrl=%v", msg.Button, msg.X, msg.Y, msg.Ctrl)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		s.viewport.ScrollUp(3)
	case tea.MouseButtonWheelDown:
		s.viewport.ScrollDown(3)
	case tea.MouseButtonLeft:
		linkScreenRow := s.imageLinkRow - s.viewport.YOffset
		logger.Info("mouse click: imageURL=%q ctrl=%v linkRow=%d y=%d",
			s.imageURL, msg.Ctrl, linkScreenRow, msg.Y)
		// Ctrl+click on image link line → open URL
		if s.imageURL != "" && msg.Ctrl {
			if msg.Y == linkScreenRow {
				logger.Info("opening URL: %s", s.imageURL)
				return s, openURLCmd(s.imageURL)
			}
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Confirm / permission dialogs
// ---------------------------------------------------------------------------

func (s *ConversationScene) handleConfirmKey(msg tea.KeyMsg) (Scene, tea.Cmd) {
	switch msg.String() {
	case "y":
		cmd := s.confirm.cmd
		s.actionStatus = s.confirm.actionStatus
		s.actionDone = false
		s.confirm = nil
		return s, cmd
	case "n", "esc":
		s.confirm = nil
	}
	return s, nil
}

func (s *ConversationScene) handlePermKey(msg tea.KeyMsg, m *Model) (Scene, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.pendingPerm.respond(true)
		m.pendingPerm = nil
	case "n", "esc":
		m.pendingPerm.respond(false)
		m.pendingPerm = nil
	}
	return s, nil
}

func (s *ConversationScene) handleCtrlKey(msg tea.KeyMsg, m *Model) (Scene, tea.Cmd, bool) {
	key := msg.String()
	// Hard reset is special
	if key == "ctrl+shift+r" {
		return s, m.hardReset(), true
	}
	// Try registered command key bindings
	return s.handleCommandKey(key, m)
}

// ---------------------------------------------------------------------------
// Slash commands
// ---------------------------------------------------------------------------

// handleSlashCommand checks if the input is a slash command and executes it.
func (s *ConversationScene) handleSlashCommand(m *Model) (Scene, tea.Cmd, bool) {
	input := strings.TrimSpace(s.input.Value())
	if !strings.HasPrefix(input, "/") {
		return s, nil, false
	}
	name := strings.ToLower(strings.TrimPrefix(input, "/"))
	slashCmds, _ := commandMap()
	if cmd, ok := slashCmds[name]; ok {
		s.input.Reset()
		return cmd.Run(s, m)
	}

	// Check if it matches a skill name — send as a chat message asking to activate the skill.
	for _, sk := range m.app.Skills {
		if sk.Name == name {
			s.input.SetValue("Please activate the " + sk.Name + " skill and use it to help me.")
			return s, s.sendChatMessage(m), true
		}
	}

	return s, nil, false
}

// handleCommandKey checks if a key matches a command's KeyBinding.
func (s *ConversationScene) handleCommandKey(key string, m *Model) (Scene, tea.Cmd, bool) {
	_, keyCmds := commandMap()
	if cmd, ok := keyCmds[key]; ok {
		return cmd.Run(s, m)
	}
	return s, nil, false
}

// ---------------------------------------------------------------------------
// Chat message sending
// ---------------------------------------------------------------------------

func (s *ConversationScene) sendChatMessage(m *Model) tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}
	body := strings.TrimSpace(s.input.Value())
	if body == "" || card.Chat.IsStreaming() {
		return nil
	}

	s.input.Reset()
	s.updateInputHeight()
	card.Chat.StartMessage(body)
	s.BuildScrollback(m)

	var cmds []tea.Cmd
	if card.Chat.NeedsWorktree() {
		card.Chat.Status = "Creating worktree..."
		cmds = append(cmds, createWorktreeCmd(card.Ctx.RepoDir, card.PR.HeadSHA, card.Ctx.Repo, card.PR.Number))
	} else {
		cmd := m.startChatCmd(card)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// Action done
// ---------------------------------------------------------------------------

func (s *ConversationScene) handleActionDone(msg actionDoneMsg, m *Model) (Scene, tea.Cmd) {
	s.actionDone = true
	if msg.err != nil {
		s.actionStatus = fmt.Sprintf("%s failed: %s", msg.action, msg.err)
		s.BuildScrollback(m)
		return s, nil
	}
	switch msg.action {
	case actionMerge:
		s.actionStatus = fmt.Sprintf("Merged PR #%d", msg.pr)
		if card := m.findCard(msg.repo, msg.pr); card != nil {
			card.PR.State = "MERGED"
		}
	case actionApprove:
		s.actionStatus = fmt.Sprintf("Approved PR #%d", msg.pr)
	case actionRequestChanges:
		s.actionStatus = fmt.Sprintf("Requested changes on PR #%d", msg.pr)
	case actionPostMergeApprove:
		s.actionStatus = fmt.Sprintf("Approved post-merge PR #%d \U0001f44d", msg.pr)
		m.markPostMergeReacted(msg.repo, msg.pr, "+1")
	case actionPostMergeFlag:
		s.actionStatus = fmt.Sprintf("Flagged post-merge PR #%d \U0001f44e", msg.pr)
		m.markPostMergeReacted(msg.repo, msg.pr, "-1")
	default:
		s.actionStatus = fmt.Sprintf("%s done", msg.action)
	}
	s.BuildScrollback(m)
	// markPostMergeReacted may have transitioned to bulk approve via
	// skipToVisibleCard → tryEnterBulkApprove. Return m.scene so the
	// caller picks up whichever scene is now active.
	return m.scene, nil
}
