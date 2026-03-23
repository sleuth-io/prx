package diff

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	diffAddedStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Background(lipgloss.Color("#1a3a1a"))
	diffRemovedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Background(lipgloss.Color("#3a1a1a"))
	diffAddedHighlightStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Background(lipgloss.Color("22"))
	diffRemovedHighlightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Background(lipgloss.Color("52"))
	diffHunkStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Faint(true)
	diffFileStyle             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215"))

	commentStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	commentAuthorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	commentExpandedStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("214")).
				PaddingLeft(1)

	cursorLineStyle    = lipgloss.NewStyle().Background(lipgloss.Color("238"))                                             // subtle bg for diff content
	cursorHunkStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("26")) // bright on brighter blue
	cursorTrivialStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("240"))           // bright on lighter gray
)

// collapsibleKind identifies what kind of item is at a viewport line.
type collapsibleKind int

const (
	kindFile         collapsibleKind = iota
	kindHunk                         // individual hunk within a file
	kindFilesGroup                   // header that wraps all diff files
	kindCommentGroup                 // top-level comments grouped by author
	kindComment                      // individual comment (inline, or expanded from group)
)

type collapsible struct {
	lineIdx    int
	kind       collapsibleKind
	fileIdx    int
	hunkIdx    int
	comment    *CommentItem
	group      *CommentGroup
	rawContent string         // unstyled text for re-rendering with cursor style
	baseStyle  lipgloss.Style // style used when not under cursor
}

// Hunk is a single diff hunk within a file.
type Hunk struct {
	HeaderLine    string
	Rendered      []string
	RawLines      []string // unstyled line content (parallel to Rendered)
	LineNums      []int
	Additions     int
	Deletions     int
	Collapsed     bool
	Trivial       bool
	Annotated     bool // true if AI provided an annotation for this hunk
	TrivialReason string
	StartLine     int // new-file line number from fragment header
}

// File is a parsed diff file with pre-rendered colored lines.
type File struct {
	Name      string
	Collapsed bool
	Hunks     []*Hunk
}

// CommentItem is a single comment (inline or top-level).
type CommentItem struct {
	Author       string
	Path         string // empty for top-level
	LineNum      int    // diff line number (0 if not line-specific)
	Body         string
	Collapsed    bool
	Pending      bool   // true while the API call is in-flight
	renderedBody string // cached markdown render
}

// CommentGroup groups top-level comments by author.
type CommentGroup struct {
	Author    string
	Comments  []*CommentItem
	Collapsed bool
}

// DiffView is a scrollable diff viewer with collapsible files and comments.
type DiffView struct {
	files          []*File
	filesCollapsed bool            // controls the files group header
	commentGroups  []*CommentGroup // top-level PR comments grouped by author
	inline         []*CommentItem  // inline code comments (grouped into files during render)
	collapsibles   []collapsible   // ordered list of all collapsible positions
	lines          []string        // built lines (no cursor highlight applied)
	cursorLine     int
	viewport       viewport.Model
	width          int
	height         int
	Focused        bool
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
	d.width = width - 1 // reserve 1 char for scrollbar
	d.height = height
	d.viewport.Width = d.width
	d.viewport.Height = height
	d.rebuildViewport()
}

func (d *DiffView) SetContent(diff string, pr *github.PR) {
	d.SetParsedContent(ParseDiff(diff), pr)
}

