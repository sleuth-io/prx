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
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

var (
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1).
			Bold(true)

	footerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)
)

const assessmentLines = 10

type Model struct {
	app          *app.App
	cards        []*PRCard
	total        int
	fetching     int // PRs whose details are still being fetched
	scoring      int // PRs whose assessments are still in progress
	current      int
	focus        focus
	diffView     DiffView
	assessmentVP viewport.Model
	spinner      spinner.Model
	modal         commentModal
	actionStatus  string // e.g. "Merging…", "Approving…" — shown in footer while action runs
	err           error
	width         int
	height        int
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return Model{
		app:          a,
		spinner:      s,
		diffView:     NewDiffView(80, 20),
		assessmentVP: viewport.New(80, assessmentLines),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchPRListCmd(m.app.Repo))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeDiffView()
		return m, nil

	case tea.KeyMsg:
		if m.modal.active {
			switch msg.String() {
			case "esc":
				prev := m.modal.prevFocus
				m.modal = commentModal{}
				m.focus = prev
				m.diffView.Focused = (prev == focusDiff)
				m.resizeDiffView()
				return m, nil
			case "ctrl+s":
				body := strings.TrimSpace(m.modal.textarea.Value())
				if body == "" {
					return m, nil
				}
				card := m.currentCard()
				if card == nil {
					return m, nil
				}
				// Capture modal state before clearing it.
				isInline := m.modal.isInline
				filePath := m.modal.filePath
				fileLine := m.modal.fileLine
				commitSHA := m.modal.commitSHA
				prev := m.modal.prevFocus
				// Add optimistic pending comment immediately.
				rc := github.ReviewComment{
					Author: m.app.CurrentUser,
					Body:   body,
					Path:   filePath,
					Line:   fileLine,
				}
				pendingItem := m.diffView.AddPendingComment(rc)
				// Close modal.
				m.modal = commentModal{}
				m.focus = prev
				m.diffView.Focused = (prev == focusDiff)
				m.resizeDiffView()
				if isInline {
					return m, postInlineCommentCmd(m.app.Repo, card.PR.Number,
						commitSHA, filePath, fileLine, body, pendingItem)
				}
				return m, postGlobalCommentCmd(m.app.Repo, card.PR.Number, body, pendingItem)
			default:
				var cmd tea.Cmd
				m.modal.textarea, cmd = m.modal.textarea.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.focus == focusAssessment {
				m.focus = focusDiff
				m.diffView.Focused = true
			} else {
				m.focus = focusAssessment
				m.diffView.Focused = false
			}
			return m, nil
		case "left":
			m.diffView.CollapseCurrentFile()
			return m, nil
		case "right":
			m.diffView.ExpandCurrentFile()
			return m, nil
		}

		// ctrl+n / ctrl+p navigate PRs from any focus.
		switch msg.String() {
		case "ctrl+n":
			m.navigatePR(1)
			return m, nil
		case "ctrl+p":
			m.navigatePR(-1)
			return m, nil
		}

		if m.focus == focusAssessment {
			switch msg.String() {
			case "n":
				m.navigatePR(1)
			case "p":
				m.navigatePR(-1)
			case "j", "down":
				if m.assessmentVP.AtBottom() {
					m.focus = focusDiff
					m.diffView.Focused = true
				} else {
					m.assessmentVP.ScrollDown(1)
				}
			case "k", "up":
				m.assessmentVP.ScrollUp(1)
			case "a":
				if card := m.currentCard(); card != nil && !card.Scoring && !m.isOwnPR(card) {
					m.actionStatus = "Approving…"
					return m, approveCmd(m.app.Repo, card.PR.Number)
				}
			case "m":
				if card := m.currentCard(); card != nil && !card.Scoring && m.isOwnPR(card) {
					m.actionStatus = "Merging…"
					return m, mergeCmd(m.app.Repo, card.PR.Number)
				}
			case "r":
				if card := m.currentCard(); card != nil && !card.Scoring && card.Assessment != nil && !m.isOwnPR(card) {
					m.actionStatus = "Requesting changes…"
					return m, requestChangesCmd(m.app.Repo, card.PR.Number, card.Assessment.ReviewNotes)
				}
			case "s":
				m.navigatePR(1)
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
		m.cards = append(m.cards, card)
		m.scoring++

		if len(m.cards) == 1 {
			m.rebuildAssessment()
		}

		return m, tea.Batch(scorePRCmd(pr, m.app), parseDiffCmd(pr))

	case prDiffParsedMsg:
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.parsedFiles = msg.files
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
					card.WeightedScore = weightedScore(msg.assessment, m.app)
					card.Verdict = computeVerdict(card.WeightedScore, m.app)
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
		}
		return m, nil

	case actionDoneMsg:
		m.actionStatus = ""
		if msg.err == nil && m.current < len(m.cards)-1 {
			m.current++
			m.loadCurrentDiff()
			m.rebuildAssessment()
		}
		return m, nil

	case commentSubmittedMsg:
		// Always update the card regardless of which PR is currently on screen.
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
		// Update the live diff view only if we're still looking at that PR.
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			if msg.err == nil {
				m.diffView.ConfirmComment(msg.pendingItem)
			} else {
				m.diffView.RemoveComment(msg.pendingItem)
			}
		}
		return m, nil
	}

	return m, nil
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
	m.assessmentVP.GotoTop()
	m.loadCurrentDiff()
	m.rebuildAssessment()
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

func (m *Model) resizeDiffView() {
	footerH := 1
	borderH := 1
	diffH := m.height - footerH - assessmentLines - borderH
	if diffH < 4 {
		diffH = 4
	}
	m.diffView.SetSize(m.width, diffH)
	w := m.width
	if w == 0 {
		w = 80
	}
	m.assessmentVP.Width = w - 1                // reserve 1 char for scrollbar
	m.assessmentVP.Height = assessmentLines - 1 // -1 for the panel title bar
	m.rebuildAssessment()
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
			hint = " m merge  c comment  s skip  n/p navigate  j/k scroll  tab to diff"
		} else {
			hint = " a approve  r request-changes  c comment  s skip  n/p navigate  j/k scroll  tab to diff"
		}
		assessmentTitle = panelTitleFocused.Render("Assessment") + dimPanelHint(hint, panelTitleFocused, width)
	} else {
		assessmentTitle = panelTitleBlurred.Render("Assessment") + dimPanelHint(" tab to focus", panelTitleBlurred, width)
	}
	assessmentContent := lipgloss.JoinHorizontal(lipgloss.Top, m.assessmentVP.View(), renderScrollbar(m.assessmentVP))
	assessmentPanel := lipgloss.JoinVertical(lipgloss.Left, assessmentTitle, assessmentContent)

	if m.modal.active {
		title := "  Add comment  (Ctrl+S submit · Esc cancel)"
		if m.modal.isInline {
			title = fmt.Sprintf("  Comment on %s:%d  (Ctrl+S submit · Esc cancel)", m.modal.filePath, m.modal.fileLine)
		}
		modalContent := lipgloss.JoinVertical(lipgloss.Left,
			panelTitleFocused.Render(title),
			lipgloss.NewStyle().Padding(0, 1).Render(m.modal.textarea.View()),
		)
		return lipgloss.JoinVertical(lipgloss.Left,
			assessmentPanel,
			m.diffView.ViewWithModal(modalContent),
			m.renderFooter(),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		assessmentPanel,
		m.diffView.View(),
		m.renderFooter(),
	)
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
	hints := "q quit  |  tab  |  j/k scroll  |  p/n nav  |  ctrl+n/p nav anywhere"
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}
