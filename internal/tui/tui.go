package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

// PRCard is a PR — may be in-progress (Scoring=true) or fully assessed.
type PRCard struct {
	PR            *github.PR
	Assessment    *ai.Assessment
	WeightedScore float64
	Verdict       string
	Scoring       bool
	ScoringErr    error
}

type focus int

const (
	focusAssessment focus = iota
	focusDiff
)

// Messages

type prListFetchedMsg struct {
	rawPRs []map[string]any
	err    error
}

type prDetailsFetchedMsg struct {
	pr  *github.PR
	raw map[string]any
	err error
}

type prScoredMsg struct {
	prNumber   int
	assessment *ai.Assessment
	err        error
	fromCache  bool
}

type actionDoneMsg struct {
	pr     int
	action string
	err    error
}

// Styles

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

	verdictApprove = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	verdictReview  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	verdictReject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))

	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
	factorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
)

const assessmentLines = 12

// Model

type Model struct {
	app      *app.App
	cards    []*PRCard
	total    int
	fetching int // PRs whose details are still being fetched
	scoring  int // PRs whose assessments are still in progress
	current  int
	focus    focus
	diffView DiffView
	spinner  spinner.Model
	err      error
	width    int
	height   int
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return Model{
		app:      a,
		spinner:  s,
		diffView: NewDiffView(80, 20),
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
				}
			case "p":
				if m.current > 0 {
					m.current--
					m.loadCurrentDiff()
				}
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
				}
			}
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

		// Show the first card immediately
		if len(m.cards) == 1 {
			m.loadCurrentDiff()
		}

		return m, scorePRCmd(pr, m.app)

	case prScoredMsg:
		m.scoring--
		// Find the card and update it in place
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
		return m, nil

	case actionDoneMsg:
		if msg.err == nil && m.current < len(m.cards)-1 {
			m.current++
			m.loadCurrentDiff()
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
	if m.current < len(m.cards) {
		card := m.cards[m.current]
		if card.PR != nil {
			m.diffView.SetContent(card.PR.Diff, card.PR)
		}
	}
}

func (m *Model) resizeDiffView() {
	headerH := 1
	footerH := 1
	borderH := 1
	diffH := m.height - headerH - footerH - assessmentLines - borderH
	if diffH < 4 {
		diffH = 4
	}
	m.diffView.SetSize(m.width, diffH)
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

	header := m.renderHeader()
	footer := m.renderFooter()
	assessment := m.renderAssessment()
	diff := m.diffView.View()

	return lipgloss.JoinVertical(lipgloss.Left, header, assessment, diff, footer)
}

func (m Model) renderHeader() string {
	status := fmt.Sprintf("prx  PR %d/%d", m.current+1, len(m.cards))
	pending := m.fetching + m.scoring
	if pending > 0 {
		status += fmt.Sprintf("  %s [%d loading...]", m.spinner.View(), pending)
	}
	if m.total > len(m.cards) && pending == 0 {
		status += fmt.Sprintf("  [%d/%d loaded]", len(m.cards), m.total)
	}
	width := m.width
	if width == 0 {
		width = 80
	}
	return headerStyle.Width(width).Render(status)
}

func (m Model) renderFooter() string {
	width := m.width
	if width == 0 {
		width = 80
	}
	return footerStyle.Width(width).Render("q quit  |  j/k scroll diff  |  p/n prev/next PR")
}

