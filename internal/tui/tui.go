package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/tui/chat"
	"github.com/sleuth-io/prx/internal/tui/diff"
	"github.com/sleuth-io/prx/internal/tui/scoring"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	footerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)
)

const defaultAssessmentLines = 10

type Model struct {
	app            *app.App
	cards          []*PRCard
	total          int
	fetching       int // PRs whose details are still being fetched
	scoring        int // PRs whose assessments are still in progress
	current        int
	focus          focus
	diffView       diff.DiffView
	chatView       chat.View
	chatActive     bool // true when chat panel is shown instead of diff
	assessmentPanel scoring.Panel
	spinner        spinner.Model
	modal          commentModal
	actionStatus   string // e.g. "Merging…", "Approving…"
	bodyExpanded   bool
	program        *tea.Program
	err            error
	width          int
	height         int
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case SetProgramMsg:
		m.program = msg.Program
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeDiffView()
		return m, nil

	case tea.KeyMsg:
		if m.modal.active {
			return m.handleModalKey(msg)
		}

		switch msg.String() {
		case "q":
			if m.focus == focusChat {
				break // let q go to textarea
			}
			return m, m.cleanupWorktrees()
		case "ctrl+c":
			return m, m.cleanupWorktrees()
		case "?":
			if m.focus == focusChat {
				break
			}
			return m, m.activateChat()
		case "tab":
			if m.chatActive {
				switch m.focus {
				case focusAssessment:
					m.focus = focusDiff
					m.diffView.Focused = true
					m.chatView.Focused = false
					m.chatView.Blur()
				case focusDiff:
					m.focus = focusChat
					m.diffView.Focused = false
					m.chatView.Focused = true
					return m, m.chatView.Focus()
				default: // focusChat
					m.focus = focusAssessment
					m.chatView.Focused = false
					m.chatView.Blur()
				}
			} else {
				if m.focus == focusAssessment {
					m.focus = focusDiff
					m.diffView.Focused = true
				} else {
					m.focus = focusAssessment
					m.diffView.Focused = false
				}
			}
			return m, nil
		case "left":
			if m.focus == focusDiff {
				m.diffView.CollapseCurrentFile()
				return m, nil
			}
		case "right":
			if m.focus == focusDiff {
				m.diffView.ExpandCurrentFile()
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+n":
			m.navigatePR(1)
			return m, nil
		case "ctrl+p":
			m.navigatePR(-1)
			return m, nil
		}

		if m.focus == focusChat {
			switch msg.String() {
			case "esc":
				if m.chatView.Streaming {
					if card := m.currentCard(); card != nil && card.chatCancel != nil {
						card.chatCancel()
						card.chatCancel = nil
					}
					return m, nil
				}
				m.chatActive = false
				m.chatView.Focused = false
				m.chatView.Blur()
				m.focus = focusDiff
				m.diffView.Focused = true
				return m, nil
			case "enter":
				return m, m.sendChatMessage()
			}
			var cmd tea.Cmd
			m.chatView, cmd = m.chatView.Update(msg)
			return m, cmd
		}

		if m.focus == focusAssessment {
			switch msg.String() {
			case "n":
				m.navigatePR(1)
			case "p":
				m.navigatePR(-1)
			case "j", "down":
				if m.assessmentPanel.AtBottom() {
					m.focus = focusDiff
					m.diffView.Focused = true
					m.chatView.Focused = false
					m.chatView.Blur()
				} else {
					m.assessmentPanel.ScrollDown(1)
				}
			case "k", "up":
				m.assessmentPanel.ScrollUp(1)
			case "a":
				if card := m.currentCard(); card != nil && !card.Scoring && !m.isOwnPR(card) {
					m.actionStatus = "Approving\u2026"
					return m, approveCmd(m.app.Repo, card.PR.Number)
				}
			case "m":
				if card := m.currentCard(); card != nil && !card.Scoring && m.isOwnPR(card) {
					m.actionStatus = "Merging\u2026"
					return m, mergeCmd(m.app.Repo, card.PR.Number)
				}
			case "r":
				if card := m.currentCard(); card != nil && !card.Scoring && card.Assessment != nil && !m.isOwnPR(card) {
					m.actionStatus = "Requesting changes\u2026"
					return m, requestChangesCmd(m.app.Repo, card.PR.Number, card.Assessment.ReviewNotes)
				}
			case "s":
				m.navigatePR(1)
			case "right":
				if !m.bodyExpanded {
					m.bodyExpanded = true
					m.rebuildAssessment()
				}
			case "left":
				if m.bodyExpanded {
					m.bodyExpanded = false
					m.rebuildAssessment()
				}
			case "c":
				if card := m.currentCard(); card != nil {
					m.openCommentModal(card, false, "", 0)
					return m, m.modal.textarea.Focus()
				}
			}
			return m, nil
		}

		if (msg.String() == "k" || msg.String() == "up") && m.diffView.AtTop() {
			m.focus = focusAssessment
			m.diffView.Focused = false
			return m, nil
		}
		if msg.String() == "c" {
			if card := m.currentCard(); card != nil {
				path, line := m.diffView.CurrentLineTarget()
				m.openCommentModal(card, path != "", path, line)
				return m, m.modal.textarea.Focus()
			}
		}
		var cmd tea.Cmd
		m.diffView, cmd = m.diffView.Update(msg)
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.chatView.Streaming || m.chatView.Status != "" {
			m.chatView.SpinnerView = m.spinner.View()
			m.chatView.RebuildViewport()
		}
		// Update the assessment panel spinner if the current card is scoring.
		if card := m.currentCard(); card != nil && card.Scoring {
			m.refreshSpinner()
		}
		// Footer spinner animates automatically via m.spinner.View() in renderFooter()
		return m, cmd

	case prListFetchedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.total = len(msg.rawPRs)
		m.fetching = len(msg.rawPRs)
		if m.total == 0 {
			return m, nil
		}
		cmds := make([]tea.Cmd, len(msg.rawPRs))
		for i, raw := range msg.rawPRs {
			cmds[i] = fetchPRDetailsCmd(raw, m.app)
		}
		return m, tea.Batch(cmds...)

	case prDetailsFetchedMsg:
		m.fetching--
		if msg.err != nil {
			logger.Error("fetching PR details: %v", msg.err)
			return m, nil
		}
		pr := msg.pr
		card := &PRCard{PR: pr, Scoring: true}
		// Insert sorted by PR number descending (newest first)
		idx := 0
		for idx < len(m.cards) && m.cards[idx].PR.Number > pr.Number {
			idx++
		}
		m.cards = append(m.cards, nil)
		copy(m.cards[idx+1:], m.cards[idx:])
		m.cards[idx] = card
		// Adjust current index if insertion was before it
		if idx <= m.current && len(m.cards) > 1 {
			m.current++
		}
		m.scoring++

		if len(m.cards) == 1 {
			m.rebuildAssessment()
		}

		return m, tea.Batch(scorePRCmd(pr, m.app), parseDiffCmd(pr))

	case prDiffParsedMsg:
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.parsedFiles = msg.files
				applyHunkAnnotations(card)
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			m.diffView.SetParsedContent(card.parsedFiles, card.PR)
		}
		return m, nil

	case prScoredMsg:
		m.scoring--
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.Scoring = false
				if msg.err != nil {
					card.ScoringErr = msg.err
				} else {
					card.Assessment = msg.assessment
					card.WeightedScore = scoring.WeightedScore(msg.assessment, m.app.Config.Criteria)
					card.Verdict = scoring.ComputeVerdict(card.WeightedScore, m.app.Config.Thresholds)
					applyHunkAnnotations(card)
				}
				src := "claude"
				if msg.fromCache {
					src = "cache"
				}
				logger.Info("PR #%d scored via %s: %.1f", msg.prNumber, src, card.WeightedScore)
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			m.rebuildAssessment()
			// Refresh diff view if annotations were just applied
			if card.parsedFiles != nil {
				m.diffView.SetParsedContent(card.parsedFiles, card.PR)
			}
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.actionStatus = fmt.Sprintf("%s failed: %s", msg.action, msg.err)
			return m, nil
		}
		m.actionStatus = ""
		if m.current < len(m.cards)-1 {
			m.current++
			m.loadCurrentDiff()
			m.rebuildAssessment()
		}
		return m, nil

	case commentSubmittedMsg:
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				if msg.err == nil {
					rc := github.ReviewComment{
						Author: m.app.CurrentUser,
						Body:   msg.body,
						Path:   msg.filePath,
						Line:   msg.fileLine,
					}
					if msg.isInline {
						card.PR.InlineComments = append(card.PR.InlineComments, rc)
					} else {
						card.PR.Comments = append(card.PR.Comments, rc)
					}
				}
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			if msg.err == nil {
				m.diffView.ConfirmComment(msg.pendingItem)
			} else {
				m.diffView.RemoveComment(msg.pendingItem)
			}
		}
		return m, nil

	case chatWorktreeReadyMsg:
		m.chatView.Status = ""
		m.chatView.RebuildViewport()
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				if msg.err != nil {
					logger.Error("worktree error for PR #%d: %v", msg.prNumber, msg.err)
				} else {
					card.worktreePath = msg.path
				}
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && msg.err == nil {
			if m.chatActive && m.chatView.Streaming {
				ctx, cancel := context.WithCancel(context.Background())
				card.chatCancel = cancel
				return m, sendChatCmd(ctx, msg.path, card.PR, card.Assessment, card.chatMessages, card.chatContext, m.app.Config.Review.Model, m.program)
			}
		}
		return m, nil

	case chatStatusMsg:
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.Status = msg.status
			m.chatView.RebuildViewport()
		}
		return m, nil

	case chatToolCallMsg:
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.ToolCallCount = msg.count
			m.chatView.LastToolCall = msg.lastTool
			m.chatView.RebuildViewport()
		}
		return m, nil

	case chatTokenMsg:
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.AppendToken(msg.token)
		}
		return m, nil

	case chatDoneMsg:
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.chatCancel = nil
				if msg.err != nil {
					logger.Error("chat error for PR #%d: %v", msg.prNumber, msg.err)
					card.chatMessages = append(card.chatMessages, chat.Message{
						Role:    "assistant",
						Content: fmt.Sprintf("Error: %v", msg.err),
					})
				} else if msg.fullResponse != "" {
					card.chatMessages = append(card.chatMessages, chat.Message{
						Role:    "assistant",
						Content: msg.fullResponse,
					})
				}
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.FinishStream(msg.fullResponse)
			m.chatView.SetMessages(card.chatMessages)
		}
		return m, nil
	}

	return m, nil
}

