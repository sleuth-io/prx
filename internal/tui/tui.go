package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/app"
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
	err          error
	width        int
	height       int
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

		if m.focus == focusAssessment {
			switch msg.String() {
			case "n":
				if m.current < len(m.cards)-1 {
					m.current++
					m.loadCurrentDiff()
					m.rebuildAssessment()
				}
			case "p":
				if m.current > 0 {
					m.current--
					m.loadCurrentDiff()
					m.rebuildAssessment()
				}
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
					return m, approveCmd(m.app.Repo, card.PR.Number)
				}
			case "m":
				if card := m.currentCard(); card != nil && !card.Scoring && m.isOwnPR(card) {
					return m, mergeCmd(m.app.Repo, card.PR.Number)
				}
			case "r":
				if card := m.currentCard(); card != nil && !card.Scoring && card.Assessment != nil && !m.isOwnPR(card) {
					return m, requestChangesCmd(m.app.Repo, card.PR.Number, card.Assessment.ReviewNotes)
				}
			case "s":
				if m.current < len(m.cards)-1 {
					m.current++
					m.loadCurrentDiff()
					m.rebuildAssessment()
				}
			}
			return m, nil
		}

		if (msg.String() == "k" || msg.String() == "up") && m.diffView.AtTop() {
			m.focus = focusAssessment
			m.diffView.Focused = false
			return m, nil
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
		if msg.err == nil && m.current < len(m.cards)-1 {
			m.current++
			m.loadCurrentDiff()
			m.rebuildAssessment()
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
	m.assessmentVP.Width = w - 1 // reserve 1 char for scrollbar
	m.assessmentVP.Height = assessmentLines - 1 // -1 for the panel title bar
	m.rebuildAssessment()
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
			hint = " m merge  s skip  n/p navigate  j/k scroll  tab to diff"
		} else {
			hint = " a approve  r request-changes  s skip  n/p navigate  j/k scroll  tab to diff"
		}
		assessmentTitle = panelTitleFocused.Render("Assessment") + dimPanelHint(hint, panelTitleFocused, width)
	} else {
		assessmentTitle = panelTitleBlurred.Render("Assessment") + dimPanelHint(" tab to focus", panelTitleBlurred, width)
	}
	assessmentContent := lipgloss.JoinHorizontal(lipgloss.Top, m.assessmentVP.View(), renderScrollbar(m.assessmentVP))
	assessmentPanel := lipgloss.JoinVertical(lipgloss.Left, assessmentTitle, assessmentContent)

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
	pending := m.fetching + m.scoring
	if pending > 0 {
		status += fmt.Sprintf("  %s %d loading", m.spinner.View(), pending)
	}
	hints := "q quit  |  tab  |  j/k scroll  |  p/n nav"
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}