// SetParsedContent sets the diff view using pre-parsed files (avoids re-running ParseDiff).
func (d *DiffView) SetParsedContent(files []*File, pr *github.PR) {
	d.files = files
	d.filesCollapsed = false

	// Group top-level comments by author (preserving first-appearance order)
	groupByAuthor := map[string]*CommentGroup{}
	d.commentGroups = nil
	for _, c := range pr.Comments {
		item := &CommentItem{Author: c.Author, Body: c.Body, Collapsed: true}
		if g, ok := groupByAuthor[c.Author]; ok {
			g.Comments = append(g.Comments, item)
		} else {
			g = &CommentGroup{Author: c.Author, Collapsed: true, Comments: []*CommentItem{item}}
			groupByAuthor[c.Author] = g
			d.commentGroups = append(d.commentGroups, g)
		}
	}
	d.inline = nil
	for _, c := range pr.InlineComments {
		d.inline = append(d.inline, &CommentItem{
			Author:    c.Author,
			Path:      c.Path,
			LineNum:   c.Line,
			Body:      c.Body,
			Collapsed: true,
		})
	}
	d.cursorLine = 0
	d.rebuildViewport()
	d.viewport.GotoTop()
}

// SetDiff is kept for callers that don't have a PR available.
func (d *DiffView) SetDiff(raw string) {
	d.files = ParseDiff(raw)
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
		if !d.files[c.fileIdx].Collapsed {
			d.files[c.fileIdx].Collapsed = true
			d.rebuildAndStay(c)
		}
	case kindHunk:
		h := d.files[c.fileIdx].Hunks[c.hunkIdx]
		if !h.Collapsed {
			h.Collapsed = true
			d.rebuildAndStay(c)
		}
	case kindCommentGroup:
		if !c.group.Collapsed {
			c.group.Collapsed = true
			d.rebuildAndStay(c)
		}
	case kindComment:
		if !c.comment.Collapsed {
			c.comment.Collapsed = true
			d.rebuildAndStay(c)
		} else if c.group != nil {
			c.group.Collapsed = true
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
		if d.files[c.fileIdx].Collapsed {
			d.files[c.fileIdx].Collapsed = false
			d.rebuildAndStay(c)
		}
	case kindHunk:
		h := d.files[c.fileIdx].Hunks[c.hunkIdx]
		if h.Collapsed {
			h.Collapsed = false
			d.rebuildAndStay(c)
		}
	case kindCommentGroup:
		if c.group.Collapsed {
			c.group.Collapsed = false
			d.rebuildAndStay(c)
		}
	case kindComment:
		if c.comment.Collapsed {
			c.comment.Collapsed = false
			d.rebuildAndStay(c)
		}
	}
}

// ExpandMore expands one level at a time:
//  1. filesGroup collapsed        → expand filesGroup
//  2. any file collapsed          → expand all files
//  3. any hunk collapsed          → expand all hunks
//  4. any comment group collapsed → expand all comment groups
func (d *DiffView) ExpandMore() {
	if d.filesCollapsed {
		d.filesCollapsed = false
		d.rebuildViewport()
		return
	}
	for _, f := range d.files {
		if f.Collapsed {
			for _, f2 := range d.files {
				f2.Collapsed = false
			}
			d.rebuildViewport()
			return
		}
	}
	for _, f := range d.files {
		for _, h := range f.Hunks {
			if h.Collapsed {
				for _, f2 := range d.files {
					for _, h2 := range f2.Hunks {
						h2.Collapsed = false
					}
				}
				d.rebuildViewport()
				return
			}
		}
	}
	for _, g := range d.commentGroups {
		if g.Collapsed {
			for _, g2 := range d.commentGroups {
				g2.Collapsed = false
			}
			d.rebuildViewport()
			return
		}
	}
}

