package diff

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	diffHunkHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24"))  // bright on blue — needs attention
	diffHunkTrivialStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("238"))            // light on dark gray — skippable
	diffImportantStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("214")).Padding(0, 1)
	diffLineNumStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
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
		if c.kind == target.kind && c.fileIdx == target.fileIdx && c.hunkIdx == target.hunkIdx && c.comment == target.comment && c.group == target.group {
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

			allTrivial := len(f.Hunks) > 0 && allHunksTrivial(f.Hunks)
			fheader := diffFileStyle.Render("  ── " + f.Name + " ")
			if allTrivial {
				fheader += diffHunkTrivialStyle.Render("(all trivial) ")
			}
			if f.Collapsed {
				fheader += diffCollapsed.Render(" [→ expand]")
				lines = append(lines, fheader)
			} else {
				lines = append(lines, fheader)

				// Compute gutter width from max line number across all hunks
				gutterW := gutterWidth(f.Hunks)

				for hi, h := range f.Hunks {
					d.collapsibles = append(d.collapsibles, collapsible{
						lineIdx: len(lines),
						kind:    kindHunk,
						fileIdx: i,
						hunkIdx: hi,
					})

					if h.Collapsed {
						content := hunkLabel(f.Name, h)
						if h.TrivialReason != "" {
							content += "  " + h.TrivialReason
						}
						content += "  [→ expand]"
						lines = append(lines, diffHunkTrivialStyle.Width(d.width).Render(content))
					} else {
						content := hunkLabel(f.Name, h)
						if !h.Trivial && h.TrivialReason != "" {
							content += "  " + diffImportantStyle.Render(" "+h.TrivialReason+" ")
						}
						lines = append(lines, diffHunkHeaderStyle.Width(d.width).Render(content))
						for li, rl := range h.Rendered {
							var gutter string
							if li < len(h.LineNums) && h.LineNums[li] > 0 {
								gutter = diffLineNumStyle.Render(fmt.Sprintf("%*d ", gutterW, h.LineNums[li]))
							} else {
								gutter = diffLineNumStyle.Render(strings.Repeat(" ", gutterW+1))
							}
							lines = append(lines, gutter+rl)
						}
					}
				}

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

func hunkLabel(fileName string, h *Hunk) string {
	allDeletions := true
	for _, ln := range h.LineNums {
		if ln > 0 {
			allDeletions = false
			break
		}
	}
	if allDeletions {
		return "  " + fileName + " (deleted)"
	}
	return fmt.Sprintf("  %s:%d", fileName, h.StartLine)
}

func allHunksTrivial(hunks []*Hunk) bool {
	for _, h := range hunks {
		if !h.Trivial {
			return false
		}
	}
	return true
}

func gutterWidth(hunks []*Hunk) int {
	maxLine := 0
	for _, h := range hunks {
		for _, ln := range h.LineNums {
			if ln > maxLine {
				maxLine = ln
			}
		}
	}
	w := 1
	for n := maxLine; n >= 10; n /= 10 {
		w++
	}
	return w
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