func (m Model) renderAssessment() string {
	if m.current >= len(m.cards) {
		return ""
	}
	card := m.cards[m.current]
	var sb strings.Builder

	// Panel title bar
	width := m.width
	if width == 0 {
		width = 80
	}
	if m.focus == focusAssessment {
		var hint string
		if card := m.currentCard(); card != nil && m.isOwnPR(card) {
			hint = " m merge  s skip  n/p navigate  tab to diff"
		} else {
			hint = " a approve  r request-changes  s skip  n/p navigate  tab to diff"
		}
		sb.WriteString(panelTitleFocused.Render("Assessment") + dimPanelHint(hint, panelTitleFocused, width))
	} else {
		sb.WriteString(panelTitleBlurred.Render("Assessment") +
			dimPanelHint(" tab to focus", panelTitleBlurred, width))
	}
	sb.WriteString("\n")

	// Title + meta
	fmt.Fprintf(&sb, "\n  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("#%d", card.PR.Number)),
		card.PR.Title)
	fmt.Fprintf(&sb, "  %s\n",
		dimStyle.Render(fmt.Sprintf("by %s  ·  +%d/-%d  ·  %d files  ·  %s",
			card.PR.Author,
			card.PR.Additions,
			card.PR.Deletions,
			card.PR.FilesChanged,
			card.PR.CreatedAt[:10],
		)))
	fmt.Fprintf(&sb, "  %s\n", renderReviewStatus(card.PR))
	fmt.Fprintf(&sb, "  %s\n\n", renderChecksStatus(card.PR))

	if card.Scoring {
		fmt.Fprintf(&sb, "  %s Scoring with Claude...\n", m.spinner.View())
		return sb.String()
	}

	if card.ScoringErr != nil {
		fmt.Fprintf(&sb, "  %s\n", verdictReject.Render(fmt.Sprintf("Scoring error: %v", card.ScoringErr)))
		return sb.String()
	}

	a := card.Assessment
	bar := scoreBar(card.WeightedScore)
	fmt.Fprintf(&sb, "  Risk  %s  %.1f   %s\n\n", bar, card.WeightedScore, renderVerdict(card.Verdict))
	fmt.Fprintf(&sb, "  %s\n\n",
		factorStyle.Render(fmt.Sprintf(
			"blast:%d  test:%d  sens:%d  cmplx:%d  scope:%d",
			a.BlastRadius.Score, a.TestCoverage.Score,
			a.Sensitivity.Score, a.Complexity.Score, a.ScopeFocus.Score,
		)))
	fmt.Fprintf(&sb, "  %s\n", dimStyle.Render(a.RiskSummary))
	fmt.Fprintf(&sb, "\n  %s\n", sectionStyle.Render("Notes"))
	for _, line := range strings.Split(a.ReviewNotes, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Fprintf(&sb, "  %s\n", line)
	}

	if len(card.PR.Comments) > 0 {
		fmt.Fprintf(&sb, "\n  %s\n", sectionStyle.Render("Comments"))
		for _, c := range card.PR.Comments {
			body := c.Body
			if len(body) > 120 {
				body = body[:120] + "…"
			}
			fmt.Fprintf(&sb, "  %s  %s\n", dimStyle.Render(c.Author+":"), body)
		}
	}

	if len(card.PR.InlineComments) > 0 {
		fmt.Fprintf(&sb, "\n  %s\n", sectionStyle.Render("Inline Comments"))
		for _, c := range card.PR.InlineComments {
			body := c.Body
			if len(body) > 120 {
				body = body[:120] + "…"
			}
			fmt.Fprintf(&sb, "  %s  %s\n",
				dimStyle.Render(fmt.Sprintf("%s on %s:", c.Author, c.Path)),
				body)
		}
	}

	return sb.String()
}

func renderReviewStatus(pr *github.PR) string {
	// Deduplicate reviews per author — keep latest state
	latest := map[string]string{}
	for _, r := range pr.Reviews {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			latest[r.Author] = r.State
		case "COMMENTED":
			if _, exists := latest[r.Author]; !exists {
				latest[r.Author] = r.State
			}
		}
	}

	var parts []string
	for author, state := range latest {
		switch state {
		case "APPROVED":
			parts = append(parts, verdictApprove.Render("✓ "+author))
		case "CHANGES_REQUESTED":
			parts = append(parts, verdictReject.Render("✗ "+author))
		case "DISMISSED":
			parts = append(parts, dimStyle.Render("~ "+author))
		case "COMMENTED":
			parts = append(parts, dimStyle.Render("· "+author))
		}
	}

	// Pending requested reviewers who haven't reviewed
	reviewed := map[string]bool{}
	for author := range latest {
		reviewed[author] = true
	}
	for _, r := range pr.RequestedReviewers {
		if !reviewed[r] {
			parts = append(parts, verdictReview.Render("? "+r))
		}
	}

	if len(parts) == 0 {
		return dimStyle.Render("No reviews yet")
	}
	return strings.Join(parts, "  ")
}