func (m *Model) handleModalKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		prev := m.modal.prevFocus
		m.modal = commentModal{}
		m.focus = prev
		m.diffView.Focused = (prev == focusDiff)
		m.resizeDiffView()
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
		prev := m.modal.prevFocus
		rc := github.ReviewComment{
			Author: m.app.CurrentUser,
			Body:   body,
			Path:   filePath,
			Line:   fileLine,
		}
		pendingItem := m.diffView.AddPendingComment(rc)
		m.modal = commentModal{}
		m.focus = prev
		m.diffView.Focused = (prev == focusDiff)
		m.resizeDiffView()
		if isInline {
			return *m, postInlineCommentCmd(m.app.Repo, card.PR.Number,
				commitSHA, filePath, fileLine, body, pendingItem)
		}
		return *m, postGlobalCommentCmd(m.app.Repo, card.PR.Number, body, pendingItem)
	default:
		var cmd tea.Cmd
		m.modal.textarea, cmd = m.modal.textarea.Update(msg)
		return *m, cmd
	}
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

func (m *Model) navigatePR(delta int) {
	next := m.current + delta
	if next < 0 || next >= len(m.cards) {
		return
	}
	m.current = next
	m.bodyExpanded = false
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

func (m *Model) openCommentModal(card *PRCard, isInline bool, path string, line int) {
	ta := textarea.New()
	ta.Placeholder = "Write your comment..."
	ta.SetWidth(m.width - 4)
	ta.SetHeight(4)
	prev := m.focus
	m.modal = commentModal{
		active:    true,
		isInline:  isInline,
		filePath:  path,
		fileLine:  line,
		commitSHA: card.PR.HeadSHA,
		prevFocus: prev,
		textarea:  ta,
	}
	m.focus = focusModal
	m.resizeDiffView()
}

func (m *Model) activateChat() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}

	path, line := m.diffView.CurrentLineTarget()
	if path != "" {
		card.chatContext = &ai.DiffContext{File: path, Line: line}
	} else {
		card.chatContext = nil
	}

	m.chatActive = true
	m.focus = focusChat
	m.diffView.Focused = false
	m.chatView.Focused = true
	m.chatView.SetContext(card.chatContext)
	m.chatView.WelcomeText = buildChatWelcome(card, card.chatContext)
	m.chatView.SetMessages(card.chatMessages)
	m.resizeDiffView()

	var cmds []tea.Cmd
	cmds = append(cmds, m.chatView.Focus())

	if card.worktreePath == "" {
		m.chatView.Status = "Creating worktree..."
		cmds = append(cmds, createWorktreeCmd(m.app.RepoDir, card.PR.HeadRefName, card.PR.Number))
	}

	return tea.Batch(cmds...)
}

