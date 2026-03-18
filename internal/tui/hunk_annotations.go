package tui

import (
	"github.com/sleuth-io/prx/internal/ai"
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
	type key struct {
		file string
		line int
	}
	lookup := make(map[key]*ai.HunkAnnotation, len(card.Assessment.HunkAnnotations))
	for i := range card.Assessment.HunkAnnotations {
		a := &card.Assessment.HunkAnnotations[i]
		lookup[key{a.File, a.StartLine}] = a
	}

	for _, f := range card.parsedFiles {
		allTrivial := len(f.Hunks) > 0
		for _, h := range f.Hunks {
			if a, ok := lookup[key{f.Name, h.StartLine}]; ok {
				h.Trivial = a.Trivial
				h.TrivialReason = a.Reason
				if a.Trivial {
					h.Collapsed = true
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
