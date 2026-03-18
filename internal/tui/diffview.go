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
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
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
	kindFile         collapsibleKind = iota
	kindFilesGroup                   // header that wraps all diff files
	kindCommentGroup                 // top-level comments grouped by author
	kindComment                      // individual comment (inline, or expanded from group)
)

type collapsible struct {
	lineIdx int
	kind    collapsibleKind
	fileIdx int
	comment *commentItem
	group   *commentGroup
}

type diffFile struct {
	name      string
	collapsed bool
	rendered  []string
}

type commentItem struct {
	author       string
	path         string // empty for top-level
	body         string
	collapsed    bool
	renderedBody string // cached markdown render
}

type commentGroup struct {
	author    string
	comments  []*commentItem
	collapsed bool
}

var cursorLineStyle = lipgloss.NewStyle().Background(lipgloss.Color("237"))

// DiffView is a scrollable diff viewer with collapsible files and comments.
type DiffView struct {
	files          []*diffFile
	filesCollapsed bool            // controls the files group header
	commentGroups  []*commentGroup // top-level PR comments grouped by author
	inline         []*commentItem  // inline code comments (grouped into files during render)
	collapsibles   []collapsible   // ordered list of all collapsible positions
	lines          []string        // built lines (no cursor highlight applied)
	cursorLine     int
	viewport       viewport.Model
	width          int
	height         int
	Focused        bool
	mdRenderer     *glamour.TermRenderer
}

func NewDiffView(width, height int) DiffView {
	vp := viewport.New(width, height)
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())
	return DiffView{
		viewport:   vp,
		width:      width,
		height:     height,
		mdRenderer: r,
	}
}

func (d *DiffView) SetSize(width, height int) {
	d.width = width - 1 // reserve 1 char for scrollbar
	d.height = height
	d.viewport.Width = d.width
	d.viewport.Height = height
	d.rebuildViewport()
}

func (d *DiffView) SetContent(diff string, pr *github.PR) {
	d.SetParsedContent(parseDiff(diff), pr)
}

// SetParsedContent sets the diff view using pre-parsed files (avoids re-running parseDiff).
func (d *DiffView) SetParsedContent(files []*diffFile, pr *github.PR) {
	d.files = files
	d.filesCollapsed = false

	// Group top-level comments by author (preserving first-appearance order)
	groupByAuthor := map[string]*commentGroup{}
	d.commentGroups = nil
	for _, c := range pr.Comments {
		item := &commentItem{author: c.Author, body: c.Body, collapsed: true}
		if g, ok := groupByAuthor[c.Author]; ok {
			g.comments = append(g.comments, item)
		} else {
			g = &commentGroup{author: c.Author, collapsed: true, comments: []*commentItem{item}}
			groupByAuthor[c.Author] = g
			d.commentGroups = append(d.commentGroups, g)
		}
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
	d.cursorLine = 0
	d.rebuildViewport()
	d.viewport.GotoTop()
}

// SetDiff is kept for callers that don't have a PR available.
func (d *DiffView) SetDiff(raw string) {
	d.files = parseDiff(raw)
	d.commentGroups = nil
	d.inline = nil
	d.cursorLine = 0
	d.rebuildViewport()
	d.viewport.GotoTop()
}

// rebuildAndStay rebuilds the viewport after a collapse/expand, keeping the
// cursor on the same collapsible item without scrolling the viewport.
func (d *DiffView) rebuildAndStay(c *collapsible) {
	d.rebuildViewport()
	d.cursorLine = d.collapsibleLineIdx(c)
	d.syncViewport()
}

func (d *DiffView) CollapseCurrentFile() {
	c := d.collapsibleAtOffset()
	if c == nil {
		return
	}
	switch c.kind {
	case kindFilesGroup:
		if !d.filesCollapsed {
			d.filesCollapsed = true
			d.rebuildAndStay(c)
		}
	case kindFile:
		if !d.files[c.fileIdx].collapsed {
			d.files[c.fileIdx].collapsed = true
			d.rebuildAndStay(c)
		}
	case kindCommentGroup:
		if !c.group.collapsed {
			c.group.collapsed = true
			d.rebuildAndStay(c)
		}
	case kindComment:
		if !c.comment.collapsed {
			c.comment.collapsed = true
			d.rebuildAndStay(c)
		} else if c.group != nil {
			c.group.collapsed = true
			d.rebuildAndStay(c)
		}
	}
}

func (d *DiffView) ExpandCurrentFile() {
	c := d.collapsibleAtOffset()
	if c == nil {
		return
	}
	switch c.kind {
	case kindFilesGroup:
		if d.filesCollapsed {
			d.filesCollapsed = false
			d.rebuildAndStay(c)
		}
	case kindFile:
		if d.files[c.fileIdx].collapsed {
			d.files[c.fileIdx].collapsed = false
			d.rebuildAndStay(c)
		}
	case kindCommentGroup:
		if c.group.collapsed {
			c.group.collapsed = false
			d.rebuildAndStay(c)
		}
	case kindComment:
		if c.comment.collapsed {
			c.comment.collapsed = false
			d.rebuildAndStay(c)
		}
	}
}

func (d *DiffView) AtTop() bool {
	return d.cursorLine == 0
}

func (d *DiffView) NextFile() {
	for _, c := range d.collapsibles {
		if c.kind == kindFile && c.lineIdx > d.cursorLine {
			d.MoveCursor(c.lineIdx - d.cursorLine)
			return
		}
	}
}

func (d *DiffView) PrevFile() {
	var best *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.kind == kindFile && c.lineIdx < d.cursorLine {
			best = c
		}
	}
	if best != nil {
		d.MoveCursor(best.lineIdx - d.cursorLine)
	}
}

