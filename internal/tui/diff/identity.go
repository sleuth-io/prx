package diff

import (
	"strings"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/reviewstate"
)

// HunkContentHash returns a stable content hash for a hunk's raw diff lines.
func HunkContentHash(h *Hunk) string {
	return reviewstate.ContentHash(strings.Join(h.RawLines, "\n"))
}

// DigestsFromFiles produces hunk digests for all hunks across all files.
func DigestsFromFiles(files []*File) []reviewstate.HunkDigest {
	var digests []reviewstate.HunkDigest
	for _, f := range files {
		for _, h := range f.Hunks {
			digests = append(digests, reviewstate.HunkDigest{
				File:      f.Name,
				StartLine: h.StartLine,
				Hash:      HunkContentHash(h),
			})
		}
	}
	return digests
}

// CommentBodyHash returns a content hash for a comment body.
func CommentBodyHash(body string) string {
	return reviewstate.ContentHash(body)
}

// CommentDigestsFromPR produces comment digests from a PR's inline and top-level comments.
// If excludeUser is provided, comments by that user are excluded — this prevents
// the current user's own comments from being tracked or flagged as new.
func CommentDigestsFromPR(pr *github.PR, excludeUser ...string) []reviewstate.CommentDigest {
	exclude := ""
	if len(excludeUser) > 0 {
		exclude = excludeUser[0]
	}
	var digests []reviewstate.CommentDigest
	for _, c := range pr.InlineComments {
		if c.ID != 0 && c.Author != exclude {
			digests = append(digests, reviewstate.CommentDigest{
				ID:   c.ID,
				Hash: CommentBodyHash(c.Body),
			})
		}
	}
	for _, c := range pr.Comments {
		if c.ID != 0 && c.Author != exclude {
			digests = append(digests, reviewstate.CommentDigest{
				ID:   c.ID,
				Hash: CommentBodyHash(c.Body),
			})
		}
	}
	return digests
}

// FileHunkInfo extracts minimal hunk info for incremental computation.
func FileHunkInfo(files []*File) (fileNames []string, fileHunks [][]reviewstate.HunkInfo) {
	for _, f := range files {
		fileNames = append(fileNames, f.Name)
		var hunks []reviewstate.HunkInfo
		for _, h := range f.Hunks {
			hunks = append(hunks, reviewstate.HunkInfo{
				StartLine: h.StartLine,
				Hash:      HunkContentHash(h),
			})
		}
		fileHunks = append(fileHunks, hunks)
	}
	return
}