func buildChatWelcome(card *PRCard, ctx *ai.DiffContext) string {
	var sb strings.Builder

	if card.Assessment != nil {
		var topName, topReason string
		topScore := 0
		for name, f := range card.Assessment.Factors {
			if f.Score > topScore {
				topScore = f.Score
				topName = name
				topReason = f.Reason
			}
		}
		if topScore >= 3 {
			fmt.Fprintf(&sb, "Highest risk: %s (%d/5) \u2014 %s\n\n", topName, topScore, topReason)
		}
	}

	if ctx != nil && ctx.File != "" {
		if ctx.Line > 0 {
			fmt.Fprintf(&sb, "You're looking at %s:%d. ", ctx.File, ctx.Line)
		} else {
			fmt.Fprintf(&sb, "You're looking at %s. ", ctx.File)
		}
	}

	sb.WriteString("What questions do you have?")
	return sb.String()
}

func (m *Model) sendChatMessage() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}
	body := m.chatView.InputValue()
	if body == "" {
		return nil
	}
	if m.chatView.Streaming {
		return nil
	}

	m.chatView.ResetInput()
	card.chatMessages = append(card.chatMessages, chat.Message{Role: "user", Content: body})
	m.chatView.SetMessages(card.chatMessages)
	m.chatView.Streaming = true
	m.chatView.Status = "Starting Claude..."

	if card.worktreePath != "" {
		ctx, cancel := context.WithCancel(context.Background())
		card.chatCancel = cancel
		return sendChatCmd(ctx, card.worktreePath, card.PR, card.Assessment, card.chatMessages, card.chatContext, m.app.Config.Review.Model, m.program)
	}
	return nil
}

