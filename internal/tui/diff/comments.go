package diff

import (
	"fmt"
	"strings"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/style"
)

// AddPendingComment adds a comment item and rebuilds the viewport.
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
				d.rebuildPreservingPosition()
				return item
			}
		}
		g := &CommentGroup{Author: c.Author, Collapsed: false, Comments: []*CommentItem{item}}
		d.commentGroups = append(d.commentGroups, g)
	}
	d.rebuildPreservingPosition()
	return item
}

// ConfirmComment clears the pending flag on a comment item.
func (d *DiffView) ConfirmComment(item *CommentItem) {
	item.Pending = false
	d.rebuildPreservingPosition()
}

// RemoveComment removes a pending comment (on API error).
func (d *DiffView) RemoveComment(item *CommentItem) {
	for _, g := range d.commentGroups {
		for i, c := range g.Comments {
			if c == item {
				g.Comments = append(g.Comments[:i], g.Comments[i+1:]...)
				d.rebuildPreservingPosition()
				return
			}
		}
	}
	for i, c := range d.inline {
		if c == item {
			d.inline = append(d.inline[:i], d.inline[i+1:]...)
			d.rebuildPreservingPosition()
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
			hunkCol = nil
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
	offset := d.cursorLine - hunkCol.lineIdx - 1
	if offset < 0 || offset >= len(h.LineNums) {
		return "", 0
	}
	ln := h.LineNums[offset]
	if ln <= 0 {
		return "", 0
	}
	return f.Name, ln
}

// CurrentQuote returns the diff line at the cursor for quoting in chat.
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

// TitleWithCommentCount returns the diff panel title with comment count badge.
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

// titleString returns the title with incremental summary if applicable.
func (d DiffView) titleString() string {
	base := d.TitleWithCommentCount()
	if summary := d.IncrementalSummary(); summary != "" {
		return base + " (" + summary + ")"
	}
	return base
}

func renderCommentGroup(g *CommentGroup) string {
	count := style.DimStyle.Render(fmt.Sprintf("(%d)", len(g.Comments)))
	if g.Collapsed {
		return "\U0001f4ac " + commentAuthorStyle.Render(g.Author) + " " + count + style.CollapseHint.Render("  [\u2192 expand]")
	}
	return "\U0001f4ac " + commentAuthorStyle.Render(g.Author) + " " + count + style.CollapseHint.Render("  [\u2190 collapse]")
}

func renderComment(c *CommentItem, width int, grouped bool) []string {
	isReply := c.InReplyToID != 0
	indent := ""
	if isReply {
		indent = "  " // indent replies
	}

	var header string
	if grouped {
		header = "  " + indent
		if c.IsNew {
			header += diffNewBadge.Render("new") + " "
		} else if c.IsEdited {
			header += diffEditedBadge.Render("edited") + " "
		}
	} else {
		prefix := ""
		if c.Path != "" {
			prefix = style.DimStyle.Render(c.Path + ": ")
		}
		pendingMark := ""
		if c.Pending {
			pendingMark = style.DimStyle.Render(" \u2026posting")
		}
		badge := ""
		if c.IsNew {
			badge = " " + diffNewBadge.Render("new")
		} else if c.IsEdited {
			badge = " " + diffEditedBadge.Render("edited")
		}
		replyIcon := "\U0001f4ac "
		if isReply {
			replyIcon = "  ↳ "
		}
		header = indent + replyIcon + commentAuthorStyle.Render(c.Author) + pendingMark + badge + "  " + prefix
	}

	bodyWidth := width - 4 - len(indent)
	if bodyWidth < 20 {
		bodyWidth = 20
	}

	if c.Collapsed {
		first := firstLine(c.Body)
		if len(first) > 80 {
			first = first[:80] + "\u2026"
		}
		return []string{header + commentStyle.Render(first) + style.CollapseHint.Render("  [\u2192 expand]")}
	}

	if c.renderedBody == "" {
		c.renderedBody = style.RenderMarkdown(c.Body, bodyWidth)
	}
	body := commentExpandedStyle.Width(bodyWidth).Render(c.renderedBody)
	var out []string
	out = append(out, header+style.CollapseHint.Render("  [\u2190 collapse]"))
	for _, line := range strings.Split(body, "\n") {
		out = append(out, indent+line)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
