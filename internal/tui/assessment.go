package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/github"
)

var (
	verdictApprove = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	verdictReview  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	verdictReject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))

	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
	factorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
)

func (m *Model) rebuildAssessment() {
	m.assessmentVP.SetContent(m.buildAssessmentContent())
}

func (m Model) buildAssessmentContent() string {
	if m.current >= len(m.cards) {
		return ""
	}
	card := m.cards[m.current]

	width := m.assessmentVP.Width
	if width == 0 {
		width = 80
	}
	leftW := width * 2 / 5
	rightW := width - leftW

	pr := card.PR

	// Title: full width, no truncation
	title := fmt.Sprintf("  %s  %s", boldStyle.Render(fmt.Sprintf("#%d", pr.Number)), pr.Title)

	// Left column: meta + review state
	meta := fmt.Sprintf("  %s", dimStyle.Render(fmt.Sprintf("by %s  ·  +%d/-%d  ·  %d files  ·  %s",
		pr.Author, pr.Additions, pr.Deletions, pr.FilesChanged, pr.CreatedAt[:10])))
	reviews := "  " + renderReviewStatus(pr)
	checks := "  " + renderChecksStatus(pr)

	leftLines := []string{meta, reviews, checks}

	// Right column: AI assessment
	var rightLines []string
	if card.Scoring {
		rightLines = append(rightLines, fmt.Sprintf("  %s Scoring with Claude...", m.spinner.View()))
	} else if card.ScoringErr != nil {
		rightLines = append(rightLines, "  "+verdictReject.Render(fmt.Sprintf("Scoring error: %v", card.ScoringErr)))
	} else {
		a := card.Assessment
		bar := scoreBar(card.WeightedScore)
		rightLines = append(rightLines,
			fmt.Sprintf("  Risk %s %.1f  %s", bar, card.WeightedScore, renderVerdict(card.Verdict)))

		var factorParts []string
		for _, c := range m.app.Config.Criteria {
			if f, ok := a.Factors[c.Name]; ok {
				factorParts = append(factorParts, fmt.Sprintf("%s:%d", c.Label, f.Score))
			}
		}
		rightLines = append(rightLines, "  "+factorStyle.Render(strings.Join(factorParts, "  ")))
	}

	// Pad shorter column so JoinHorizontal aligns cleanly
	for len(leftLines) < len(rightLines) {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < len(leftLines) {
		rightLines = append(rightLines, "")
	}

	leftCol := lipgloss.NewStyle().Width(leftW).Render(strings.Join(leftLines, "\n"))
	rightCol := lipgloss.NewStyle().Width(rightW).Render(strings.Join(rightLines, "\n"))
	cols := lipgloss.JoinVertical(lipgloss.Left,
		title,
		lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol),
	)

	if card.Assessment == nil {
		return cols
	}

	a := card.Assessment
	wrapW := width - 2 // account for the 2-space indent
	wrapStyle := lipgloss.NewStyle().Width(wrapW)
	var below []string

	if a.RiskSummary != "" {
		below = append(below, "  "+dimStyle.Width(wrapW).Render(a.RiskSummary))
	}
	for _, line := range strings.Split(a.ReviewNotes, "\n") {
		if strings.TrimSpace(line) != "" {
			below = append(below, "  "+wrapStyle.Render(line))
		}
	}
	if len(below) == 0 {
		return cols
	}
	return lipgloss.JoinVertical(lipgloss.Left, cols, strings.Join(below, "\n"))
}

func truncate(s string, max int) string {
	// Strip ANSI before measuring, but we're working with plain strings here so rune-count is fine
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func renderReviewStatus(pr *github.PR) string {
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

	reviewed := map[string]bool{}
	for author := range latest {
		reviewed[author] = true
	}

	var parts []string
	var changesCount, pendingCount int

	for author, state := range latest {
		if state == "APPROVED" {
			parts = append(parts, verdictApprove.Render("✓ "+author))
		} else if state == "CHANGES_REQUESTED" {
			changesCount++
		}
	}
	for _, r := range pr.RequestedReviewers {
		if !reviewed[r] {
			pendingCount++
		}
	}

	if changesCount > 0 {
		parts = append(parts, verdictReject.Render(fmt.Sprintf("✗ %d", changesCount)))
	}
	if pendingCount > 0 {
		parts = append(parts, verdictReview.Render(fmt.Sprintf("? %d pending", pendingCount)))
	}

	if len(parts) == 0 {
		return dimStyle.Render("no reviews")
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

// renderScrollbar returns a 1-char-wide vertical scrollbar for a viewport.
func renderScrollbar(vp viewport.Model) string {
	h := vp.Height
	total := vp.TotalLineCount()

	trackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	thumbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	lines := make([]string, h)
	if total <= h {
		for i := range lines {
			lines[i] = trackStyle.Render("│")
		}
		return strings.Join(lines, "\n")
	}

	thumbH := h * h / total
	if thumbH < 1 {
		thumbH = 1
	}
	scrollRange := total - h
	thumbPos := 0
	if scrollRange > 0 {
		thumbPos = (h - thumbH) * vp.YOffset / scrollRange
	}
	for i := range lines {
		if i >= thumbPos && i < thumbPos+thumbH {
			lines[i] = thumbStyle.Render("█")
		} else {
			lines[i] = trackStyle.Render("│")
		}
	}
	return strings.Join(lines, "\n")
}
