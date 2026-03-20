package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/style"
)

// DiffOverlayScene is the full-screen diff viewer with comment modal.
type DiffOverlayScene struct {
	modal  commentModal
	conv   *ConversationScene // return target
	width  int
	height int
}

func newDiffOverlayScene(conv *ConversationScene, width, height int) *DiffOverlayScene {
	return &DiffOverlayScene{
		conv:   conv,
		width:  width,
		height: height,
	}
}

// ---------------------------------------------------------------------------
// Scene interface
// ---------------------------------------------------------------------------

func (s *DiffOverlayScene) Update(msg tea.Msg, m *Model) (Scene, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		if s.modal.active {
			return s.handleModalKey(msg, m)
		}
		return s.handleKey(msg, m)
	}
	return s, nil
}

func (s *DiffOverlayScene) View(m *Model) string {
	if s.modal.active {
		title := "  Add comment  (Enter submit · Alt+Enter newline · Esc cancel)"
		if s.modal.isInline {
			title = fmt.Sprintf("  Comment on %s:%d  (Enter submit · Alt+Enter newline · Esc cancel)", s.modal.filePath, s.modal.fileLine)
		}
		modalContent := lipgloss.JoinVertical(lipgloss.Left,
			style.PanelTitleFocused.Render(title),
			lipgloss.NewStyle().Padding(0, 1).Render(s.modal.textarea.View()),
		)
		return lipgloss.JoinVertical(lipgloss.Left,
			m.diffView.ViewWithModal(modalContent),
			s.renderFooter(m),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.diffView.ViewContent(),
		s.renderFooter(m),
	)
}

func (s *DiffOverlayScene) Resize(width, height int) {
	s.width = width
	s.height = height
	// Note: diffView size is set by Model in resizeLayout
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (s *DiffOverlayScene) handleKey(msg tea.KeyMsg, m *Model) (Scene, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+d":
		// Return to conversation scene
		m.diffView.Focused = false
		s.conv.Resize(s.width, s.height)
		s.conv.BuildScrollback(m)
		return s.conv, s.conv.FocusInput()
	case "?":
		// Quote current line into textarea and jump to conversation
		quote := m.diffView.CurrentQuote()
		m.diffView.Focused = false
		if quote != nil {
			var prefix string
			if quote.RawContent != "" {
				prefix = fmt.Sprintf("> %s:%d\n> %s\n\n", quote.File, quote.Line, quote.RawContent)
			} else {
				prefix = fmt.Sprintf("> %s:%d\n\n", quote.File, quote.Line)
			}
			s.conv.input.SetValue(prefix)
			s.conv.input.CursorEnd()
			s.conv.updateInputHeight()
		}
		s.conv.Resize(s.width, s.height)
		s.conv.BuildScrollback(m)
		return s.conv, s.conv.FocusInput()
	case "c":
		if card := m.currentCard(); card != nil {
			path, line := m.diffView.CurrentLineTarget()
			s.openCommentModal(card, path != "", path, line)
			return s, s.modal.textarea.Focus()
		}
	case "shift+left", "<":
		m.diffView.CollapseMore()
		return s, nil
	case "shift+right", ">":
		m.diffView.ExpandMore()
		return s, nil
	case "left":
		m.diffView.CollapseCurrentFile()
		return s, nil
	case "right":
		m.diffView.ExpandCurrentFile()
		return s, nil
	}
	var cmd tea.Cmd
	m.diffView, cmd = m.diffView.Update(msg)
	return s, cmd
}

func (s *DiffOverlayScene) handleModalKey(msg tea.KeyMsg, m *Model) (Scene, tea.Cmd) {
	switch msg.String() {
	case "esc":
		s.modal = commentModal{}
		return s, nil
	case "enter", "ctrl+s":
		body := strings.TrimSpace(s.modal.textarea.Value())
		if body == "" {
			return s, nil
		}
		card := m.currentCard()
		if card == nil {
			return s, nil
		}
		isInline := s.modal.isInline
		filePath := s.modal.filePath
		fileLine := s.modal.fileLine
		commitSHA := s.modal.commitSHA
		pendingItem := m.diffView.AddPendingComment(github.ReviewComment{
			Author: m.app.CurrentUser,
			Body:   body,
			Path:   filePath,
			Line:   fileLine,
		})
		s.modal = commentModal{}
		if isInline {
			return s, postInlineCommentCmd(m.app.Repo, card.PR.Number, commitSHA, filePath, fileLine, body, pendingItem)
		}
		return s, postGlobalCommentCmd(m.app.Repo, card.PR.Number, body, pendingItem)
	default:
		var cmd tea.Cmd
		s.modal.textarea, cmd = s.modal.textarea.Update(msg)
		return s, cmd
	}
}

func (s *DiffOverlayScene) openCommentModal(card *PRCard, isInline bool, path string, line int) {
	ta := textarea.New()
	ta.Placeholder = "Write your comment..."
	ta.SetWidth(s.width - 4)
	ta.SetHeight(4)
	s.modal = commentModal{
		active:    true,
		isInline:  isInline,
		filePath:  path,
		fileLine:  line,
		commitSHA: card.PR.HeadSHA,
		textarea:  ta,
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (s *DiffOverlayScene) renderFooter(m *Model) string {
	width := s.width
	if width == 0 {
		width = 80
	}
	status := fmt.Sprintf("prx  PR %d/%d", m.current+1, len(m.cards))
	hints := "j/k scroll  [/] file  {/} hunk  ←/→ collapse  </> all  ? ask  c comment  q back"
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}