func (m *Model) cleanupWorktrees() tea.Cmd {
	var cmds []tea.Cmd
	for _, card := range m.cards {
		if card.worktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(m.app.RepoDir, card.worktreePath))
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\nError: %v\n\nPress q to quit.\n", m.err)
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
			hint = " m merge  ←/→ description  c comment  s skip  n/p navigate  j/k scroll  tab to diff"
		} else {
			hint = " a approve  r request-changes  ←/→ description  c comment  s skip  n/p navigate  j/k scroll  tab to diff"
		}
		assessmentTitle = style.PanelTitleFocused.Render("Assessment") + style.DimPanelHint(hint, style.PanelTitleFocused, width, "Assessment")
	} else {
		assessmentTitle = style.PanelTitleBlurred.Render("Assessment") + style.DimPanelHint(" tab to focus", style.PanelTitleBlurred, width, "Assessment")
	}
	assessmentContent := lipgloss.JoinHorizontal(lipgloss.Top, m.assessmentPanel.ViewContent(), style.RenderScrollbar(m.assessmentPanel.Viewport()))
	assessmentPanel := lipgloss.JoinVertical(lipgloss.Left, assessmentTitle, assessmentContent)

	if m.modal.active {
		title := "  Add comment  (Enter submit \u00b7 Alt+Enter newline \u00b7 Esc cancel)"
		if m.modal.isInline {
			title = fmt.Sprintf("  Comment on %s:%d  (Enter submit \u00b7 Alt+Enter newline \u00b7 Esc cancel)", m.modal.filePath, m.modal.fileLine)
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

	if m.chatActive {
		showChat := m.focus == focusChat || m.focus == focusAssessment
		tabBar := m.renderTabBar(width, !showChat, showChat)

		var content string
		if showChat {
			content = m.chatView.ViewContent()
		} else {
			content = m.diffView.ViewContent()
		}
		return lipgloss.JoinVertical(lipgloss.Left,
			assessmentPanel,
			tabBar,
			content,
			m.renderFooter(),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		assessmentPanel,
		m.diffView.View(),
		m.renderFooter(),
	)
}

var (
	tabActive = lipgloss.NewStyle().
			Bold(true).
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1)
	tabInactive = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("243")).
			Padding(0, 1)
	tabHint = lipgloss.NewStyle().
		Background(lipgloss.Color("237")).
		Foreground(lipgloss.Color("243")).
		Faint(true)
)

