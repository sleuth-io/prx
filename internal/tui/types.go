package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/chat"
	"github.com/sleuth-io/prx/internal/tui/diff"
)

// SetProgramMsg delivers the tea.Program reference for streaming sends.
type SetProgramMsg struct {
	Program *tea.Program
}

// PRCard is a PR — may be in-progress (Scoring=true) or fully assessed.
type PRCard struct {
	PR                 *github.PR
	Assessment         *ai.Assessment
	WeightedScore      float64
	Verdict            string
	Scoring            bool
	ScoringErr         error
	parsedFiles        []*diff.File    // pre-parsed diff files (nil until ready)
	annotationsApplied bool            // true once hunk annotations have been applied
	chatMessages       []chat.Message  // in-memory chat history per PR
	chatContext        *ai.DiffContext // file/line the reviewer was looking at when chat opened
	chatCancel         func()          // cancels the running claude process (nil if not streaming)
	worktreePath       string          // git worktree path for chat (empty until created)
}

type prDiffParsedMsg struct {
	prNumber int
	files    []*diff.File
}

// scene represents the top-level UI mode — which full-screen view is shown.
type scene int

const (
	sceneReview      scene = iota // loading screen and main PR review
	sceneBulkApprove              // bulk approve overlay
)

// focus represents which panel is active within sceneReview.
type focus int

const (
	focusAssessment focus = iota
	focusDiff
	focusModal
	focusChat
)

type commentModal struct {
	active    bool
	isInline  bool
	filePath  string
	fileLine  int
	commitSHA string
	prevFocus focus
	textarea  textarea.Model
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

type prRefreshedMsg struct {
	prNumber int
	activity *github.PRActivity
	newDiff  string // non-empty when head SHA changed and diff was re-fetched
	err      error
}
