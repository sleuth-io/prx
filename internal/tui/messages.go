package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/diff"
)

// SetProgramMsg delivers the tea.Program reference for streaming sends.
type SetProgramMsg struct {
	Program *tea.Program
}

type prDiffParsedMsg struct {
	prNumber int
	files    []*diff.File
}

type prListFetchedMsg struct {
	rawPRs []map[string]any
	err    error
}

type prDetailsFetchedMsg struct {
	pr  *github.PR
	raw map[string]any
	err error
}

type prScoredMsg struct {
	prNumber   int
	assessment *ai.Assessment
	err        error
	fromCache  bool
}

const (
	actionMerge          = "merge"
	actionApprove        = "approve"
	actionRequestChanges = "request-changes"
)

type actionDoneMsg struct {
	pr     int
	action string
	err    error
}

type scoringToolCallMsg struct {
	prNumber int
	count    int
	lastTool string
}

type scoringStatusMsg struct {
	prNumber int
	status   string
}

type chatStatusMsg struct {
	prNumber int
	status   string
}

type chatTokenMsg struct {
	prNumber int
	token    string
}

type chatDoneMsg struct {
	prNumber     int
	fullResponse string
	err          error
}

type chatToolCallMsg struct {
	prNumber int
	count    int
	lastTool string
}

type chatWorktreeReadyMsg struct {
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
	prNumber    int
	isInline    bool
	filePath    string
	fileLine    int
	body        string
	pendingItem *diff.CommentItem
	err         error
}

type prRefreshedMsg struct {
	prNumber int
	activity *github.PRActivity
	newDiff  string // non-empty when head SHA changed and diff was re-fetched
	err      error
}
