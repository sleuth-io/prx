package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/imgrender"
	"github.com/sleuth-io/prx/internal/tui/diff"
	"github.com/sleuth-io/prx/internal/tui/perm"
)

var footerStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("237")).
	Foreground(lipgloss.Color("250")).
	Padding(0, 1)

var permBannerStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("214")).
	Foreground(lipgloss.Color("230")).
	Padding(0, 1)

type Model struct {
	app            *app.App
	cards          []*PRCard
	total          int
	fetching       int // PRs whose details are still being fetched
	scoring        int // PRs whose assessments are still in progress
	current        int
	spinner        spinner.Model
	program        *tea.Program
	err            error
	width          int
	height         int
	permSocketPath string
	permCleanup    func()
	pendingPerm    *permRequestMsg

	// Scene: active UI mode (conversation, diff overlay, bulk approve)
	scene Scene
	// convScene is the conversation scene, kept for returning from other scenes
	convScene *ConversationScene

	// Diff view: shared state loaded by Model, rendered by DiffOverlayScene
	diffView diff.DiffView

	// Image rendering cache (sixel/kitty/iTerm2)
	imageCache *imgrender.Cache

	// Bulk approve
	bulkApproveShown bool // true once auto-shown this session

	// Startup
	startupDone bool // true once first PR is scored and ready to view
	startupLog  []startupEntry
	noPRs       bool // true when no open PRs found — any key exits

	// Post-merge review
	showAllMerged  bool // when true, show all merged PRs including already-reviewed/reacted
	openListDone   bool // true once open PR list fetch has returned
	mergedListDone bool // true once merged PR list fetch has returned
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	cs := newConversationScene()

	var imgCache *imgrender.Cache
	if imgrender.Supported() {
		imgCache = imgrender.NewCache(60, 6) // 60 cols wide, 6 rows tall thumbnail
	}

	return Model{
		app:        a,
		spinner:    s,
		scene:      cs,
		convScene:  cs,
		diffView:   diff.NewDiffView(80, 20),
		imageCache: imgCache,
		startupLog: []startupEntry{
			{text: fmt.Sprintf("Signed in as %s", a.CurrentUser), done: true},
			{text: fmt.Sprintf("Fetching PRs from %s", a.Repo)},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchPRListCmd(m.app.Repo), fetchMergedPRListCmd(m.app.Repo, m.app.CurrentUser), m.convScene.FocusInput())
}

// Update dispatches global messages first, then routes to the active scene.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case SetProgramMsg:
		return m.handleSetProgram(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)
	case perm.Msg:
		m.pendingPerm = &permRequestMsg{description: msg.Description, respond: msg.Respond}
		return m, nil
	case perm.RefreshMsg:
		return m.handlePermRefresh(msg)
	case perm.ConfigReloadMsg:
		return m.handleConfigReload(msg)
	case tea.KeyMsg:
		// Any key exits on error or no-PRs screens
		if m.err != nil || m.noPRs {
			return m, m.cleanupWorktrees()
		}
		// q/ctrl+c/ctrl+q exit during startup loading
		if !m.startupDone {
			key := msg.String()
			if key == "q" || key == "ctrl+c" || key == "ctrl+q" {
				return m, m.cleanupWorktrees()
			}
			return m, nil
		}
	}

	return m.updateReview(msg)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\nError: %v\n\nPress q to quit.\n", m.err)
	}

	if !m.startupDone {
		return m.renderStartupLog()
	}

	// Active scene (conversation, diff overlay, or bulk approve)
	return m.scene.View(&m)
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

func (m *Model) resizeLayout() {
	width := m.width
	if width == 0 {
		width = 80
	}
	height := m.height
	if height == 0 {
		height = 24
	}

	// Always resize diffView (shared state)
	footerH := 1
	m.diffView.SetSize(width, height-footerH)

	// Resize active scene
	m.scene.Resize(width, height)
}