func (d *DiffView) collapsibleAtOffset() *collapsible {
	var best *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.lineIdx <= d.cursorLine {
			best = c
		}
	}
	return best
}

// collapsibleLineIdx finds the current line index for a collapsible after rebuild.
func (d *DiffView) collapsibleLineIdx(target *collapsible) int {
	for _, c := range d.collapsibles {
		if c.kind == target.kind && c.fileIdx == target.fileIdx && c.comment == target.comment && c.group == target.group {
			return c.lineIdx
		}
	}
	return 0
}

func (d *DiffView) rebuildViewport() {
	var lines []string
	d.collapsibles = d.collapsibles[:0]

	// Top-level comment groups
	for _, g := range d.commentGroups {
		d.collapsibles = append(d.collapsibles, collapsible{
			lineIdx: len(lines),
			kind:    kindCommentGroup,
			group:   g,
		})
		lines = append(lines, renderCommentGroup(g))
		if !g.collapsed {
			for _, c := range g.comments {
				d.collapsibles = append(d.collapsibles, collapsible{
					lineIdx: len(lines),
					kind:    kindComment,
					comment: c,
					group:   g,
				})
				lines = append(lines, renderComment(c, d.width, true, d.mdRenderer)...)
			}
			lines = append(lines, "")
		}
	}

	// Build inline comment map: path -> comments
	inlineByFile := map[string][]*commentItem{}
	for _, c := range d.inline {
		inlineByFile[c.path] = append(inlineByFile[c.path], c)
	}

	// Files group header
	d.collapsibles = append(d.collapsibles, collapsible{lineIdx: len(lines), kind: kindFilesGroup})
	filesHeader := diffFileStyle.Render(fmt.Sprintf("── Files (%d) ", len(d.files)))
	if d.filesCollapsed {
		filesHeader += diffCollapsed.Render(" [→ expand]")
		lines = append(lines, filesHeader)
	} else {
		filesHeader += diffCollapsed.Render(" [← collapse]")
		lines = append(lines, filesHeader)

		for i, f := range d.files {
			d.collapsibles = append(d.collapsibles, collapsible{
				lineIdx: len(lines),
				kind:    kindFile,
				fileIdx: i,
			})

			fheader := diffFileStyle.Render("  ── " + f.name + " ")
			if f.collapsed {
				fheader += diffCollapsed.Render(" [→ expand]")
				lines = append(lines, fheader)
			} else {
				lines = append(lines, fheader)
				lines = append(lines, f.rendered...)

				for _, c := range inlineByFile[f.name] {
					lines = append(lines, "")
					d.collapsibles = append(d.collapsibles, collapsible{
						lineIdx: len(lines),
						kind:    kindComment,
						comment: c,
					})
					lines = append(lines, renderComment(c, d.width, false, d.mdRenderer)...)
				}
			}
		}
	}

	d.lines = lines
	if d.cursorLine >= len(d.lines) {
		d.cursorLine = len(d.lines) - 1
	}
	if d.cursorLine < 0 {
		d.cursorLine = 0
	}
	d.syncViewport()
}

