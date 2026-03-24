package diff

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/style"
)

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
			// Auto-collapse the file if it has only one hunk
			if len(d.files[c.fileIdx].Hunks) == 1 {
				d.files[c.fileIdx].Collapsed = true
			}
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
			// Auto-expand the hunk if the file has only one
			if len(d.files[c.fileIdx].Hunks) == 1 {
				d.files[c.fileIdx].Hunks[0].Collapsed = false
			}
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

// ScrollToHunk scrolls to the hunk matching the given file and start line (plus/minus 3 fuzzy).
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
		// Fuzzy match plus/minus 3
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
