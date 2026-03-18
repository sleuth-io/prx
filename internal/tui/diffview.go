package tui

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/github"
)

var (
	diffAddedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	diffRemovedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	diffHunkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Faint(true)
	diffFileStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215"))
	diffCollapsed    = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Faint(true)

	commentStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("222"))
	commentAuthorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	commentExpandedStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("214")).
				PaddingLeft(1)

	panelTitleFocused = lipgloss.NewStyle().
				Bold(true).
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Padding(0, 1)
	panelTitleBlurred = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("243")).
				Padding(0, 1)
)

// collapsibleKind identifies what kind of item is at a viewport line.
type collapsibleKind int

const (
	kindFile    collapsibleKind = iota
	kindComment                 // top-level or inline
)

type collapsible struct {
	lineIdx int
	kind    collapsibleKind
	fileIdx int
	comment *commentItem
}

type diffFile struct {
	name      string
	collapsed bool
	rendered  []string
}

type commentItem struct {
	author    string
	path      string // empty for top-level
	body      string
	collapsed bool
}

// DiffView is a scrollable diff viewer with collapsible files and comments.
type DiffView struct {
	files       []*diffFile
	comments    []*commentItem   // top-level PR comments
	inline      []*commentItem   // inline code comments (grouped into files during render)
	collapsibles []collapsible    // ordered list of all collapsible positions
	viewport    viewport.Model
	width       int
	height      int
	Focused     bool
}

func NewDiffView(width, height int) DiffView {
	vp := viewport.New(width, height)
	return DiffView{
		viewport: vp,
		width:    width,
		height:   height,
	}
}

func (d *DiffView) SetSize(width, height int) {
	d.width = width
	d.height = height
	d.viewport.Width = width
	d.viewport.Height = height
	d.rebuildViewport()
}

func (d *DiffView) SetContent(diff string, pr *github.PR) {
	d.files = parseDiff(diff)

	d.comments = nil
	for _, c := range pr.Comments {
		d.comments = append(d.comments, &commentItem{
			author:    c.Author,
			body:      c.Body,
			collapsed: true,
		})
	}

	d.inline = nil
	for _, c := range pr.InlineComments {
		d.inline = append(d.inline, &commentItem{
			author:    c.Author,
			path:      c.Path,
			body:      c.Body,
			collapsed: true,
		})
	}

	d.rebuildViewport()
	d.viewport.GotoTop()
}

// SetDiff is kept for callers that don't have a PR available.
func (d *DiffView) SetDiff(raw string) {
	d.files = parseDiff(raw)
	d.comments = nil
	d.inline = nil
	d.rebuildViewport()
	d.viewport.GotoTop()
}

func (d *DiffView) CollapseCurrentFile() {
	c := d.collapsibleAtOffset()
	if c == nil {
		return
	}
	switch c.kind {
	case kindFile:
		f := d.files[c.fileIdx]
		if !f.collapsed {
			f.collapsed = true
			d.rebuildViewport()
			d.viewport.SetYOffset(d.collapsibleLineIdx(c))
		}
	case kindComment:
		if !c.comment.collapsed {
			c.comment.collapsed = true
			d.rebuildViewport()
			d.viewport.SetYOffset(d.collapsibleLineIdx(c))
		}
	}
}

func (d *DiffView) ExpandCurrentFile() {
	c := d.collapsibleAtOffset()
	if c == nil {
		return
	}
	switch c.kind {
	case kindFile:
		f := d.files[c.fileIdx]
		if f.collapsed {
			f.collapsed = false
			d.rebuildViewport()
			d.viewport.SetYOffset(d.collapsibleLineIdx(c))
		}
	case kindComment:
		if c.comment.collapsed {
			c.comment.collapsed = false
			d.rebuildViewport()
			d.viewport.SetYOffset(d.collapsibleLineIdx(c))
		}
	}
}

func (d *DiffView) NextFile() {
	offset := d.viewport.YOffset
	for _, c := range d.collapsibles {
		if c.kind == kindFile && c.lineIdx > offset {
			d.viewport.SetYOffset(c.lineIdx)
			return
		}
	}
}

func (d *DiffView) PrevFile() {
	offset := d.viewport.YOffset
	var best *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.kind == kindFile && c.lineIdx < offset {
			best = c
		}
	}
	if best != nil {
		d.viewport.SetYOffset(best.lineIdx)
	}
}

func (d *DiffView) collapsibleAtOffset() *collapsible {
	offset := d.viewport.YOffset
	var best *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.lineIdx <= offset {
			best = c
		}
	}
	return best
}

// collapsibleLineIdx finds the current line index for a collapsible after rebuild.
func (d *DiffView) collapsibleLineIdx(target *collapsible) int {
	for _, c := range d.collapsibles {
		if c.kind == target.kind && c.fileIdx == target.fileIdx && c.comment == target.comment {
			return c.lineIdx
		}
	}
	return 0
}

