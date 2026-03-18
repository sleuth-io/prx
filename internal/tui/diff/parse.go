package diff

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/sleuth-io/prx/internal/logger"
)

var dmp = diffmatchpatch.New()

// ParseDiff parses a unified diff string into per-file colored lines.
func ParseDiff(raw string) []*File {
	files, _, err := gitdiff.Parse(strings.NewReader(raw))
	if err != nil {
		logger.Debug("parseDiff: raw_len=%d files=%d err=%v", len(raw), len(files), err)
	}
	if len(files) == 0 {
		return []*File{{
			Name: "diff",
			Hunks: []*Hunk{{
				HeaderLine: diffHunkStyle.Render("  raw diff"),
				Rendered:   colorRawDiff(raw),
			}},
		}}
	}

	result := make([]*File, 0, len(files))
	for _, f := range files {
		name := f.NewName
		if name == "" || name == "/dev/null" {
			name = f.OldName
		}
		hunks := renderFileDiff(f)
		result = append(result, &File{
			Name:  name,
			Hunks: hunks,
		})
	}
	return result
}

func renderFileDiff(f *gitdiff.File) []*Hunk {
	lexer := detectLexer(f.NewName, f.OldName)
	var hunks []*Hunk

	for _, frag := range f.TextFragments {
		comment := strings.TrimSpace(frag.Comment)
		linePos := frag.NewPosition
		if linePos == 0 {
			linePos = frag.OldPosition
		}
		_ = comment
		hunkHeader := fmt.Sprintf("  line %d", linePos)
		startLine := int(frag.NewPosition)
		if startLine == 0 {
			startLine = int(frag.OldPosition)
		}
		rendered, lineNums := renderFragmentLines(frag.Lines, lexer, int(frag.NewPosition))
		hunks = append(hunks, &Hunk{
			HeaderLine: hunkHeader,
			Rendered:   rendered,
			LineNums:   lineNums,
			StartLine:  startLine,
		})
	}
	return hunks
}

// renderFragmentLines renders diff lines, pairing equal-count remove/add runs for inline diff.
func renderFragmentLines(fragLines []gitdiff.Line, lexer chroma.Lexer, startLine int) ([]string, []int) {
	var out []string
	var nums []int
	newLine := startLine
	i := 0
	for i < len(fragLines) {
		// Collect consecutive deletions
		j := i
		for j < len(fragLines) && fragLines[j].Op == gitdiff.OpDelete {
			j++
		}
		// Collect consecutive additions immediately after
		k := j
		for k < len(fragLines) && fragLines[k].Op == gitdiff.OpAdd {
			k++
		}

		numDels := j - i
		numAdds := k - j

		if numDels > 0 && numAdds > 0 && numDels == numAdds {
			for n := 0; n < numDels; n++ {
				old := strings.TrimRight(fragLines[i+n].Line, "\n")
				new := strings.TrimRight(fragLines[j+n].Line, "\n")
				out = append(out, renderInlineDiffLines(old, new)...)
				nums = append(nums, -1, newLine)
				newLine++
			}
			i = k
		} else if numDels == 0 {
			l := fragLines[i]
			out = append(out, renderDiffLine(l, lexer))
			switch l.Op {
			case gitdiff.OpAdd, gitdiff.OpContext:
				nums = append(nums, newLine)
				newLine++
			default:
				nums = append(nums, -1)
			}
			i++
		} else {
			for _, l := range fragLines[i:k] {
				out = append(out, renderDiffLine(l, lexer))
				switch l.Op {
				case gitdiff.OpAdd, gitdiff.OpContext:
					nums = append(nums, newLine)
					newLine++
				default:
					nums = append(nums, -1)
				}
			}
			i = k
		}
	}
	return out, nums
}

func renderInlineDiffLines(old, new string) []string {
	diffs := dmp.DiffMain(old, new, false)
	dmp.DiffCleanupSemantic(diffs)

	var oldLine, newLine strings.Builder
	oldLine.WriteString(diffRemovedStyle.Render("-"))
	newLine.WriteString(diffAddedStyle.Render("+"))

	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			oldLine.WriteString(diffRemovedStyle.Render(d.Text))
			newLine.WriteString(diffAddedStyle.Render(d.Text))
		case diffmatchpatch.DiffDelete:
			oldLine.WriteString(diffRemovedHighlightStyle.Render(d.Text))
		case diffmatchpatch.DiffInsert:
			newLine.WriteString(diffAddedHighlightStyle.Render(d.Text))
		}
	}
	return []string{oldLine.String(), newLine.String()}
}

func withLineBg(s string, hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	var r, g, b int
	_, _ = fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	bg := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	return bg + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bg)
}

func renderDiffLine(line gitdiff.Line, lexer chroma.Lexer) string {
	content := strings.TrimRight(line.Line, "\n")
	switch line.Op {
	case gitdiff.OpAdd:
		return diffAddedStyle.Render("+") + withLineBg(syntaxHighlight(content, lexer), "#1a3a1a")
	case gitdiff.OpDelete:
		return diffRemovedStyle.Render("-") + withLineBg(syntaxHighlight(content, lexer), "#3a1a1a")
	default:
		return " " + syntaxHighlight(content, lexer)
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
