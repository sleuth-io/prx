package diff

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/reviewstate"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	diffHunkHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")) // bright on blue — needs attention
	diffHunkTrivialStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("238"))           // light on dark gray — skippable
	diffImportantStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("214")).Padding(0, 1)
	diffLineNumStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// vpLine converts a d.lines index to the corresponding viewport line index.
// d.lines entries may contain embedded newlines (e.g. from lipgloss Width
// wrapping), so one d.lines entry can span multiple viewport lines.  This
// helper accounts for that so callers never assume a 1:1 mapping.
func (d *DiffView) vpLine(idx int) int {
	n := 0
	for i := 0; i < idx && i < len(d.lines); i++ {
		n += 1 + strings.Count(d.lines[i], "\n")
	}
	return n
}

func (d *DiffView) rebuildAndStay(c *collapsible) {
	d.rebuildViewport()
	d.cursorLine = d.collapsibleLineIdx(c)
	idealOffset := d.vpLine(d.cursorLine) - d.viewport.Height/4
	if idealOffset < 0 {
		idealOffset = 0
	}
	d.viewport.SetYOffset(idealOffset)
	d.syncViewport()
}

// rebuildPreservingPosition rebuilds the viewport while keeping the cursor and
// scroll offset visually anchored to whatever collapsible the cursor was in.
// Use this when content is inserted/removed (e.g. comments added) so the user
// doesn't get kicked to the top of the diff.
func (d *DiffView) rebuildPreservingPosition() {
	anchor := d.collapsibleAtOffset()
	if anchor == nil {
		d.rebuildViewport()
		return
	}
	oldAnchorLine := anchor.lineIdx
	cursorDelta := d.cursorLine - oldAnchorLine
	oldAnchorVP := d.vpLine(oldAnchorLine)
	offsetDelta := d.viewport.YOffset - oldAnchorVP

	d.rebuildViewport()

	newAnchorLine := d.collapsibleLineIdx(anchor)
	d.cursorLine = newAnchorLine + cursorDelta
	if d.cursorLine < 0 {
		d.cursorLine = 0
	}
	if d.cursorLine >= len(d.lines) {
		d.cursorLine = len(d.lines) - 1
	}
	d.viewport.SetYOffset(d.vpLine(newAnchorLine) + offsetDelta)
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
	d.applyIncrementalFlags()

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
	rendered := map[*CommentItem]bool{}
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
		filesHeader += style.CollapseHint.Render(" [→ expand]")
		lines = append(lines, filesHeader)
	} else {
		filesHeader += style.CollapseHint.Render(" [← collapse]")
		lines = append(lines, filesHeader)

		for i, f := range d.files {
			d.collapsibles = append(d.collapsibles, collapsible{
				lineIdx: len(lines),
				kind:    kindFile,
				fileIdx: i,
			})

			allTrivial := len(f.Hunks) > 0 && allHunksTrivial(f.Hunks)
			fheader := diffFileStyle.Render("  ── " + f.Name + " ")
			if d.incrementalMode {
				if allNew := d.fileAllNew(f); allNew {
					fheader += diffNewBadge.Render("new file") + " "
				}
				if nc := d.fileNewCommentCount(f); nc > 0 {
					fheader += diffNewBadge.Render(fmt.Sprintf("%d new comments", nc)) + " "
				}
			} else if allTrivial {
				fheader += diffHunkTrivialStyle.Render("(all trivial) ")
			}
			if f.Collapsed {
				fheader += style.CollapseHint.Render(" [→ expand]")
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
						if d.incrementalMode && h.ReviewStatus == reviewstate.StatusSeen {
							hunkStyle = diffSeenStyle
						} else if h.Annotated && !h.Trivial {
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

						// In incremental mode, show new/edited inline comments
						// even when the hunk is collapsed (code unchanged but comments are new).
						if d.incrementalMode {
							for _, ic := range inlineByFile[f.Name] {
								if rendered[ic] || (!ic.IsNew && !ic.IsEdited) {
									continue
								}
								// Check if this comment belongs to this hunk's line range
								if ic.LineNum >= h.StartLine && ic.LineNum < h.StartLine+len(h.LineNums) {
									rendered[ic] = true
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
					} else {
						content := hunkLabel(f.Name, h)
						rawContent := content
						if d.incrementalMode && h.ReviewStatus == reviewstate.StatusNew {
							content += "  " + diffNewBadge.Render("new")
							rawContent += "  new"
						} else if h.Annotated && !h.Trivial {
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
									rendered[ic] = true
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

				// Comments that couldn't be placed at a specific diff line
				for _, c := range inlineByFile[f.Name] {
					if rendered[c] {
						continue // already rendered inline
					}
					rendered[c] = true
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

// fileAllNew returns true if all hunks in a file are new (incremental mode).
func (d *DiffView) fileAllNew(f *File) bool {
	if d.incremental == nil {
		return false
	}
	hunkStatuses, ok := d.incremental.HunkStatus[f.Name]
	if !ok {
		return false
	}
	for hi := range f.Hunks {
		if s, ok := hunkStatuses[hi]; !ok || s != reviewstate.StatusNew {
			return false
		}
	}
	return len(f.Hunks) > 0
}

// fileNewCommentCount returns the number of new/edited inline comments on a file.
func (d *DiffView) fileNewCommentCount(f *File) int {
	count := 0
	for _, c := range d.inline {
		if c.Path == f.Name && (c.IsNew || c.IsEdited) {
			count++
		}
	}
	return count
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
