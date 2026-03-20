package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/tui/bulkapprove"
	"github.com/sleuth-io/prx/internal/tui/diff"
	"github.com/sleuth-io/prx/internal/tui/perm"
	"github.com/sleuth-io/prx/internal/tui/scoring"
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

	// Bulk approve
	bulkApproveShown bool // true once auto-shown this session

	// Startup
	startupDone bool // true once first PR is scored and ready to view
	startupLog  []startupEntry
	noPRs       bool // true when no open PRs found — any key exits
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	cs := newConversationScene()

	return Model{
		app:       a,
		spinner:   s,
		scene:     cs,
		convScene: cs,
		diffView:  diff.NewDiffView(80, 20),
		startupLog: []startupEntry{
			{text: fmt.Sprintf("Signed in as %s", a.CurrentUser), done: true},
			{text: fmt.Sprintf("Fetching open PRs from %s", a.Repo)},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchPRListCmd(m.app.Repo), m.convScene.FocusInput())
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

// ---------------------------------------------------------------------------
// Model helpers
// ---------------------------------------------------------------------------

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func (m Model) currentCard() *PRCard {
	if m.current < len(m.cards) {
		return m.cards[m.current]
	}
	return nil
}

func (m Model) isOwnPR(card *PRCard) bool {
	return card.PR.Author == m.app.CurrentUser
}

func (m *Model) navigatePR(delta int, s *ConversationScene) {
	if !m.bulkApproveShown && m.scoring == 0 && m.fetching == 0 {
		if m.tryEnterBulkApprove() {
			return
		}
	}
	next := m.current + delta
	if next < 0 || next >= len(m.cards) {
		return
	}
	m.current = next
	s.actionStatus = ""
	s.actionDone = false
	m.loadCurrentDiff()
	s.BuildScrollback(m)
}

func (m *Model) loadCurrentDiff() {
	card := m.currentCard()
	if card == nil || card.PR == nil {
		return
	}
	if card.parsedFiles != nil {
		m.diffView.SetParsedContent(card.parsedFiles, card.PR)
	} else {
		m.diffView.SetContent(card.PR.Diff, card.PR)
	}
}

func (m *Model) buildRenderData(card *PRCard) scoring.RenderData {
	return scoring.RenderData{
		PR:               card.PR,
		Assessment:       card.Assessment,
		Score:            card.WeightedScore,
		Verdict:          card.Verdict,
		Scoring:          card.Scoring,
		ScoringErr:       card.ScoringErr,
		SpinnerView:      m.spinner.View(),
		Criteria:         m.app.Config.Criteria,
		BodyExpanded:     false,
		ScoringToolCount: card.ScoringToolCount,
		ScoringLastTool:  card.ScoringLastTool,
		ScoringStatus:    card.ScoringStatus,
	}
}

// startChatCmd creates a sendChatCmd for the given card.
func (m *Model) startChatCmd(card *PRCard) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	card.Chat.Cancel = cancel
	return sendChatCmd(ctx, card.Chat.WorktreePath, card.PR, card.Assessment, card.Chat.Messages,
		nil, m.app.Config.Review.Model, m.app.Repo, m.isOwnPR(card),
		m.permSocketPath, m.program)
}

// hardReset cancels all in-flight work, clears cache, and restarts from scratch.
func (m *Model) hardReset() tea.Cmd {
	var cmds []tea.Cmd
	for _, card := range m.cards {
		card.Chat.CancelStreaming()
		if card.Chat.WorktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(m.app.RepoDir, card.Chat.WorktreePath))
		}
	}
	m.convScene.actionStatus = ""
	m.convScene.actionDone = false
	m.scene = m.convScene
	m.app.Cache.Clear()
	m.cards = nil
	m.total = 0
	m.fetching = 0
	m.scoring = 0
	m.current = 0
	m.startupDone = false
	m.startupLog = []startupEntry{
		{text: fmt.Sprintf("Signed in as %s", m.app.CurrentUser), done: true},
		{text: fmt.Sprintf("Fetching open PRs from %s", m.app.Repo)},
	}
	cmds = append(cmds, m.spinner.Tick, fetchPRListCmd(m.app.Repo))
	return tea.Batch(cmds...)
}

func (m *Model) cleanupWorktrees() tea.Cmd {
	if m.permCleanup != nil {
		m.permCleanup()
		m.permCleanup = nil
	}
	var cmds []tea.Cmd
	for _, card := range m.cards {
		if card.Chat.WorktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(m.app.RepoDir, card.Chat.WorktreePath))
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
}

// buildScrollback is a convenience that delegates to the conversation scene.
func (m *Model) buildScrollback() {
	m.convScene.BuildScrollback(m)
}

// tryEnterBulkApprove creates a bulk approve scene if there are eligible PRs.
func (m *Model) tryEnterBulkApprove() bool {
	var items []bulkapprove.Item
	for _, card := range m.cards {
		if !card.Scoring && card.ScoringErr == nil && !m.isOwnPR(card) {
			summary := ""
			if card.Assessment != nil {
				summary = card.Assessment.RiskSummary
			}
			items = append(items, bulkapprove.ItemFromCard(card.PR, card.WeightedScore, card.Verdict, summary))
		}
	}
	if len(items) == 0 {
		return false
	}
	ba := bulkapprove.New(m.app.Repo, items, m.width, m.height)
	m.bulkApproveShown = true
	m.scene = newBulkApproveScene(ba, m.convScene, m.width, m.height)
	return true
}