// CollapseMore collapses one level at a time (reverse of ExpandMore):
//  1. any comment group expanded → collapse all comment groups
//  2. any hunk expanded          → collapse all hunks (keep files visible)
//  3. any file expanded          → collapse all files (keep filesGroup visible)
//  4. otherwise                  → collapse filesGroup
func (d *DiffView) CollapseMore() {
	for _, g := range d.commentGroups {
		if !g.Collapsed {
			for _, g2 := range d.commentGroups {
				g2.Collapsed = true
			}
			d.rebuildViewport()
			return
		}
	}
	for _, f := range d.files {
		for _, h := range f.Hunks {
			if !h.Collapsed {
				for _, f2 := range d.files {
					for _, h2 := range f2.Hunks {
						h2.Collapsed = true
					}
				}
				d.rebuildViewport()
				return
			}
		}
	}
	for _, f := range d.files {
		if !f.Collapsed {
			for _, f2 := range d.files {
				f2.Collapsed = true
			}
			d.rebuildViewport()
			return
		}
	}
	d.filesCollapsed = true
	d.rebuildViewport()
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

func (d *DiffView) NextHunk() {
	for _, c := range d.collapsibles {
		if c.kind == kindHunk && c.lineIdx > d.cursorLine {
			d.MoveCursor(c.lineIdx - d.cursorLine)
			return
		}
	}
}

func (d *DiffView) PrevHunk() {
	var best *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.kind == kindHunk && c.lineIdx < d.cursorLine {
			best = c
		}
	}
	if best != nil {
		d.MoveCursor(best.lineIdx - d.cursorLine)
	}
}

// ScrollToHunk scrolls to the hunk matching the given file and start line (±3 fuzzy).
// If the file or hunk is collapsed, it expands them first.
func (d *DiffView) ScrollToHunk(file string, startLine int) {
	// Find the file and hunk indices
	fileIdx := -1
	hunkIdx := -1
	for fi, f := range d.files {
		if f.Name != file {
			continue
		}
		fileIdx = fi
		// Exact match first
		for hi, h := range f.Hunks {
			if h.StartLine == startLine {
				hunkIdx = hi
				break
			}
		}
		// Fuzzy match ±3
		if hunkIdx < 0 {
			for delta := 1; delta <= 3; delta++ {
				for hi, h := range f.Hunks {
					if h.StartLine == startLine+delta || h.StartLine == startLine-delta {
						hunkIdx = hi
						break
					}
				}
				if hunkIdx >= 0 {
					break
				}
			}
		}
		break
	}
	if fileIdx < 0 || hunkIdx < 0 {
		return
	}

	// Expand file and hunk if collapsed
	changed := false
	if d.files[fileIdx].Collapsed {
		d.files[fileIdx].Collapsed = false
		changed = true
	}
	if d.files[fileIdx].Hunks[hunkIdx].Collapsed {
		d.files[fileIdx].Hunks[hunkIdx].Collapsed = false
		changed = true
	}
	if changed {
		d.rebuildViewport()
	}

	// Find the collapsible entry for this hunk and jump to it
	for _, c := range d.collapsibles {
		if c.kind == kindHunk && c.fileIdx == fileIdx && c.hunkIdx == hunkIdx {
			d.cursorLine = c.lineIdx
			idealOffset := d.cursorLine - d.viewport.Height/2
			if idealOffset < 0 {
				idealOffset = 0
			}
			d.viewport.SetYOffset(idealOffset)
			d.syncViewport()
			return
		}
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
		case "}":
			d.NextHunk()
			return d, nil
		case "{":
			d.PrevHunk()
			return d, nil
		}
	}

	return d, nil
}

// AddPendingComment adds a comment to the live diff view marked as pending.
// Returns a pointer to the item so the caller can confirm or remove it later.
func (d *DiffView) AddPendingComment(c github.ReviewComment) *CommentItem {
	item := &CommentItem{
		Author:    c.Author,
		Path:      c.Path,
		LineNum:   c.Line,
		Body:      c.Body,
		Collapsed: false,
		Pending:   true,
	}
	if c.Path != "" {
		d.inline = append(d.inline, item)
	} else {
		for _, g := range d.commentGroups {
			if g.Author == c.Author {
				g.Comments = append(g.Comments, item)
				g.Collapsed = false
				d.rebuildViewport()
				return item
			}
		}
		g := &CommentGroup{Author: c.Author, Collapsed: false, Comments: []*CommentItem{item}}
		d.commentGroups = append(d.commentGroups, g)
	}
	d.rebuildViewport()
	return item
}

