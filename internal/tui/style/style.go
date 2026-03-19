package style

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	PanelTitleFocused = lipgloss.NewStyle().
				Bold(true).
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Padding(0, 1)
	PanelTitleBlurred = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("243")).
				Padding(0, 1)

	DimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// DimPanelHint renders a right-aligned hint in the panel title bar.
func DimPanelHint(hint string, titleStyle lipgloss.Style, width int, titleTexts ...string) string {
	titleText := "Diff"
	if len(titleTexts) > 0 {
		titleText = titleTexts[0]
	}
	titleWidth := lipgloss.Width(titleStyle.Render(titleText))
	remaining := width - titleWidth
	if remaining <= 0 {
		return ""
	}
	return PanelTitleBlurred.Faint(true).Width(remaining).Align(lipgloss.Right).Render(hint)
}

var mdRenderer *glamour.TermRenderer

func init() {
	mdRenderer, _ = glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(0))
}

// RenderMarkdown renders markdown text for the terminal using glamour.
func RenderMarkdown(text string, width int) string {
	if mdRenderer == nil {
		return lipgloss.NewStyle().Width(width).Render(text)
	}
	out, err := mdRenderer.Render(text)
	if err != nil {
		return lipgloss.NewStyle().Width(width).Render(text)
	}
	// Glamour doesn't wrap (width=0), so wrap with lipgloss
	result := strings.TrimRight(out, "\n")
	if width > 0 {
		result = lipgloss.NewStyle().Width(width).Render(result)
	}
	return result
}

// RenderScrollbar returns a 1-char-wide vertical scrollbar for a viewport.
func RenderScrollbar(vp viewport.Model) string {
	h := vp.Height
	total := vp.TotalLineCount()

	trackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	thumbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	lines := make([]string, h)
	if total <= h {
		for i := range lines {
			lines[i] = trackStyle.Render("|")
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
			lines[i] = thumbStyle.Render("\u2588")
		} else {
			lines[i] = trackStyle.Render("\u2502")
		}
	}
	return strings.Join(lines, "\n")
}