func renderChecksStatus(pr *github.PR) string {
	summary := pr.ChecksSummary()
	if pr.HasFailingChecks() {
		return verdictReject.Render("Checks: " + summary)
	}
	if strings.Contains(summary, "pending") {
		return verdictReview.Render("Checks: " + summary)
	}
	if strings.Contains(summary, "passed") {
		return verdictApprove.Render("Checks: " + summary)
	}
	return dimStyle.Render("Checks: " + summary)
}

func scoreBar(score float64) string {
	filled := int(math.Round(score))
	if filled > 5 {
		filled = 5
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 5-filled)
	var style lipgloss.Style
	switch {
	case score <= 2.0:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case score <= 3.5:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	default:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	}
	return style.Render(bar)
}

func renderVerdict(verdict string) string {
	switch verdict {
	case "approve":
		return verdictApprove.Render("APPROVE")
	case "review":
		return verdictReview.Render("REVIEW")
	case "reject":
		return verdictReject.Render("REJECT")
	default:
		return verdict
	}
}

// Commands

func fetchPRListCmd(repo string) tea.Cmd {
	return func() tea.Msg {
		rawPRs, err := github.ListOpenPRsMeta(repo)
		return prListFetchedMsg{rawPRs: rawPRs, err: err}
	}
}

func fetchPRDetailsCmd(raw map[string]any, a *app.App) tea.Cmd {
	return func() tea.Msg {
		pr, err := github.FetchPRDetails(a.Repo, raw, a.Config.Review.MaxDiffChars)
		return prDetailsFetchedMsg{pr: pr, raw: raw, err: err}
	}
}

func scorePRCmd(pr *github.PR, a *app.App) tea.Cmd {
	return func() tea.Msg {
		key := cache.Key(a.Repo, pr.Number, pr.Diff, reviewsText(pr))

		if assessment, ok := a.Cache.Get(key); ok {
			logger.Info("PR #%d: cache hit", pr.Number)
			return prScoredMsg{prNumber: pr.Number, assessment: &assessment, fromCache: true}
		}

		assessment, err := ai.AssessPR(pr, a.RepoDir)
		if err != nil {
			return prScoredMsg{prNumber: pr.Number, err: err}
		}
		a.Cache.Set(key, *assessment)
		return prScoredMsg{prNumber: pr.Number, assessment: assessment}
	}
}

func reviewsText(pr *github.PR) string {
	var sb strings.Builder
	for _, r := range pr.Reviews {
		fmt.Fprintf(&sb, "%s|%s|%s\n", r.Author, r.State, r.Body)
	}
	for _, c := range pr.InlineComments {
		fmt.Fprintf(&sb, "%s|inline|%s|%s\n", c.Author, c.Path, c.Body)
	}
	for _, c := range pr.Comments {
		fmt.Fprintf(&sb, "%s|comment|%s\n", c.Author, c.Body)
	}
	return sb.String()
}


func mergeCmd(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		err := github.MergePR(repo, number)
		return actionDoneMsg{pr: number, action: "merge", err: err}
	}
}

func approveCmd(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		err := github.ApprovePR(repo, number)
		return actionDoneMsg{pr: number, action: "approve", err: err}
	}
}

func requestChangesCmd(repo string, number int, body string) tea.Cmd {
	return func() tea.Msg {
		err := github.RequestChanges(repo, number, body)
		return actionDoneMsg{pr: number, action: "request-changes", err: err}
	}
}

func weightedScore(assessment *ai.Assessment, app *app.App) float64 {
	w := app.Config.Weights
	total := w.BlastRadius + w.TestCoverage + w.Sensitivity + w.Complexity + w.ScopeFocus
	if total == 0 {
		return 0
	}
	weighted := float64(assessment.BlastRadius.Score)*w.BlastRadius +
		float64(assessment.TestCoverage.Score)*w.TestCoverage +
		float64(assessment.Sensitivity.Score)*w.Sensitivity +
		float64(assessment.Complexity.Score)*w.Complexity +
		float64(assessment.ScopeFocus.Score)*w.ScopeFocus
	return math.Round(weighted/total*10) / 10
}

func computeVerdict(score float64, app *app.App) string {
	t := app.Config.Thresholds
	if score < t.ApproveBelow {
		return "approve"
	}
	if score > t.ReviewAbove {
		return "reject"
	}
	return "review"
}