// ConfirmComment clears the pending flag on a comment item.
func (d *DiffView) ConfirmComment(item *CommentItem) {
	item.Pending = false
	d.rebuildViewport()
}

// RemoveComment removes a pending comment (on API error).
func (d *DiffView) RemoveComment(item *CommentItem) {
	for _, g := range d.commentGroups {
		for i, c := range g.Comments {
			if c == item {
				g.Comments = append(g.Comments[:i], g.Comments[i+1:]...)
				d.rebuildViewport()
				return
			}
		}
	}
	for i, c := range d.inline {
		if c == item {
			d.inline = append(d.inline[:i], d.inline[i+1:]...)
			d.rebuildViewport()
			return
		}
	}
}

// CurrentLineTarget returns the file path and new-file line number at the cursor.
func (d *DiffView) CurrentLineTarget() (path string, line int) {
	var fileCol *collapsible
	var hunkCol *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.lineIdx > d.cursorLine {
			break
		}
		if c.kind == kindFile {
			fileCol = c
			hunkCol = nil // reset hunk when we enter a new file
		}
		if c.kind == kindHunk {
			hunkCol = c
		}
	}
	if fileCol == nil || hunkCol == nil {
		return "", 0
	}
	f := d.files[fileCol.fileIdx]
	h := f.Hunks[hunkCol.hunkIdx]
	offset := d.cursorLine - hunkCol.lineIdx - 1 // -1 for hunk header line
	if offset < 0 || offset >= len(h.LineNums) {
		return "", 0
	}
	ln := h.LineNums[offset]
	if ln <= 0 {
		return "", 0
	}
	return f.Name, ln
}

// DiffQuote captures a quoted line from the diff for embedding in chat.
type DiffQuote struct {
	File       string // file path
	Line       int    // line number
	RawContent string // unstyled line content (e.g. "+  def foo():")
	StyledLine string // ANSI-styled rendered line from the diff
}

// CurrentQuote returns the diff line at the cursor for quoting in chat.
// Returns nil if cursor isn't on a diff line.
func (d *DiffView) CurrentQuote() *DiffQuote {
	path, line := d.CurrentLineTarget()
	if path == "" {
		return nil
	}
	var fileCol *collapsible
	var hunkCol *collapsible
	for i := range d.collapsibles {
		c := &d.collapsibles[i]
		if c.lineIdx > d.cursorLine {
			break
		}
		if c.kind == kindFile {
			fileCol = c
			hunkCol = nil
		}
		if c.kind == kindHunk {
			hunkCol = c
		}
	}
	q := &DiffQuote{File: path, Line: line}
	if fileCol == nil || hunkCol == nil {
		return q
	}
	f := d.files[fileCol.fileIdx]
	h := f.Hunks[hunkCol.hunkIdx]
	offset := d.cursorLine - hunkCol.lineIdx - 1
	if offset < 0 || offset >= len(h.RawLines) {
		return q
	}
	q.RawContent = h.RawLines[offset]
	if offset < len(h.Rendered) {
		q.StyledLine = h.Rendered[offset]
	}
	return q
}

func (d DiffView) TitleWithCommentCount() string {
	n := len(d.inline)
	for _, g := range d.commentGroups {
		n += len(g.Comments)
	}
	if n > 0 {
		return fmt.Sprintf("Diff \U0001f4ac%d", n)
	}
	return "Diff"
}

// ViewContent renders the diff body without a title bar (for tabbed layout).
func (d DiffView) ViewContent() string {
	return lipgloss.JoinHorizontal(lipgloss.Top, d.viewport.View(), style.RenderScrollbar(d.viewport))
}

