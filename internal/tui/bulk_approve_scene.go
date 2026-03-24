package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/tui/bulkapprove"
)

// BulkApproveScene wraps bulkapprove.Model as a Scene adapter.
type BulkApproveScene struct {
	model  bulkapprove.Model
	conv   *ConversationScene // return target
	width  int
	height int
}

func newBulkApproveScene(ba bulkapprove.Model, conv *ConversationScene, width, height int) *BulkApproveScene {
	return &BulkApproveScene{
		model:  ba,
		conv:   conv,
		width:  width,
		height: height,
	}
}

func (s *BulkApproveScene) Update(msg tea.Msg, m *Model) (Scene, tea.Cmd) {
	switch msg := msg.(type) {
	case bulkapprove.ExitMsg:
		m.loadCurrentDiff()
		s.conv.BuildScrollback(m)
		return s.conv, s.conv.FocusInput()
	case bulkapprove.ViewPRMsg:
		for i, c := range m.cards {
			if c.PR.Number == msg.PRNumber {
				m.current = i
				break
			}
		}
		m.loadCurrentDiff()
		s.conv.BuildScrollback(m)
		return s.conv, s.conv.FocusInput()
	case bulkapprove.QuitMsg:
		return s, m.cleanupWorktrees()
	case tea.KeyMsg:
		if msg.String() == "ctrl+r" {
			return s, tea.Batch(
				fetchPRListCmd(m.app.Repo),
				fetchMergedPRListCmd(m.app.Repo, m.app.CurrentUser),
			)
		}
		if msg.String() == "ctrl+a" {
			m.showAllMerged = !m.showAllMerged
			if m.visibleCardCount() > 0 {
				m.skipToVisibleCard()
				m.loadCurrentDiff()
				s.conv.BuildScrollback(m)
				return s.conv, s.conv.FocusInput()
			}
			// Still no visible cards — rebuild bulk approve with updated items.
			m.tryEnterBulkApprove()
			return m.scene, nil
		}
		var cmd tea.Cmd
		s.model, cmd = s.model.Update(msg)
		return s, cmd
	default:
		var cmd tea.Cmd
		s.model, cmd = s.model.Update(msg)
		return s, cmd
	}
}

func (s *BulkApproveScene) View(_ *Model) string {
	// Clear any Kitty protocol images from the conversation scene.
	return "\x1b_Ga=d,d=a\x1b\\" + s.model.View()
}

func (s *BulkApproveScene) Resize(width, height int) {
	s.width = width
	s.height = height
	s.model.SetSize(width, height)
}
