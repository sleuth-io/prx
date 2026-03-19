package tui

import (
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/logger"
)

// applyHunkAnnotations matches AI hunk annotations to parsed diff hunks,
// setting trivial/collapsed state. If all hunks in a file are trivial,
// the file is collapsed.
func applyHunkAnnotations(card *PRCard) {
	if card.Assessment == nil || len(card.Assessment.HunkAnnotations) == 0 || card.parsedFiles == nil {
		return
	}
	if card.annotationsApplied {
		return
	}
	card.annotationsApplied = true

	// Build lookup: (file, startLine) -> annotation
	lookup := make(map[hunkKey]*ai.HunkAnnotation, len(card.Assessment.HunkAnnotations))
	for i := range card.Assessment.HunkAnnotations {
		a := &card.Assessment.HunkAnnotations[i]
		lookup[hunkKey{a.File, a.StartLine}] = a
	}

	for _, f := range card.parsedFiles {
		allTrivial := len(f.Hunks) > 0
		for _, h := range f.Hunks {
			// Exact match first, then fuzzy match within ±3 lines
			a, ok := lookup[hunkKey{f.Name, h.StartLine}]
			if !ok {
				a = fuzzyMatch(lookup, f.Name, h.StartLine, 3)
			}

			if a != nil {
				h.Annotated = true
				h.Trivial = a.Trivial
				h.TrivialReason = a.Reason
				if a.Trivial {
					h.Collapsed = true
				}
			} else {
				logger.Info("hunk unmatched: %s:%d", f.Name, h.StartLine)
				if card.Assessment.DiffTruncated {
					h.TrivialReason = "(beyond diff limit)"
				} else {
					h.TrivialReason = "(not rated)"
				}
			}
			if !h.Trivial {
				allTrivial = false
			}
		}
		if allTrivial {
			f.Collapsed = true
		}
	}
}

type hunkKey struct {
	file string
	line int
}

// fuzzyMatch finds an annotation for the given file within ±tolerance lines.
func fuzzyMatch(lookup map[hunkKey]*ai.HunkAnnotation, file string, line, tolerance int) *ai.HunkAnnotation {
	for delta := 1; delta <= tolerance; delta++ {
		if a, ok := lookup[hunkKey{file, line + delta}]; ok {
			return a
		}
		if a, ok := lookup[hunkKey{file, line - delta}]; ok {
			return a
		}
	}
	return nil
}
