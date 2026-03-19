package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
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

const defaultAssessmentLines = 10

type Model struct {
	app              *app.App
	cards            []*PRCard
	total            int
	fetching         int // PRs whose details are still being fetched
	scoring          int // PRs whose assessments are still in progress
	current          int
	scene            scene
	focus            focus
	diffView         diff.DiffView
	chatView         chat.View
	chatActive       bool // true when chat panel is shown instead of diff
	assessmentPanel  scoring.Panel
	spinner          spinner.Model
	modal            commentModal
	confirm          *confirmDialog
	actionStatus     string // e.g. "Merging…", "Approving…"
	bodyExpanded     bool
	program          *tea.Program
	err              error
	width            int
	height           int
	permSocketPath   string
	permCleanup      func()
	pendingPerm      *permRequestMsg
	bulkApprove      bulkapprove.Model
	bulkApproveShown bool // true once auto-shown this session
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return Model{
		app:             a,
		spinner:         s,
		diffView:        diff.NewDiffView(80, 20),
		chatView:        chat.New(80, 20),
		assessmentPanel: scoring.New(80, defaultAssessmentLines),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchPRListCmd(m.app.Repo))
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

	if m.scene == sceneBulkApprove {
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

	if m.scene == sceneBulkApprove {
		return m.bulkApprove.View()
	}

	if len(m.cards) == 0 {
		if m.fetching == 0 && m.total == 0 {
			return fmt.Sprintf("\n  %s Fetching PRs for %s...\n\n  Press q to quit.\n",
				m.spinner.View(), m.app.Repo)
		}
		if m.fetching > 0 {
			fetched := m.total - m.fetching
			return fmt.Sprintf("\n  %s Loading PRs (%d/%d)...\n\n  Press q to quit.\n",
				m.spinner.View(), fetched, m.total)
		}
		return "\n  No open PRs found.\n\n  Press q to quit.\n"
	}

	width := m.width
	if width == 0 {
		width = 80
	}

	var assessmentTitle string
	if m.focus == focusAssessment {
		var hint string
		if card := m.currentCard(); card != nil && m.isOwnPR(card) {
			hint = "m merge  c comment  n/p navigate  ? chat  tab to diff"
		} else {
			hint = "a approve  r request-changes  c comment  n/p navigate  ? chat  tab to diff"
		}
		assessmentTitle = style.RenderPanelTitle("Assessment", hint, true, width)
	} else {
		assessmentTitle = style.RenderPanelTitle("Assessment", "tab to focus", false, width)
	}
	assessmentContent := lipgloss.JoinHorizontal(lipgloss.Top, m.assessmentPanel.ViewContent(), style.RenderScrollbar(m.assessmentPanel.Viewport()))
	assessmentPanel := lipgloss.JoinVertical(lipgloss.Left, assessmentTitle, assessmentContent)

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
			assessmentPanel,
			m.diffView.ViewWithModal(modalContent),
			m.renderFooter(),
		)
	}

	showChat := m.chatActive && (m.focus == focusChat || m.focus == focusAssessment)
	tabBar := m.renderTabBar(width, !showChat, showChat)
	var content string
	if showChat {
		content = m.chatView.ViewContent()
	} else {
		content = m.diffView.ViewContent()
	}
	parts := []string{assessmentPanel, tabBar, content}
	if m.confirm != nil {
		parts = append(parts, m.renderConfirmBanner(width))
	} else if m.pendingPerm != nil {
		parts = append(parts, m.renderPermBanner(width))
	}
	parts = append(parts, m.renderFooter())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) renderTabBar(width int, diffActive, chatActive bool) string {
	focused := m.focus == focusDiff || m.focus == focusChat

	var sep string
	var lineCol, activeCol, inactiveCol lipgloss.Color
	if focused {
		sep = "━"
		lineCol = lipgloss.Color("75")
		activeCol = lipgloss.Color("255")
		inactiveCol = lipgloss.Color("250")
	} else {
		sep = "─"
		lineCol = lipgloss.Color("244")
		activeCol = lipgloss.Color("250")
		inactiveCol = lipgloss.Color("244")
	}
	lineS := lipgloss.NewStyle().Foreground(lineCol)

	diffName := m.diffView.TitleWithCommentCount()
	diffS := lipgloss.NewStyle().Foreground(inactiveCol)
	if diffActive {
		diffS = lipgloss.NewStyle().Foreground(activeCol).Bold(focused)
	}

	chatName := m.chatView.TabName()
	chatS := lipgloss.NewStyle().Foreground(inactiveCol)
	if chatActive {
		chatS = lipgloss.NewStyle().Foreground(activeCol).Bold(focused)
	}

	left := lineS.Render(sep+sep+" ") +
		diffS.Render(diffName) +
		lineS.Render(" "+sep+sep+" ") +
		chatS.Render(chatName) +
		lineS.Render(" ")

	var hint string
	if diffActive && m.focus == focusDiff {
		hint = "←/→ collapse/expand  [/] file  {/} hunk  c comment"
	} else if chatActive && m.focus == focusChat {
		hint = "enter send  alt+enter newline  esc stop/close"
	} else if !m.chatActive {
		hint = "? chat  tab to focus"
	} else {
		hint = "tab to focus"
	}

	hintS := lipgloss.NewStyle().Foreground(lineCol).Faint(!focused)
	right := hintS.Render(hint) + lineS.Render(" "+sep+sep)

	fill := width - lipgloss.Width(left) - lipgloss.Width(right)
	if fill < 0 {
		fill = 0
	}
	return left + lineS.Render(strings.Repeat(sep, fill)) + right
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
	if m.actionStatus != "" {
		status += fmt.Sprintf("  %s %s", m.spinner.View(), m.actionStatus)
	} else if pending := m.fetching + m.scoring; pending > 0 {
		status += fmt.Sprintf("  %s %d loading", m.spinner.View(), pending)
	}
	var hints string
	if m.chatActive && m.focus == focusChat {
		hints = "esc stop/close  |  tab  |  ctrl+n/p nav  |  ctrl+r refresh  |  ctrl+c quit"
	} else if m.chatActive {
		hints = "? chat  |  tab  |  ctrl+n/p nav  |  ctrl+r refresh  |  q quit"
	} else {
		hints = "? chat  |  q quit  |  tab  |  p/n nav  |  ctrl+b bulk  |  ctrl+r refresh"
	}
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}

