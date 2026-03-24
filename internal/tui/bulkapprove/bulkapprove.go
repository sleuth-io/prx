package bulkapprove

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/fireworks"
	"github.com/sleuth-io/prx/internal/tui/scoring"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	cursorStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("237"))
	checkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")).Bold(true)
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
	dimStyle    = style.DimStyle
	footerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)
)

// Item is a PR eligible for bulk approval.
type Item struct {
	Number        int
	Title         string
	Author        string
	Additions     int
	Deletions     int
	FilesChanged  int
	CreatedAt     string
	WeightedScore float64
	Verdict       string
	RiskSummary   string
	PostMerge     bool
}

// ItemFromCard builds an Item from a PRCard's exported fields.
func ItemFromCard(pr *github.PR, score float64, verdict string, riskSummary string, postMerge bool) Item {
	return Item{
		Number:        pr.Number,
		Title:         pr.Title,
		Author:        pr.Author,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		FilesChanged:  pr.FilesChanged,
		CreatedAt:     pr.CreatedAt,
		WeightedScore: score,
		Verdict:       verdict,
		RiskSummary:   riskSummary,
		PostMerge:     postMerge,
	}
}

// --- Messages emitted by this model ---

// ExitMsg tells the parent to leave bulk approve mode.
type ExitMsg struct{}

// ViewPRMsg tells the parent to navigate to a specific PR.
type ViewPRMsg struct {
	PRNumber int
}

// QuitMsg tells the parent to quit the application.
type QuitMsg struct{}

// approveDoneMsg is internal — results from bulk approval.
type approveDoneMsg struct {
	results []approveResult
}

type approveResult struct {
	prNumber int
	err      error
}

// Model is the bulk approve screen.
type Model struct {
	items       []Item
	selected    map[int]bool  // PR number -> selected
	results     map[int]error // PR number -> nil (success) or error; nil map = not done
	cursor      int
	approving   bool
	repo        string
	currentUser string
	width       int
	height      int
	spinnerView string
	frame       int // animation frame counter for empty state
}

// New creates a new bulk approve model. Items with "approve" verdict are pre-checked.
func New(repo string, currentUser string, items []Item, width, height int) Model {
	sel := make(map[int]bool, len(items))
	for _, item := range items {
		sel[item.Number] = item.Verdict == "approve"
	}
	return Model{
		items:       items,
		selected:    sel,
		repo:        repo,
		currentUser: currentUser,
		width:       width,
		height:      height,
	}
}

// SetSize updates dimensions.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// SetSpinnerView updates the spinner frame for the approving state.
func (m *Model) SetSpinnerView(s string) {
	m.spinnerView = s
	m.frame++
}

