package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/github"
)

// PRCard is a PR — may be in-progress (Scoring=true) or fully assessed.
type PRCard struct {
	PR            *github.PR
	Assessment    *ai.Assessment
	WeightedScore float64
	Verdict       string
	Scoring       bool
	ScoringErr    error
	parsedFiles   []*diffFile // pre-parsed diff files (nil until ready)
}

type prDiffParsedMsg struct {
	prNumber int
	files    []*diffFile
}

type focus int

const (
	focusAssessment focus = iota
	focusDiff
	focusModal
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
	pendingItem *commentItem
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

type actionDoneMsg struct {
	pr     int
	action string
	err    error
}
