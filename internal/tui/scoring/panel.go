package scoring

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/imgrender"
	"github.com/sleuth-io/prx/internal/tui/diff"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	verdictApprove = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	verdictReview  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	verdictReject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))

	boldStyle    = lipgloss.NewStyle().Bold(true)
	mergedBanner = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)
	closedBanner = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("238")).
			Padding(0, 1)
)

// RenderData contains everything needed to render the assessment.
type RenderData struct {
	Repo               string // "owner/name" — shown in title when MultiRepo is true
	MultiRepo          bool   // true when reviewing across multiple repos
	PR                 *github.PR
	Assessment         *ai.Assessment
	Score              float64
	Verdict            string
	Scoring            bool
	ScoringErr         error
	SpinnerView        string
	Criteria           []config.Criterion
	ScoringToolCount   int
	ScoringLastTool    string
	ScoringStatus      string
	ParsedFiles        []*diff.File
	ImageCache         *imgrender.Cache
	IncrementalSummary string // e.g., "2 new hunks, 3 new comments" — set when incremental state exists
	BodyEndLine        int    // set by buildContent: line after body text (for image placement)
	PostMerge          bool
	UserReaction       string // "+1" or "-1" if user reacted this session
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

// RenderInline renders the assessment as a string for embedding in a scrollback.
func RenderInline(data *RenderData, width int) string {
	return buildContent(data, width)
}

// ImageOverlay returns the rendered image escape sequence and source URL.
// This must be drawn separately (not in viewport content) to avoid layout corruption.
func ImageOverlay(data *RenderData) (rendered string, url string) {
	if data.ImageCache == nil || data.PR.Body == "" {
		return "", ""
	}
	rawBody := strings.ReplaceAll(data.PR.Body, "\r\n", "\n")
	for _, ref := range imgrender.ExtractImages(rawBody) {
		if r := data.ImageCache.Get(ref.URL); r != "" {
			return r, ref.URL
		}
	}
	return "", ""
}

// ScoreBar renders a 5-block bar colored by risk level.
func ScoreBar(score float64) string {
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

// RenderVerdict renders a colored verdict label.
func RenderVerdict(verdict string) string {
	switch verdict {
	case "approve":
		return verdictApprove.Render("APPROVE")
	case "review":
		return verdictReview.Render("REVIEW")
	case "reject":
		return verdictReject.Render("HIGH RISK")
	default:
		return verdict
	}
}

func buildContent(data *RenderData, vpWidth int) string {
	w := vpWidth
	if w == 0 {
		w = 80
	}

	pr := data.PR
	leftW := w * 2 / 5
	rightW := w - leftW

	stateBadge := ""
	switch pr.State {
	case "MERGED":
		stateBadge = mergedBanner.Render("MERGED") + " "
	case "CLOSED":
		stateBadge = closedBanner.Render("CLOSED") + " "
	}
	prNum := fmt.Sprintf("#%d", pr.Number)
	if data.MultiRepo {
		parts := strings.Split(data.Repo, "/")
		prNum = parts[len(parts)-1] + " " + prNum
	}
	title := lipgloss.NewStyle().Width(w - 2).Render(
		fmt.Sprintf("  %s%s  %s", stateBadge, boldStyle.Render(prNum), pr.Title))

	meta := fmt.Sprintf("  %s", style.DimStyle.Render(fmt.Sprintf("by %s  \u00b7  %s",
		pr.Author, pr.CreatedAt[:10])))
	diffStats := fmt.Sprintf("  %s", style.DimStyle.Render(fmt.Sprintf("+%d/-%d  \u00b7  %d files",
		pr.Additions, pr.Deletions, pr.FilesChanged)))
	reviews := "  Approvals: " + renderReviewStatus(pr)
	switch data.UserReaction {
	case "+1":
		reviews += "  " + verdictApprove.Render("\U0001f44d You approved")
	case "-1":
		reviews += "  " + verdictReject.Render("\U0001f44e You flagged")
	}
	checks := "  " + renderChecksStatus(pr)

	rawBody := strings.TrimSpace(strings.ReplaceAll(pr.Body, "\r\n", "\n"))
	prBody := sanitizeBody(rawBody)

	var riskLine string
	var scoringDetail string // full-width scoring status, shown below columns
	if data.Scoring {
		riskLine = fmt.Sprintf("  %s Scoring...", data.SpinnerView)
		if data.ScoringToolCount > 0 && data.ScoringLastTool != "" {
			scoringDetail = fmt.Sprintf("  %s %s (%d tool calls)", data.SpinnerView, data.ScoringLastTool, data.ScoringToolCount)
		} else if data.ScoringStatus != "" {
			scoringDetail = fmt.Sprintf("  %s %s", data.SpinnerView, data.ScoringStatus)
		}
	} else if data.ScoringErr != nil {
		riskLine = "  " + verdictReject.Render(fmt.Sprintf("Scoring error: %v", data.ScoringErr))
	} else {
		bar := ScoreBar(data.Score)
		riskLine = fmt.Sprintf("  Risk %s %.1f  %s", bar, data.Score, RenderVerdict(data.Verdict))
	}

	leftLines := []string{meta, diffStats, riskLine, reviews, checks}

	// Compact risk factors for the right column (label + bar + score only)
	var rightLines []string
	if data.Assessment != nil {
		maxLabelW := 0
		for _, c := range data.Criteria {
			if len(c.Label) > maxLabelW {
				maxLabelW = len(c.Label)
			}
		}
		for _, c := range data.Criteria {
			if f, ok := data.Assessment.Factors[c.Name]; ok {
				padded := fmt.Sprintf("%-*s", maxLabelW, c.Label)
				rightLines = append(rightLines, fmt.Sprintf("  %s %s %d", boldStyle.Render(padded), ScoreBar(float64(f.Score)), f.Score))
			}
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

	twoCol := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol)
	cols := lipgloss.JoinVertical(lipgloss.Left, title, twoCol)

	if scoringDetail != "" {
		cols = lipgloss.JoinVertical(lipgloss.Left, cols, scoringDetail)
	}

	if prBody != "" {
		cols = lipgloss.JoinVertical(lipgloss.Left, cols, style.DimStyle.Render("  ── Description ──"))
		rendered := lipgloss.NewStyle().Width(w - 4).Render("  " + prBody)
		cols = lipgloss.JoinVertical(lipgloss.Left, cols, rendered)
	}
	data.BodyEndLine = strings.Count(cols, "\n") + 1
	// Reserve blank lines for image overlay (if any) so risk factors appear below.
	// Add a clickable filename link BELOW the image area so it's not covered.
	if data.ImageCache != nil && pr.Body != "" {
		for _, ref := range imgrender.ExtractImages(strings.ReplaceAll(pr.Body, "\r\n", "\n")) {
			if data.ImageCache.Get(ref.URL) != "" {
				cols += strings.Repeat("\n", data.ImageCache.PlaceholderLines())
				cols += "\n" + style.DimStyle.Render("  📎 "+ref.URL)
				break
			}
		}
	}

	if data.Assessment == nil {
		return cols
	}

	a := data.Assessment
	wrapW := w - 2
	wrapStyle := lipgloss.NewStyle().Width(wrapW)
	var below []string

	// Review Guide first — the actionable summary
	if a.Guide != nil {
		below = append(below, style.DimStyle.Render("  ── Review Guide ──"))
		below = append(below, renderGuideRow("Summary", a.Guide.Summary, wrapW))
		below = append(below, renderGuideRow("Risk", a.Guide.Risk, wrapW))
		below = append(below, renderGuideRow("Focus", a.Guide.Focus, wrapW))
		if data.IncrementalSummary != "" {
			labelStr := "  " + guideNewLabelStyle.Render("New")
			below = append(below, labelStr+"  "+guideNewTextStyle.Render(data.IncrementalSummary))
		}
	} else if a.RiskSummary != "" || a.ReviewNotes != "" {
		// Fallback for cached assessments without structured guide.
		below = append(below, style.DimStyle.Render("  ── Review Guide ──"))
		if a.RenderedNotes == "" {
			notes := ""
			if a.RiskSummary != "" {
				notes += "**" + a.RiskSummary + "**\n\n"
			}
			if a.ReviewNotes != "" {
				notes += a.ReviewNotes
			}
			a.RenderedNotes = style.RenderMarkdown(notes, wrapW)
		}
		below = append(below, a.RenderedNotes)
	}

	// Key Change preview
	if keyHunkLines := renderKeyHunk(a, data.ParsedFiles, w); len(keyHunkLines) > 0 {
		below = append(below, keyHunkLines...)
	}

	_ = wrapStyle
	if len(below) == 0 {
		return cols
	}
	return lipgloss.JoinVertical(lipgloss.Left, cols, strings.Join(below, "\n"))
}

var (
	guideLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("240")).
			Padding(0, 1).
			Width(9) // fixed width so all labels align
	guideTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	guideNewLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("82")).
				Padding(0, 1).
				Width(9)
	guideNewTextStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
)

const guideLabelCol = 2 + 9 + 2 // indent + label width + gap

func renderGuideRow(label, text string, wrapW int) string {
	if text == "" {
		return ""
	}
	labelStr := "  " + guideLabelStyle.Render(label)
	textW := wrapW - guideLabelCol
	if textW < 20 {
		textW = 20
	}
	wrapped := guideTextStyle.Width(textW).Render(text)
	lines := strings.Split(wrapped, "\n")
	result := labelStr + "  " + lines[0]
	indent := strings.Repeat(" ", guideLabelCol)
	for _, l := range lines[1:] {
		result += "\n" + indent + l
	}
	return result
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

	// Collect approved authors sorted for stable rendering.
	var approved []string
	for author, state := range latest {
		switch state {
		case "APPROVED":
			approved = append(approved, author)
		case "CHANGES_REQUESTED":
			changesCount++
		}
	}
	sort.Strings(approved)
	for _, author := range approved {
		parts = append(parts, verdictApprove.Render("\u2713 "+author))
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
		parts = append(parts, verdictReview.Render(fmt.Sprintf("%d pending", pendingCount)))
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

var (
	keyHunkFileStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215"))
	keyHunkLineNum    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	keyHunkReasonText = lipgloss.NewStyle().Foreground(lipgloss.Color("248")).Italic(true)
)

// renderKeyHunk finds the AI-identified key hunk in parsed files and renders a compact preview.
func renderKeyHunk(a *ai.Assessment, files []*diff.File, _ int) []string {
	if a.KeyHunk == nil || files == nil {
		return nil
	}

	hunk := findKeyHunk(a.KeyHunk, files)
	if hunk == nil {
		return nil
	}

	var lines []string
	lines = append(lines, style.DimStyle.Render("  ── Key Change ──"))

	// Build the code block content using pre-rendered syntax-highlighted lines
	maxLines := 10
	var codeLines []string
	gutterW := 3
	for i, rendered := range hunk.Rendered {
		if i >= maxLines {
			more := fmt.Sprintf("  … %d more lines (^d to view full diff)", len(hunk.Rendered)-i)
			codeLines = append(codeLines, style.DimStyle.Render(more))
			break
		}
		// Add line numbers like the diff view
		lineNum := -1
		if i < len(hunk.LineNums) {
			lineNum = hunk.LineNums[i]
		}
		var gutter string
		if lineNum > 0 {
			gutter = keyHunkLineNum.Render(fmt.Sprintf("%*d ", gutterW, lineNum))
		} else {
			gutter = keyHunkLineNum.Render(strings.Repeat(" ", gutterW+1))
		}
		codeLines = append(codeLines, gutter+rendered)
	}

	// Render the code block with a left border for clear separation
	codeBlock := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(lipgloss.Color("62")).
		PaddingLeft(1).
		Render(strings.Join(codeLines, "\n"))

	// File header above the code block
	fileHeader := "  " + keyHunkFileStyle.Render(a.KeyHunk.File)
	lines = append(lines, fileHeader)
	// Indent the code block
	for _, cl := range strings.Split(codeBlock, "\n") {
		lines = append(lines, "  "+cl)
	}

	if a.KeyHunk.Reason != "" {
		lines = append(lines, "  "+keyHunkReasonText.Render(a.KeyHunk.Reason))
	}

	return lines
}

// findKeyHunk matches a KeyHunk reference to a parsed hunk, with ±3 line fuzzy matching.
func findKeyHunk(kh *ai.KeyHunk, files []*diff.File) *diff.Hunk {
	for _, f := range files {
		if f.Name != kh.File {
			continue
		}
		// Exact match first
		for _, h := range f.Hunks {
			if h.StartLine == kh.StartLine {
				return h
			}
		}
		// Fuzzy match ±3 lines
		for delta := 1; delta <= 3; delta++ {
			for _, h := range f.Hunks {
				if h.StartLine == kh.StartLine+delta || h.StartLine == kh.StartLine-delta {
					return h
				}
			}
		}
	}
	return nil
}

var (
	reHTMLTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// sanitizeBody converts HTML in PR descriptions to terminal-friendly text.
func sanitizeBody(s string) string {
	// Strip remaining HTML tags
	s = reHTMLTag.ReplaceAllString(s, "")
	// Collapse multiple blank lines
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