// Active returns true when the model has items to show.
func (m Model) Active() bool {
	return len(m.items) > 0
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case approveDoneMsg:
		if m.results == nil {
			m.results = make(map[int]error)
		}
		for _, r := range msg.results {
			m.results[r.prNumber] = r.err
		}
		m.approving = false
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.approving {
		if msg.String() == "q" || msg.String() == "ctrl+c" || msg.String() == "ctrl+q" {
			return m, func() tea.Msg { return QuitMsg{} }
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c", "ctrl+q":
		return m, func() tea.Msg { return QuitMsg{} }
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case " ", "x":
		if m.cursor < len(m.items) {
			num := m.items[m.cursor].Number
			if !m.isApproved(num) {
				m.selected[num] = !m.selected[num]
			}
		}
	case "A":
		allSelected := true
		for _, item := range m.items {
			if !m.selected[item.Number] {
				allSelected = false
				break
			}
		}
		for _, item := range m.items {
			if !m.isApproved(item.Number) {
				m.selected[item.Number] = !allSelected
			}
		}
	case "a":
		var toApprove []Item
		for _, item := range m.items {
			if m.selected[item.Number] && !m.isApproved(item.Number) {
				toApprove = append(toApprove, item)
			}
		}
		if len(toApprove) > 0 {
			m.approving = true
			return m, approveCmd(m.repo, m.currentUser, toApprove)
		}
	case "enter":
		if m.cursor < len(m.items) {
			num := m.items[m.cursor].Number
			return m, func() tea.Msg { return ViewPRMsg{PRNumber: num} }
		}
	case "n":
		return m, func() tea.Msg { return ExitMsg{} }
	case "esc":
		return m, func() tea.Msg { return ExitMsg{} }
	}
	return m, nil
}

func (m Model) isApproved(prNumber int) bool {
	if m.results == nil {
		return false
	}
	_, done := m.results[prNumber]
	return done
}

func approveCmd(repo, currentUser string, items []Item) tea.Cmd {
	return func() tea.Msg {
		results := make([]approveResult, len(items))
		for i, item := range items {
			var err error
			if item.PostMerge {
				err = github.SetReaction(repo, item.Number, "+1", currentUser)
			} else {
				err = github.ApprovePR(repo, item.Number)
			}
			results[i] = approveResult{prNumber: item.Number, err: err}
		}
		return approveDoneMsg{results: results}
	}
}

func (m Model) View() string {
	width := m.width
	if width == 0 {
		width = 80
	}

	// Count selected.
	selectedCount := 0
	for _, item := range m.items {
		if m.selected[item.Number] {
			selectedCount++
		}
	}

	// Header.
	approveCount := 0
	for _, item := range m.items {
		if item.Verdict == "approve" {
			approveCount++
		}
	}
	hint := fmt.Sprintf("%d pre-selected for approval", approveCount)
	header := style.RenderPanelTitle(fmt.Sprintf("Bulk Approve — %d PRs", len(m.items)), hint, true, width)

	// Build list.
	var lines []string
	lines = append(lines, header)
	lines = append(lines, "")

	if len(m.items) == 0 {
		availH := m.height - len(lines) - 1 // -1 for footer
		if availH < 5 {
			availH = 5
		}
		fw := fireworks.Render(m.frame, width, availH)
		lines = append(lines, fw)
	}

	for i, item := range m.items {
		num := item.Number

		// Checkbox state.
		var checkbox string
		if m.results != nil {
			if err, done := m.results[num]; done {
				if err != nil {
					checkbox = errorStyle.Render("[!]")
				} else {
					checkbox = checkStyle.Render("[\u2713]")
				}
			} else {
				checkbox = renderCheckbox(m.selected[num])
			}
		} else {
			checkbox = renderCheckbox(m.selected[num])
		}

		// Line 1: checkbox, PR number, title, score, verdict.
		bar := scoring.ScoreBar(item.WeightedScore)
		scoreStr := fmt.Sprintf("%.1f", item.WeightedScore)
		verdict := scoring.RenderVerdict(item.Verdict)

		mergedTag := ""
		if item.PostMerge {
			mergedTag = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("57")).
				Render(" MERGED ") + " "
		}

		maxTitleW := width - 40
		if item.PostMerge {
			maxTitleW -= 10
		}
		title := item.Title
		if len(title) > maxTitleW && maxTitleW > 3 {
			title = title[:maxTitleW-3] + "..."
		}

		line1 := fmt.Sprintf("  %s  #%-4d  %s%-*s  %s %s %s",
			checkbox, num, mergedTag, maxTitleW, title, bar, scoreStr, verdict)

		// Line 2: metadata.
		date := item.CreatedAt
		if len(date) >= 10 {
			date = date[:10]
		}
		line2 := fmt.Sprintf("         %s",
			dimStyle.Render(fmt.Sprintf("%s  \u00b7  +%d/-%d  \u00b7  %d files  \u00b7  %s",
				item.Author, item.Additions, item.Deletions, item.FilesChanged, date)))

		// Line 3: risk summary or error.
		var line3 string
		if m.results != nil {
			if err, done := m.results[num]; done && err != nil {
				line3 = fmt.Sprintf("         %s", errorStyle.Render(fmt.Sprintf("Error: %v", err)))
			} else {
				line3 = renderSummaryLine(item.RiskSummary, width)
			}
		} else {
			line3 = renderSummaryLine(item.RiskSummary, width)
		}

		// Highlight cursor row.
		if i == m.cursor {
			line1 = cursorStyle.Width(width).Render(line1)
			line2 = cursorStyle.Width(width).Render(line2)
			line3 = cursorStyle.Width(width).Render(line3)
		}

		lines = append(lines, line1, line2, line3, "")
	}

	// Fill remaining height (fireworks already fills its area).
	if len(m.items) > 0 {
		contentHeight := len(lines)
		footerH := 1
		remaining := m.height - contentHeight - footerH
		for i := 0; i < remaining; i++ {
			lines = append(lines, "")
		}
	}

	// Footer.
	status := fmt.Sprintf("prx  %d/%d selected", selectedCount, len(m.items))
	if m.approving {
		status += fmt.Sprintf("  %s Approving...", m.spinnerView)
	}
	var hints string
	if len(m.items) == 0 {
		hints = "^a show all  ^r refresh  n next  q quit"
	} else {
		hints = "space toggle  A all  a approve  enter view  n next  esc back  q quit"
	}
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	footer := footerStyle.Width(width).Render(status + strings.Repeat(" ", gap) + hints)

	return strings.Join(lines, "\n") + "\n" + footer
}

func renderCheckbox(selected bool) string {
	if selected {
		return "[x]"
	}
	return "[ ]"
}

func renderSummaryLine(summary string, width int) string {
	if len(summary) > width-12 && width > 15 {
		summary = summary[:width-15] + "..."
	}
	return fmt.Sprintf("         %s", dimStyle.Render(summary))
}