func (d *DiffView) syncViewport() {
	if len(d.lines) == 0 {
		d.viewport.SetContent("")
		return
	}
	lines := make([]string, len(d.lines))
	copy(lines, d.lines)
	if d.cursorLine >= 0 && d.cursorLine < len(lines) {
		lines[d.cursorLine] = cursorLineStyle.Width(d.width).Render(lines[d.cursorLine])
	}
	d.viewport.SetContent(strings.Join(lines, "\n"))
}

func (d *DiffView) MoveCursor(delta int) {
	d.cursorLine += delta
	if d.cursorLine < 0 {
		d.cursorLine = 0
	}
	if d.cursorLine >= len(d.lines) {
		d.cursorLine = len(d.lines) - 1
	}

	// Re-center viewport when cursor goes out of view
	top := d.viewport.YOffset
	bottom := top + d.viewport.Height - 1
	if d.cursorLine < top || d.cursorLine > bottom {
		newTop := d.cursorLine - d.viewport.Height/2
		if newTop < 0 {
			newTop = 0
		}
		d.viewport.SetYOffset(newTop)
	}

	d.syncViewport()
}

func renderCommentGroup(g *commentGroup) string {
	count := dimStyle.Render(fmt.Sprintf("(%d)", len(g.comments)))
	if g.collapsed {
		return "💬 " + commentAuthorStyle.Render(g.author) + " " + count + diffCollapsed.Render("  [→ expand]")
	}
	return "💬 " + commentAuthorStyle.Render(g.author) + " " + count + diffCollapsed.Render("  [← collapse]")
}

// renderComment renders a single comment. grouped=true suppresses the author prefix (used within an expanded group).
func renderComment(c *commentItem, width int, grouped bool, r *glamour.TermRenderer) []string {
	var header string
	if grouped {
		header = "  "
	} else {
		prefix := ""
		if c.path != "" {
			prefix = dimStyle.Render(c.path+": ")
		}
		header = "💬 " + commentAuthorStyle.Render(c.author) + "  " + prefix
	}

	if c.collapsed {
		firstLine := firstLine(c.body)
		if len(firstLine) > 80 {
			firstLine = firstLine[:80] + "…"
		}
		return []string{header + commentStyle.Render(firstLine) + diffCollapsed.Render("  [→ expand]")}
	}

	// Expanded: render Markdown then wrap with left border (cached by width)
	if c.renderedBody == "" {
		c.renderedBody = renderMarkdown(r, c.body)
	}
	body := commentExpandedStyle.Width(width - 4).Render(c.renderedBody)
	var out []string
	out = append(out, header+diffCollapsed.Render("  [← collapse]"))
	out = append(out, strings.Split(body, "\n")...)
	return out
}

func renderMarkdown(r *glamour.TermRenderer, body string) string {
	if r == nil {
		return body
	}
	rendered, err := r.Render(body)
	if err != nil {
		return body
	}
	return strings.TrimRight(rendered, "\n")
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

	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "j", "down":
			d.MoveCursor(1)
			return d, nil
		case "k", "up":
			d.MoveCursor(-1)
			return d, nil
		case "ctrl+d", "pgdown":
			d.MoveCursor(d.viewport.Height / 2)
			return d, nil
		case "ctrl+u", "pgup":
			d.MoveCursor(-d.viewport.Height / 2)
			return d, nil
		case "]", "f":
			d.NextFile()
			return d, nil
		case "[", "F":
			d.PrevFile()
			return d, nil
		}
	}

	return d, nil
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
	content := lipgloss.JoinHorizontal(lipgloss.Top, d.viewport.View(), renderScrollbar(d.viewport))
	return lipgloss.JoinVertical(lipgloss.Left, title, content)
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
	if err != nil {
		logger.Debug("parseDiff: raw_len=%d files=%d err=%v", len(raw), len(files), err)
	}
	if len(files) == 0 {
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