func (d *DiffView) rebuildViewport() {
	var lines []string
	d.collapsibles = d.collapsibles[:0]

	// Top-level comments first
	for _, c := range d.comments {
		d.collapsibles = append(d.collapsibles, collapsible{
			lineIdx: len(lines),
			kind:    kindComment,
			comment: c,
		})
		lines = append(lines, renderComment(c, d.width)...)
		lines = append(lines, "")
	}

	// Build inline comment map: path -> comments
	inlineByFile := map[string][]*commentItem{}
	for _, c := range d.inline {
		inlineByFile[c.path] = append(inlineByFile[c.path], c)
	}

	// Files with their inline comments appended
	for i, f := range d.files {
		d.collapsibles = append(d.collapsibles, collapsible{
			lineIdx: len(lines),
			kind:    kindFile,
			fileIdx: i,
		})

		header := diffFileStyle.Render("── " + f.name + " ")
		if f.collapsed {
			header += diffCollapsed.Render(" [collapsed — → to expand]")
			lines = append(lines, header)
		} else {
			lines = append(lines, header)
			lines = append(lines, f.rendered...)

			// Inline comments for this file
			for _, c := range inlineByFile[f.name] {
				lines = append(lines, "") // spacer
				d.collapsibles = append(d.collapsibles, collapsible{
					lineIdx: len(lines),
					kind:    kindComment,
					comment: c,
				})
				lines = append(lines, renderComment(c, d.width)...)
			}
		}
		lines = append(lines, "")
	}

	d.viewport.SetContent(strings.Join(lines, "\n"))
}

func renderComment(c *commentItem, width int) []string {
	prefix := ""
	if c.path != "" {
		prefix = dimStyle.Render(c.path+": ")
	}
	header := "💬 " + commentAuthorStyle.Render(c.author) + "  " + prefix

	if c.collapsed {
		firstLine := firstLine(c.body)
		if len(firstLine) > 80 {
			firstLine = firstLine[:80] + "…"
		}
		return []string{header + commentStyle.Render(firstLine) + diffCollapsed.Render("  [→ expand]")}
	}

	// Expanded: wrap body with left border
	body := commentExpandedStyle.Width(width - 4).Render(c.body)
	var out []string
	out = append(out, header+diffCollapsed.Render("  [← collapse]"))
	out = append(out, strings.Split(body, "\n")...)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (d DiffView) Update(msg tea.Msg) (DiffView, tea.Cmd) {
	if !d.Focused {
		return d, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "c":
			d.CollapseCurrentFile()
			return d, nil
		case "]", "f":
			d.NextFile()
			return d, nil
		case "[", "F":
			d.PrevFile()
			return d, nil
		}
	}

	var cmd tea.Cmd
	d.viewport, cmd = d.viewport.Update(msg)
	return d, cmd
}

func (d DiffView) View() string {
	titleStyle := panelTitleBlurred
	hint := " tab to focus"
	if d.Focused {
		titleStyle = panelTitleFocused
		hint = " ← collapse  → expand  ] next file  [ prev file  tab to exit"
	}
	width := d.width
	if width == 0 {
		width = 80
	}
	title := titleStyle.Render("Diff") + dimPanelHint(hint, titleStyle, width)
	return lipgloss.JoinVertical(lipgloss.Left, title, d.viewport.View())
}

func dimPanelHint(hint string, titleStyle lipgloss.Style, width int) string {
	titleWidth := lipgloss.Width(titleStyle.Render("Diff"))
	remaining := width - titleWidth
	if remaining <= 0 {
		return ""
	}
	return panelTitleBlurred.Faint(true).Width(remaining).Align(lipgloss.Right).Render(hint)
}

// parseDiff parses a unified diff string into per-file colored lines.
func parseDiff(raw string) []*diffFile {
	files, _, err := gitdiff.Parse(strings.NewReader(raw))
	if err != nil || len(files) == 0 {
		return []*diffFile{{
			name:     "diff",
			rendered: colorRawDiff(raw),
		}}
	}

	result := make([]*diffFile, 0, len(files))
	for _, f := range files {
		name := f.NewName
		if name == "" || name == "/dev/null" {
			name = f.OldName
		}
		result = append(result, &diffFile{
			name:     name,
			rendered: renderFileDiff(f),
		})
	}
	return result
}

func renderFileDiff(f *gitdiff.File) []string {
	lexer := detectLexer(f.NewName, f.OldName)
	var lines []string

	for _, frag := range f.TextFragments {
		hunkHeader := fmt.Sprintf("@@ -%d,%d +%d,%d @@",
			frag.OldPosition, frag.OldLines,
			frag.NewPosition, frag.NewLines,
		)
		if frag.Comment != "" {
			hunkHeader += " " + frag.Comment
		}
		lines = append(lines, diffHunkStyle.Render(hunkHeader))

		for _, line := range frag.Lines {
			lines = append(lines, renderDiffLine(line, lexer))
		}
	}
	return lines
}

func renderDiffLine(line gitdiff.Line, lexer chroma.Lexer) string {
	content := strings.TrimRight(line.Line, "\n")
	highlighted := syntaxHighlight(content, lexer)

	switch line.Op {
	case gitdiff.OpAdd:
		return diffAddedStyle.Render("+") + diffAddedStyle.Render(highlighted)
	case gitdiff.OpDelete:
		return diffRemovedStyle.Render("-") + diffRemovedStyle.Render(highlighted)
	default:
		return " " + highlighted
	}
}

func syntaxHighlight(code string, lexer chroma.Lexer) string {
	if lexer == nil {
		return code
	}
	var sb strings.Builder
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return code
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	if err := formatter.Format(&sb, style, iterator); err != nil {
		return code
	}
	return sb.String()
}

func detectLexer(newName, oldName string) chroma.Lexer {
	name := newName
	if name == "" || name == "/dev/null" {
		name = oldName
	}
	if name == "" {
		return nil
	}
	l := lexers.Match(name)
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

func colorRawDiff(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			lines = append(lines, diffFileStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			lines = append(lines, diffAddedStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			lines = append(lines, diffRemovedStyle.Render(line))
		case strings.HasPrefix(line, "@@"):
			lines = append(lines, diffHunkStyle.Render(line))
		default:
			lines = append(lines, line)
		}
	}
	return lines
}
