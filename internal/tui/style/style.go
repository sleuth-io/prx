package style

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	// PanelTitleFocused / PanelTitleBlurred are kept for overlays (modals, etc.)
	// that still want a solid background title bar.
	PanelTitleFocused = lipgloss.NewStyle().
				Bold(true).
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Padding(0, 1)
	PanelTitleBlurred = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("243")).
				Padding(0, 1)

	DimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	CollapseHint = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
)

// RenderPanelTitle renders a full-width horizontal rule with the panel name
// and an optional hint embedded — no solid background fill.
//
//	focused:  [bright] ━━ Name ━━━━━━━━━━━━ hint ━━ [/bright]
//	blurred:  [gray]   ── Name ──────────── hint ── [/gray]
func RenderPanelTitle(name, hint string, focused bool, width int) string {
	var sep string
	var lineCol, nameCol lipgloss.Color
	var hintFaint bool
	if focused {
		sep = "━"
		lineCol = lipgloss.Color("75")  // bright blue
		nameCol = lipgloss.Color("255") // bright white
		hintFaint = false
	} else {
		sep = "─"
		lineCol = lipgloss.Color("244") // visible mid-gray
		nameCol = lipgloss.Color("250")
		hintFaint = true
	}
	lineS := lipgloss.NewStyle().Foreground(lineCol)
	nameS := lipgloss.NewStyle().Foreground(nameCol).Bold(focused)

	left := lineS.Render(sep+sep+" ") + nameS.Render(name) + lineS.Render(" ")
	var right string
	if hint != "" {
		hintS := lipgloss.NewStyle().Foreground(lineCol).Faint(hintFaint)
		right = hintS.Render(hint) + lineS.Render(" "+sep+sep)
	} else {
		right = lineS.Render(sep + sep)
	}

	fill := width - lipgloss.Width(left) - lipgloss.Width(right)
	if fill < 0 {
		fill = 0
	}
	return left + lineS.Render(strings.Repeat(sep, fill)) + right
}

var mdRenderer *glamour.TermRenderer

func init() {
	mdRenderer, _ = glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(0))
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
	result := strings.Trim(out, "\n")
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
