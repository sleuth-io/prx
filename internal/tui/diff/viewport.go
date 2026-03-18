package diff

import (
	"fmt"
	"strings"
)

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
		if !g.Collapsed {
			for _, c := range g.Comments {
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
	inlineByFile := map[string][]*CommentItem{}
	for _, c := range d.inline {
		inlineByFile[c.Path] = append(inlineByFile[c.Path], c)
	}

	// Files group header
	d.collapsibles = append(d.collapsibles, collapsible{lineIdx: len(lines), kind: kindFilesGroup})
	filesHeader := diffFileStyle.Render(fmt.Sprintf("\u2500\u2500 Files (%d) ", len(d.files)))
	if d.filesCollapsed {
		filesHeader += diffCollapsed.Render(" [\u2192 expand]")
		lines = append(lines, filesHeader)
	} else {
		filesHeader += diffCollapsed.Render(" [\u2190 collapse]")
		lines = append(lines, filesHeader)

		for i, f := range d.files {
			d.collapsibles = append(d.collapsibles, collapsible{
				lineIdx: len(lines),
				kind:    kindFile,
				fileIdx: i,
			})

			fheader := diffFileStyle.Render("  \u2500\u2500 " + f.Name + " ")
			if f.Collapsed {
				fheader += diffCollapsed.Render(" [\u2192 expand]")
				lines = append(lines, fheader)
			} else {
				lines = append(lines, fheader)
				lines = append(lines, f.Rendered...)

				for _, c := range inlineByFile[f.Name] {
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
