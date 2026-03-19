package diff

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"strings"
)

var (
	diffHunkHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")) // bright on blue — needs attention
	diffHunkTrivialStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("238"))           // light on dark gray — skippable
	diffImportantStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("214")).Padding(0, 1)
	diffLineNumStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
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
				lines = append(lines, renderComment(c, d.width, true)...)
			}
			lines = append(lines, "")
		}
	}

	// Build inline comment map: "path:line" -> comments
	type fileLineKey struct {
		path string
		line int
	}
	inlineByLine := map[fileLineKey][]*CommentItem{}
	inlineByFile := map[string][]*CommentItem{}
	for _, c := range d.inline {
		if c.LineNum > 0 {
			inlineByLine[fileLineKey{c.Path, c.LineNum}] = append(inlineByLine[fileLineKey{c.Path, c.LineNum}], c)
		}
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
					if h.Collapsed {
						content := hunkLabel(f.Name, h)
						if h.TrivialReason != "" {
							content += "  " + h.TrivialReason
						}
						content += "  [→ expand]"
						hunkStyle := diffHunkTrivialStyle
						if h.Annotated && !h.Trivial {
							hunkStyle = diffHunkHeaderStyle
						}
						d.collapsibles = append(d.collapsibles, collapsible{
							lineIdx:    len(lines),
							kind:       kindHunk,
							fileIdx:    i,
							hunkIdx:    hi,
							rawContent: content,
							baseStyle:  hunkStyle,
						})
						lines = append(lines, hunkStyle.Width(d.width).Render(content))
					} else {
						content := hunkLabel(f.Name, h)
						rawContent := content
						if h.Annotated && !h.Trivial {
							content += "  " + diffImportantStyle.Render(" "+h.TrivialReason+" ")
							rawContent += "  " + h.TrivialReason
						}
						d.collapsibles = append(d.collapsibles, collapsible{
							lineIdx:    len(lines),
							kind:       kindHunk,
							fileIdx:    i,
							hunkIdx:    hi,
							rawContent: rawContent,
							baseStyle:  diffHunkHeaderStyle,
						})
						lines = append(lines, diffHunkHeaderStyle.Width(d.width).Render(content))
						for li, rl := range h.Rendered {
							var lineNum int
							if li < len(h.LineNums) {
								lineNum = h.LineNums[li]
							}
							var gutter string
							if lineNum > 0 {
								gutter = diffLineNumStyle.Render(fmt.Sprintf("%*d ", gutterW, lineNum))
							} else {
								gutter = diffLineNumStyle.Render(strings.Repeat(" ", gutterW+1))
							}
							lines = append(lines, gutter+rl)

							// Insert inline comments after the line they reference
							if lineNum > 0 {
								key := fileLineKey{f.Name, lineNum}
								for _, ic := range inlineByLine[key] {
									lines = append(lines, "")
									d.collapsibles = append(d.collapsibles, collapsible{
										lineIdx: len(lines),
										kind:    kindComment,
										comment: ic,
									})
									lines = append(lines, renderComment(ic, d.width, false)...)
								}
							}
						}
					}
				}

				// Comments that couldn't be placed at a specific line (line == 0)
				for _, c := range inlineByFile[f.Name] {
					if c.LineNum > 0 {
						continue // already rendered inline
					}
					lines = append(lines, "")
					d.collapsibles = append(d.collapsibles, collapsible{
						lineIdx: len(lines),
						kind:    kindComment,
						comment: c,
					})
					lines = append(lines, renderComment(c, d.width, false)...)
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
	var stats string
	if h.Additions > 0 && h.Deletions > 0 {
		stats = fmt.Sprintf(" +%d/-%d", h.Additions, h.Deletions)
	} else if h.Additions > 0 {
		stats = fmt.Sprintf(" +%d", h.Additions)
	} else if h.Deletions > 0 {
		stats = fmt.Sprintf(" -%d", h.Deletions)
	}
	if allDeletions {
		return "  " + fileName + stats + " (deleted)"
	}
	return fmt.Sprintf("  %s:%d%s", fileName, h.StartLine, stats)
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
		// For hunk headers, re-render with a brighter cursor-aware style
		rerendered := false
		for _, c := range d.collapsibles {
			if c.lineIdx == d.cursorLine && c.kind == kindHunk && c.rawContent != "" {
				curStyle := cursorTrivialStyle
				if c.baseStyle.GetBackground() == diffHunkHeaderStyle.GetBackground() {
					curStyle = cursorHunkStyle
				}
				lines[d.cursorLine] = curStyle.Width(d.width).Render(c.rawContent)
				rerendered = true
				break
			}
		}
		if !rerendered {
			lines[d.cursorLine] = cursorLineStyle.Width(d.width).Render(lines[d.cursorLine])
		}
	}
	d.viewport.SetContent(strings.Join(lines, "\n"))
}
