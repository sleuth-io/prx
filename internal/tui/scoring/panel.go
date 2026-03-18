package scoring

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	verdictApprove = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	verdictReview  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	verdictReject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))

	boldStyle   = lipgloss.NewStyle().Bold(true)
	factorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
)

// RenderData contains everything needed to render the assessment panel.
type RenderData struct {
	PR          *github.PR
	Assessment  *ai.Assessment
	Score       float64
	Verdict     string
	Scoring     bool
	ScoringErr  error
	SpinnerView string
	Criteria    []config.Criterion
}

// Panel is the assessment/scoring panel component.
type Panel struct {
	viewport viewport.Model
}

func New(width, height int) Panel {
	return Panel{
		viewport: viewport.New(width, height-1), // -1 for the panel title bar
	}
}

func (p *Panel) SetSize(width, height int) {
	p.viewport.Width = width - 1 // reserve 1 char for scrollbar
	p.viewport.Height = height - 1
}

func (p *Panel) ScrollUp(n int)   { p.viewport.ScrollUp(n) }
func (p *Panel) ScrollDown(n int) { p.viewport.ScrollDown(n) }
func (p *Panel) AtBottom() bool   { return p.viewport.AtBottom() }
func (p *Panel) GotoTop()         { p.viewport.GotoTop() }

// SetContent rebuilds the assessment panel content from data.
func (p *Panel) SetContent(data RenderData) {
	p.viewport.SetContent(buildContent(data, p.viewport.Width))
}


// ViewContent renders just the viewport content (no title bar).
func (p Panel) ViewContent() string {
	return p.viewport.View()
}

// Viewport returns the underlying viewport for scrollbar rendering.
func (p Panel) Viewport() viewport.Model {
	return p.viewport
}

// WeightedScore calculates the weighted score from an assessment.
func WeightedScore(assessment *ai.Assessment, criteria []config.Criterion) float64 {
	var totalWeight, weighted float64
	for _, c := range criteria {
		if f, ok := assessment.Factors[c.Name]; ok {
			totalWeight += c.Weight
			weighted += float64(f.Score) * c.Weight
		}
	}
	if totalWeight == 0 {
		return 0
	}
	return math.Round(weighted/totalWeight*10) / 10
}

// ComputeVerdict returns the verdict string based on score and thresholds.
func ComputeVerdict(score float64, thresholds config.ThresholdsConfig) string {
	if score < thresholds.ApproveBelow {
		return "approve"
	}
	if score > thresholds.ReviewAbove {
		return "reject"
	}
	return "review"
}

func buildContent(data RenderData, vpWidth int) string {
	w := vpWidth
	if w == 0 {
		w = 80
	}

	pr := data.PR
	leftW := w * 2 / 5
	rightW := w - leftW

	title := fmt.Sprintf("  %s  %s", boldStyle.Render(fmt.Sprintf("#%d", pr.Number)), pr.Title)

	meta := fmt.Sprintf("  %s", style.DimStyle.Render(fmt.Sprintf("by %s  \u00b7  +%d/-%d  \u00b7  %d files  \u00b7  %s",
		pr.Author, pr.Additions, pr.Deletions, pr.FilesChanged, pr.CreatedAt[:10])))
	reviews := "  " + renderReviewStatus(pr)
	checks := "  " + renderChecksStatus(pr)

	leftLines := []string{meta, reviews, checks}

	var rightLines []string
	if data.Scoring {
		rightLines = append(rightLines, fmt.Sprintf("  %s Scoring with Claude...", data.SpinnerView))
	} else if data.ScoringErr != nil {
		rightLines = append(rightLines, "  "+verdictReject.Render(fmt.Sprintf("Scoring error: %v", data.ScoringErr)))
	} else {
		bar := scoreBar(data.Score)
		rightLines = append(rightLines,
			fmt.Sprintf("  Risk %s %.1f  %s", bar, data.Score, renderVerdict(data.Verdict)))

		if data.Assessment != nil {
			var factorParts []string
			for _, c := range data.Criteria {
				if f, ok := data.Assessment.Factors[c.Name]; ok {
					factorParts = append(factorParts, fmt.Sprintf("%s:%d", c.Label, f.Score))
				}
			}
			rightLines = append(rightLines, "  "+factorStyle.Render(strings.Join(factorParts, "  ")))
		}
	}

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

	if data.Assessment == nil {
		return cols
	}

	a := data.Assessment
	wrapW := w - 2
	wrapStyle := lipgloss.NewStyle().Width(wrapW)
	var below []string

	if a.RiskSummary != "" {
		below = append(below, "  "+style.DimStyle.Width(wrapW).Render(a.RiskSummary))
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
		switch state {
		case "APPROVED":
			parts = append(parts, verdictApprove.Render("\u2713 "+author))
		case "CHANGES_REQUESTED":
			changesCount++
		}
	}
	for _, r := range pr.RequestedReviewers {
		if !reviewed[r] {
			pendingCount++
		}
	}

	if changesCount > 0 {
		parts = append(parts, verdictReject.Render(fmt.Sprintf("\u2717 %d", changesCount)))
	}
	if pendingCount > 0 {
		parts = append(parts, verdictReview.Render(fmt.Sprintf("? %d pending", pendingCount)))
	}

	if len(parts) == 0 {
		return style.DimStyle.Render("no reviews")
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
	return style.DimStyle.Render("Checks: " + summary)
}

func scoreBar(score float64) string {
	filled := int(math.Round(score))
	if filled > 5 {
		filled = 5
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", 5-filled)
	var s lipgloss.Style
	switch {
	case score <= 2.0:
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case score <= 3.5:
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	default:
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	}
	return s.Render(bar)
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
