package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/tui/bulkapprove"
	"github.com/sleuth-io/prx/internal/tui/chat"
	"github.com/sleuth-io/prx/internal/tui/diff"
	"github.com/sleuth-io/prx/internal/tui/perm"
	"github.com/sleuth-io/prx/internal/tui/scoring"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var footerStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("237")).
	Foreground(lipgloss.Color("250")).
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

	// Conversation view
	viewport viewport.Model
	input    textarea.Model
	overlay  overlayKind
	diffView diff.DiffView
	modal    commentModal

	// Dialogs
	confirm      *confirmDialog
	actionStatus string // e.g. "Merging…", "Approving…"
	actionDone   bool   // true when actionStatus is a final result (no spinner)

	// Bulk approve
	bulkApprove       bulkapprove.Model
	bulkApproveShown  bool // true once auto-shown this session
	bulkApproveActive bool // true while bulk approve scene is displayed

	// Startup
	startupDone bool // true once first PR is scored and ready to view
	startupLog  []startupEntry
}

type startupEntry struct {
	text string
	done bool
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ta := textarea.New()
	ta.Placeholder = ""
	ta.CharLimit = 2000
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = lipgloss.NewStyle()
	ta.Prompt = ""

	vp := viewport.New(80, 20)

	return Model{
		app:      a,
		spinner:  s,
		viewport: vp,
		input:    ta,
		diffView: diff.NewDiffView(80, 20),
		startupLog: []startupEntry{
			{text: fmt.Sprintf("Signed in as %s", a.CurrentUser), done: true},
			{text: fmt.Sprintf("Fetching open PRs from %s", a.Repo)},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchPRListCmd(m.app.Repo), m.input.Focus())
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
	}

	if m.bulkApproveActive {
		return m.updateBulkApprove(msg)
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

	// Diff overlay: full-screen DiffView
	if m.overlay == overlayDiff {
		if m.modal.active {
			title := "  Add comment  (Enter submit · Alt+Enter newline · Esc cancel)"
			if m.modal.isInline {
				title = fmt.Sprintf("  Comment on %s:%d  (Enter submit · Alt+Enter newline · Esc cancel)", m.modal.filePath, m.modal.fileLine)
			}
			modalContent := lipgloss.JoinVertical(lipgloss.Left,
				style.PanelTitleFocused.Render(title),
				lipgloss.NewStyle().Padding(0, 1).Render(m.modal.textarea.View()),
			)
			return lipgloss.JoinVertical(lipgloss.Left,
				m.diffView.ViewWithModal(modalContent),
				m.renderDiffFooter(),
			)
		}
		return lipgloss.JoinVertical(lipgloss.Left,
			m.diffView.ViewContent(),
			m.renderDiffFooter(),
		)
	}

	// Bulk approve scene
	if m.inBulkApprove() {
		return m.bulkApprove.View()
	}

	// Conversation view: viewport + input + footer
	width := m.width
	if width == 0 {
		width = 80
	}

	vpContent := lipgloss.JoinHorizontal(lipgloss.Top, m.viewport.View(), style.RenderScrollbar(m.viewport))

	// Claude Code-style input area with horizontal rules
	ruleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	rule := ruleStyle.Render(strings.Repeat("─", width))
	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	inputLine := promptStyle.Render("❯ ") + m.input.View()
	inputArea := lipgloss.JoinVertical(lipgloss.Left, rule, inputLine, rule)

	parts := []string{vpContent, inputArea}
	if m.confirm != nil {
		parts = append(parts[:1], m.renderConfirmBanner(width))
		parts = append(parts, inputArea)
	} else if m.pendingPerm != nil {
		parts = append(parts[:1], m.renderPermBanner(width))
		parts = append(parts, inputArea)
	}
	parts = append(parts, m.renderFooter())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) inBulkApprove() bool {
	return m.bulkApproveActive
}

var permBannerStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("214")).
	Foreground(lipgloss.Color("230")).
	Padding(0, 1)

func (m Model) renderConfirmBanner(width int) string {
	inner := fmt.Sprintf("%s\n[y] confirm   [n/esc] cancel", m.confirm.description)
	maxW := width - 4
	if maxW < 20 {
		maxW = 20
	}
	return permBannerStyle.Width(maxW).Render(inner)
}

func (m Model) renderPermBanner(width int) string {
	inner := fmt.Sprintf("Claude wants to: %s\n[y] allow   [n] deny", m.pendingPerm.description)
	maxW := width - 4
	if maxW < 20 {
		maxW = 20
	}
	return permBannerStyle.Width(maxW).Render(inner)
}

func (m Model) renderFooter() string {
	width := m.width
	if width == 0 {
		width = 80
	}
	status := fmt.Sprintf("prx  PR %d/%d", m.current+1, len(m.cards))
	if m.actionStatus != "" && m.actionDone {
		status += fmt.Sprintf("  %s", m.actionStatus)
	} else if m.actionStatus != "" {
		status += fmt.Sprintf("  %s %s", m.spinner.View(), m.actionStatus)
	} else if pending := m.fetching + m.scoring; pending > 0 {
		status += fmt.Sprintf("  %s %d loading", m.spinner.View(), pending)
	}

	hints := "^d diff  ^n/^p nav  /approve  /merge  /comment  ^q quit"
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}