func (d DiffView) View() string {
	hint := "tab to focus"
	if d.Focused {
		hint = "\u2190/\u2192 collapse/expand  [/] file  {/} hunk  c comment  tab to exit"
	}
	width := d.width
	if width == 0 {
		width = 80
	}
	title := style.RenderPanelTitle(d.TitleWithCommentCount(), hint, d.Focused, width)
	return lipgloss.JoinVertical(lipgloss.Left, title, d.ViewContent())
}

// ViewWithModal renders the diff with modal injected at the cursor position.
func (d DiffView) ViewWithModal(modal string) string {
	hint := "tab to focus"
	if d.Focused {
		hint = "\u2190/\u2192 collapse/expand  [/] file  {/} hunk  c comment  tab to exit"
	}
	width := d.width
	if width == 0 {
		width = 80
	}
	title := style.RenderPanelTitle(d.TitleWithCommentCount(), hint, d.Focused, width)

	vpHeight := d.viewport.Height

	vpStr := strings.TrimRight(d.viewport.View(), "\n")
	vpLines := strings.Split(vpStr, "\n")
	for len(vpLines) < vpHeight {
		vpLines = append(vpLines, "")
	}

	modal = strings.TrimRight(modal, "\n")
	modalLines := strings.Split(modal, "\n")
	modalH := len(modalLines)

	cursorInView := d.cursorLine - d.viewport.YOffset
	if cursorInView < 0 {
		cursorInView = 0
	}
	if cursorInView >= vpHeight {
		cursorInView = vpHeight - 1
	}

	aboveCount := cursorInView + 1
	belowCount := vpHeight - aboveCount - modalH
	if belowCount < 0 {
		aboveCount = vpHeight - modalH
		if aboveCount < 0 {
			aboveCount = 0
		}
		belowCount = 0
	}

	result := make([]string, 0, vpHeight)
	for i := 0; i < aboveCount; i++ {
		result = append(result, vpLines[i])
	}
	result = append(result, modalLines...)
	for i := 0; i < belowCount; i++ {
		result = append(result, vpLines[aboveCount+i])
	}
	for len(result) < vpHeight {
		result = append(result, "")
	}
	result = result[:vpHeight]

	content := lipgloss.JoinHorizontal(lipgloss.Top,
		strings.Join(result, "\n"),
		style.RenderScrollbar(d.viewport),
	)
	return lipgloss.JoinVertical(lipgloss.Left, title, content)
}

func renderCommentGroup(g *CommentGroup) string {
	count := style.DimStyle.Render(fmt.Sprintf("(%d)", len(g.Comments)))
	if g.Collapsed {
		return "\U0001f4ac " + commentAuthorStyle.Render(g.Author) + " " + count + style.CollapseHint.Render("  [\u2192 expand]")
	}
	return "\U0001f4ac " + commentAuthorStyle.Render(g.Author) + " " + count + style.CollapseHint.Render("  [\u2190 collapse]")
}

func renderComment(c *CommentItem, width int, grouped bool) []string {
	var header string
	if grouped {
		header = "  "
	} else {
		prefix := ""
		if c.Path != "" {
			prefix = style.DimStyle.Render(c.Path + ": ")
		}
		pendingMark := ""
		if c.Pending {
			pendingMark = style.DimStyle.Render(" \u2026posting")
		}
		header = "\U0001f4ac " + commentAuthorStyle.Render(c.Author) + pendingMark + "  " + prefix
	}

	if c.Collapsed {
		first := firstLine(c.Body)
		if len(first) > 80 {
			first = first[:80] + "\u2026"
		}
		return []string{header + commentStyle.Render(first) + style.CollapseHint.Render("  [\u2192 expand]")}
	}

	if c.renderedBody == "" {
		c.renderedBody = style.RenderMarkdown(c.Body, width-4)
	}
	body := commentExpandedStyle.Width(width - 4).Render(c.renderedBody)
	var out []string
	out = append(out, header+style.CollapseHint.Render("  [\u2190 collapse]"))
	out = append(out, strings.Split(body, "\n")...)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
