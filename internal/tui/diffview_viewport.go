package tui

import (
	"fmt"
	"strings"
)

// rebuildAndStay rebuilds the viewport after a collapse/expand, keeping the
// cursor on the same collapsible item without scrolling the viewport.
func (d *DiffView) rebuildAndStay(c *collapsible) {
	d.rebuildViewport()
	d.cursorLine = d.collapsibleLineIdx(c)
	idealOffset := d.cursorLine - d.viewport.Height/2
	if idealOffset < 0 {
		idealOffset = 0
	}
	d.viewport.SetYOffset(idealOffset)
	d.syncViewport()
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