func (m Model) renderDiffFooter() string {
	width := m.width
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

// ---------------------------------------------------------------------------
// Scrollback management
// ---------------------------------------------------------------------------

// buildScrollback rebuilds the viewport content for the current PR.
func (m *Model) buildScrollback() {
	card := m.currentCard()
	if card == nil {
		m.viewport.SetContent("")
		return
	}

	width := m.width - 1 // reserve 1 for scrollbar
	if width < 40 {
		width = 40
	}

	var sections []string

	// 1. Assessment block (includes PR header)
	assessmentStr := scoring.RenderInline(m.buildRenderData(card), width)
	sections = append(sections, assessmentStr)

	// 2. Chat messages + streaming
	if len(card.chatMessages) > 0 || card.Streaming || card.ChatStatus != "" {
		stream := chat.StreamState{
			Active:        card.Streaming,
			Content:       card.StreamContent,
			SpinnerView:   m.spinner.View(),
			ToolCallCount: card.ToolCallCount,
			LastToolCall:  card.LastToolCall,
			Status:        card.ChatStatus,
		}
		chatStr := chat.RenderMessages(card.chatMessages, width, stream)
		if chatStr != "" {
			sections = append(sections, chatStr)
		}
	}

	// 3. Action status
	if m.actionStatus != "" {
		var statusLine string
		if m.actionDone {
			statusLine = "\n  " + m.actionStatus
		} else {
			statusLine = fmt.Sprintf("\n  %s %s", m.spinner.View(), m.actionStatus)
		}
		sections = append(sections, statusLine)
	}

	content := strings.Join(sections, "\n")
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
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

	if m.overlay == overlayDiff {
		footerH := 1
		m.diffView.SetSize(width, height-footerH)
		return
	}

	inputH := m.inputHeight() + 2 // +2 for top and bottom rules
	footerH := 1
	vpH := height - inputH - footerH
	if vpH < 4 {
		vpH = 4
	}
	m.viewport.Width = width - 1 // reserve for scrollbar
	m.viewport.Height = vpH
	m.input.SetWidth(width - 4)
}

const maxInputLines = 5

// inputHeight returns the current textarea height in lines.
func (m *Model) inputHeight() int {
	return m.input.Height()
}

// updateInputHeight adjusts textarea height based on content, 1-5 lines.
func (m *Model) updateInputHeight() {
	content := m.input.Value()
	lines := strings.Count(content, "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > maxInputLines {
		lines = maxInputLines
	}
	if lines != m.input.Height() {
		m.input.SetHeight(lines)
		m.resizeLayout()
	}
}

// ---------------------------------------------------------------------------
// Startup log rendering & helpers
// ---------------------------------------------------------------------------

var (
	startupTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	startupDoneStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	startupCheckStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

func (m Model) renderStartupLog() string {
	var b strings.Builder
	width := m.width
	if width == 0 {
		width = 80
	}
	title := " prx "
	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	pad := (width - len(title)) / 2
	if pad < 2 {
		pad = 2
	}
	line := lineStyle.Render(strings.Repeat("─", pad)) + startupTitleStyle.Render(title) + lineStyle.Render(strings.Repeat("─", width-pad-len(title)))
	b.WriteString("\n" + line + "\n\n")

	entries := m.startupLog
	maxVisible := 12
	start := 0
	if len(entries) > maxVisible {
		start = len(entries) - maxVisible
	}
	for i := start; i < len(entries); i++ {
		entry := entries[i]
		if entry.done {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				startupCheckStyle.Render("✓"),
				startupDoneStyle.Render(entry.text)))
		} else {
			b.WriteString(fmt.Sprintf("  %s %s\n", m.spinner.View(), entry.text))
		}
	}
	b.WriteString("\n  Press q to quit.\n")
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func (m *Model) logDone() {
	for i := len(m.startupLog) - 1; i >= 0; i-- {
		if !m.startupLog[i].done {
			m.startupLog[i].done = true
			return
		}
	}
}

func (m *Model) logStep(text string) {
	m.logDone()
	m.startupLog = append(m.startupLog, startupEntry{text: text})
}

// ---------------------------------------------------------------------------
// Model helpers
// ---------------------------------------------------------------------------

func (m Model) currentCard() *PRCard {
	if m.current < len(m.cards) {
		return m.cards[m.current]
	}
	return nil
}

func (m Model) isOwnPR(card *PRCard) bool {
	return card.PR.Author == m.app.CurrentUser
}

func (m *Model) navigatePR(delta int) {
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
	m.actionStatus = ""
	m.actionDone = false
	m.loadCurrentDiff()
	m.buildScrollback()
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
		BodyExpanded:     false, // no longer collapsible in conversation view
		ScoringToolCount: card.ScoringToolCount,
		ScoringLastTool:  card.ScoringLastTool,
		ScoringStatus:    card.ScoringStatus,
	}
}

func (m *Model) cleanupWorktrees() tea.Cmd {
	if m.permCleanup != nil {
		m.permCleanup()
		m.permCleanup = nil
	}
	var cmds []tea.Cmd
	for _, card := range m.cards {
		if card.worktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(m.app.RepoDir, card.worktreePath))
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
}
