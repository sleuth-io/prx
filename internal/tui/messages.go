package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/diff"
)

// SetProgramMsg delivers the tea.Program reference for streaming sends.
type SetProgramMsg struct {
	Program *tea.Program
}

type prDiffParsedMsg struct {
	repo     string
	prNumber int
	files    []*diff.File
}

type prListFetchedMsg struct {
	ctx    *app.RepoContext
	rawPRs []map[string]any
	err    error
}

type prDetailsFetchedMsg struct {
	ctx *app.RepoContext
	pr  *github.PR
	raw map[string]any
	err error
}

type prScoredMsg struct {
	repo       string
	prNumber   int
	assessment *ai.Assessment
	err        error
	fromCache  bool
}

const (
	actionMerge            = "merge"
	actionApprove          = "approve"
	actionRequestChanges   = "request-changes"
	actionPostMergeApprove = "post-merge-approve"
	actionPostMergeFlag    = "post-merge-flag"
)

type actionDoneMsg struct {
	repo   string
	pr     int
	action string
	err    error
}

type scoringToolCallMsg struct {
	repo     string
	prNumber int
	count    int
	lastTool string
}

type scoringStatusMsg struct {
	repo     string
	prNumber int
	status   string
}

type chatStatusMsg struct {
	repo     string
	prNumber int
	status   string
}

type chatTokenMsg struct {
	repo     string
	prNumber int
	token    string
}

type chatDoneMsg struct {
	repo         string
	prNumber     int
	fullResponse string
	err          error
}

type chatToolCallMsg struct {
	repo     string
	prNumber int
	count    int
	lastTool string
}

type chatWorktreeReadyMsg struct {
	repo     string
	prNumber int
	path     string
	err      error
}

type permRequestMsg struct {
	description string
	respond     func(allowed bool)
}

type confirmDialog struct {
	description  string
	actionStatus string // set on m.actionStatus when confirmed
	cmd          tea.Cmd
}

type commentSubmittedMsg struct {
	repo        string
	prNumber    int
	isInline    bool
	filePath    string
	fileLine    int
	body        string
	pendingItem *diff.CommentItem
	err         error
}

type prRefreshedMsg struct {
	repo     string
	prNumber int
	activity *github.PRActivity
	newDiff  string // non-empty when head SHA changed and diff was re-fetched
	err      error
}

type imageFetchedMsg struct {
	repo     string
	prNumber int
	url      string
	err      error
}

type mergedPRListFetchedMsg struct {
	ctx    *app.RepoContext
	rawPRs []map[string]any
	err    error
}

type trackedPRListFetchedMsg struct {
	ctx    *app.RepoContext
	rawPRs []map[string]any
	err    error
}

type mergedPRStatusMsg struct {
	repo        string
	prNumber    int
	hasReview   bool
	hasReaction bool
	err         error
}