// ---------------------------------------------------------------------------
// Model helpers (small, used across handlers/keys/view)
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
	m.bodyExpanded = false
	m.actionStatus = ""
	m.assessmentPanel.GotoTop()
	m.loadCurrentDiff()
	m.rebuildAssessment()
	if m.chatActive {
		if card := m.currentCard(); card != nil {
			m.chatView.SetMessages(card.chatMessages)
			m.chatView.Streaming = false
			m.chatView.StreamContent = ""
		}
	}
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

func (m *Model) assessmentHeight() int {
	maxH := m.height * 2 / 5
	if maxH < defaultAssessmentLines {
		maxH = defaultAssessmentLines
	}
	contentH := m.assessmentPanel.ContentHeight() + 1 // +1 for title bar
	if contentH < defaultAssessmentLines {
		contentH = defaultAssessmentLines
	}
	if contentH > maxH {
		return maxH
	}
	return contentH
}

func (m *Model) resizeDiffView() {
	footerH := 1
	borderH := 1
	aH := m.assessmentHeight()
	diffH := m.height - footerH - aH - borderH
	if diffH < 4 {
		diffH = 4
	}
	m.diffView.SetSize(m.width, diffH)
	m.chatView.SetSize(m.width, diffH)
	w := m.width
	if w == 0 {
		w = 80
	}
	m.assessmentPanel.SetSize(w, aH)
}

func (m *Model) rebuildAssessment() {
	if m.current >= len(m.cards) {
		return
	}
	card := m.cards[m.current]
	m.assessmentPanel.SetContent(scoring.RenderData{
		PR:           card.PR,
		Assessment:   card.Assessment,
		Score:        card.WeightedScore,
		Verdict:      card.Verdict,
		Scoring:      card.Scoring,
		ScoringErr:   card.ScoringErr,
		SpinnerView:  m.spinner.View(),
		Criteria:     m.app.Config.Criteria,
		BodyExpanded: m.bodyExpanded,
	})
	m.resizeDiffView()
}

// refreshSpinner updates only the spinner frame in the assessment panel
// without triggering a full resize/rebuild. Called on every spinner tick.
func (m *Model) refreshSpinner() {
	if m.current >= len(m.cards) {
		return
	}
	card := m.cards[m.current]
	m.assessmentPanel.SetContent(scoring.RenderData{
		PR:           card.PR,
		Assessment:   card.Assessment,
		Score:        card.WeightedScore,
		Verdict:      card.Verdict,
		Scoring:      card.Scoring,
		ScoringErr:   card.ScoringErr,
		SpinnerView:  m.spinner.View(),
		Criteria:     m.app.Config.Criteria,
		BodyExpanded: m.bodyExpanded,
	})
	// Intentionally skip resizeDiffView() — just update content, not layout
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
