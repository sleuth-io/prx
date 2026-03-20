package tui

import tea "github.com/charmbracelet/bubbletea"

// Scene is a self-contained UI mode (conversation, diff overlay, bulk approve).
// Scenes receive the Model pointer for access to shared state (cards, spinner, app).
type Scene interface {
	// Update handles messages relevant to this scene.
	// Returns the (possibly new) scene and a command.
	Update(msg tea.Msg, m *Model) (Scene, tea.Cmd)

	// View renders the scene.
	View(m *Model) string

	// Resize updates the scene's dimensions.
	Resize(width, height int)
}
