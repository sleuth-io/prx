package github

import (
	"fmt"
	"strings"
)

// Reaction represents a GitHub reaction on an issue/PR.
type Reaction struct {
	ID      int
	User    string
	Content string // "+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"
}

type CheckStatus struct {
	Name       string
	Conclusion string
}

type ReviewComment struct {
	ID          int // GitHub comment ID (stable across rebases)
	InReplyToID int // parent comment ID (0 if not a reply)
	Author      string
	Body        string
	State       string // only set for PR-level reviews
	Path        string // only set for inline comments
	Line        int    // only set for inline comments
	SubmittedAt string
}

type PR struct {
	Number             int
	Title              string
	Author             string
	URL                string
	CreatedAt          string
	Additions          int
	Deletions          int
	FilesChanged       int
	Diff               string
	Body               string
	HeadSHA            string
	HeadRefName        string
	State              string // "OPEN", "MERGED", or "CLOSED"
	MergeStateStatus   string // "CLEAN", "BLOCKED", "DIRTY", "BEHIND", "UNSTABLE", "UNKNOWN", etc.
	Checks             []CheckStatus
	Reviews            []ReviewComment // PR-level review submissions (APPROVED, CHANGES_REQUESTED, etc.)
	InlineComments     []ReviewComment // line-level code comments
	Comments           []ReviewComment // top-level conversation comments
	RequestedReviewers []string
}

// HasPendingChecks returns true if any check is still running (no conclusion yet).
func (pr *PR) HasPendingChecks() bool {
	for _, c := range pr.Checks {
		if c.Conclusion == "" {
			return true
		}
	}
	return false
}

// MergeBlockReason returns a non-empty string describing why the PR cannot be merged,
// or empty string if it is safe to proceed with merge.
// BEHIND is allowed (GitHub can auto-merge); DIRTY/BLOCKED/UNSTABLE are hard blocks.
func (pr *PR) MergeBlockReason() string {
	if pr.State == "MERGED" {
		return "PR is already merged"
	}
	if pr.State == "CLOSED" {
		return "PR is closed"
	}
	if pr.HasFailingChecks() {
		return "checks are failing"
	}
	if pr.HasPendingChecks() {
		return "checks are still running"
	}
	switch pr.MergeStateStatus {
	case "", "CLEAN", "UNKNOWN", "BEHIND", "HAS_HOOKS":
		return ""
	case "DIRTY":
		return "PR has merge conflicts"
	case "BLOCKED":
		return "PR is blocked by branch protection"
	case "UNSTABLE":
		return "status checks are failing"
	default:
		return fmt.Sprintf("PR cannot be merged (%s)", pr.MergeStateStatus)
	}
}

// MergeWarnReason returns a warning message for states that are allowed but not ideal.
func (pr *PR) MergeWarnReason() string {
	if pr.MergeStateStatus == "BEHIND" {
		return "branch is behind base"
	}
	return ""
}

func (pr *PR) HasFailingChecks() bool {
	for _, c := range pr.Checks {
		if c.Conclusion == "failure" || c.Conclusion == "cancelled" || c.Conclusion == "timed_out" {
			return true
		}
	}
	return false
}

func (pr *PR) ChecksSummary() string {
	if len(pr.Checks) == 0 {
		return "no checks"
	}
	var failed, pending []string
	passed := 0
	for _, c := range pr.Checks {
		switch c.Conclusion {
		case "success":
			passed++
		case "failure", "cancelled", "timed_out":
			failed = append(failed, c.Name)
		default:
			pending = append(pending, c.Name)
		}
	}
	var parts []string
	if passed > 0 {
		parts = append(parts, fmt.Sprintf("%d passed", passed))
	}
	for _, name := range failed {
		parts = append(parts, fmt.Sprintf("FAIL: %s", name))
	}
	for _, name := range pending {
		parts = append(parts, fmt.Sprintf("pending: %s", name))
	}
	return strings.Join(parts, "  ·  ")
}

// PRActivity holds the mutable parts of a PR — everything except the diff.
// Also includes metadata that can change (title, body, head SHA).
// Used for lightweight refreshes after actions like comment/approve/merge.
type PRActivity struct {
	// Metadata that may change between refreshes
	Title            string
	Body             string
	HeadSHA          string
	HeadRefName      string
	State            string // "OPEN", "MERGED", or "CLOSED"
	MergeStateStatus string
	// Live activity
	Checks         []CheckStatus
	Reviews        []ReviewComment
	InlineComments []ReviewComment
	Comments       []ReviewComment
}
