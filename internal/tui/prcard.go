package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/conversation"
	"github.com/sleuth-io/prx/internal/tui/diff"
)

// PRCard is a PR — may be in-progress (Scoring=true) or fully assessed.
type PRCard struct {
	Ctx                 *app.RepoContext // per-repo context (repo name, dir, shared app)
	PR                  *github.PR
	Assessment          *ai.Assessment
	WeightedScore       float64
	Verdict             string
	Scoring             bool
	ScoringErr          error
	ScoringToolCount    int
	ScoringLastTool     string
	ScoringStatus       string
	parsedFiles         []*diff.File // pre-parsed diff files (nil until ready)
	annotationsApplied  bool         // true once hunk annotations have been applied
	Chat                *conversation.ChatSession
	PostMerge           bool   // true if this is a merged PR in post-merge review
	MergedStatusChecked bool   // true once the merged PR status fetch has completed
	UserHasReviewed     bool   // user left a review (approve/reject/comment) pre-merge
	UserHasReacted      bool   // user already reacted (+1/-1)
	UserReaction        string // "+1" or "-1" — set when user reacts in this session
}

type commentModal struct {
	active    bool
	isInline  bool
	filePath  string
	fileLine  int
	commitSHA string
	textarea  textarea.Model
}
