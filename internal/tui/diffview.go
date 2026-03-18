package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/github"
)

var (
	diffAddedStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Background(lipgloss.Color("#1a3a1a"))
	diffRemovedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Background(lipgloss.Color("#3a1a1a"))
	diffAddedHighlightStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Background(lipgloss.Color("22"))
	diffRemovedHighlightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Background(lipgloss.Color("52"))
	diffHunkStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Faint(true)
	diffFileStyle             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215"))
	diffCollapsed             = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))

	commentStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
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

	cursorLineStyle = lipgloss.NewStyle().Background(lipgloss.Color("237"))
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

func (d *DiffView) MoveCursor(delta int) {
	d.cursorLine += delta
	if d.cursorLine < 0 {
		d.cursorLine = 0
	}
	if d.cursorLine >= len(d.lines) {
		d.cursorLine = len(d.lines) - 1
	}

	// Lock cursor to viewport center when possible; scroll viewport to compensate
	idealOffset := d.cursorLine - d.viewport.Height/2
	if idealOffset < 0 {
		idealOffset = 0
	}
	d.viewport.SetYOffset(idealOffset)

	d.syncViewport()
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
			prefix = dimStyle.Render(c.path + ": ")
		}
		header = "💬 " + commentAuthorStyle.Render(c.author) + "  " + prefix
	}

	if c.collapsed {
		first := firstLine(c.body)
		if len(first) > 80 {
			first = first[:80] + "…"
		}
		return []string{header + commentStyle.Render(first) + diffCollapsed.Render("  [→ expand]")}
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
