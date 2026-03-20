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
	default:
		var cmd tea.Cmd
		s.model, cmd = s.model.Update(msg)
		return s, cmd
	}
}

func (s *BulkApproveScene) View(_ *Model) string {
	return s.model.View()
}

func (s *BulkApproveScene) Resize(width, height int) {
	s.width = width
	s.height = height
	s.model.SetSize(width, height)
}