func (m Model) renderTabBar(width int, diffActive, chatActive bool) string {
	diffTabName := m.diffView.TitleWithCommentCount()
	var diffTab, chatTab string
	if diffActive {
		diffTab = tabActive.Render(diffTabName)
	} else {
		diffTab = tabInactive.Render(diffTabName)
	}

	chatName := m.chatView.TabName()
	if chatActive {
		chatTab = tabActive.Render(chatName)
	} else {
		chatTab = tabInactive.Render(chatName)
	}

	tabs := diffTab + " " + chatTab
	tabsW := lipgloss.Width(tabs)

	var hint string
	if diffActive && m.focus == focusDiff {
		hint = "\u2190/\u2192 collapse/expand  [/] file  {/} hunk  c comment  ? chat"
	} else if chatActive && m.focus == focusChat {
		hint = "enter send  alt+enter newline  esc stop/close"
	}

	remaining := width - tabsW
	if remaining > 2 && hint != "" {
		hintRendered := tabHint.Render(hint)
		hintW := lipgloss.Width(hintRendered)
		gap := remaining - hintW
		if gap > 0 {
			tabs += tabHint.Render(strings.Repeat(" ", gap)) + hintRendered
		} else {
			tabs += hintRendered
		}
	} else if remaining > 0 {
		tabs += tabHint.Render(strings.Repeat(" ", remaining))
	}

	return tabs
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
		hints = "esc stop/close  |  tab  |  ctrl+n/p nav  |  ctrl+c quit"
	} else if m.chatActive {
		hints = "? chat  |  tab  |  j/k scroll  |  ctrl+n/p nav  |  q quit"
	} else {
		hints = "? chat  |  q quit  |  tab  |  j/k scroll  |  p/n nav  |  ctrl+n/p nav anywhere"
	}
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}
