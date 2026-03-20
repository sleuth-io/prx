package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/tui/conversation"
	"github.com/sleuth-io/prx/internal/tui/diff"
)

// PRCard is a PR — may be in-progress (Scoring=true) or fully assessed.
type PRCard struct {
	PR                 *github.PR
	Assessment         *ai.Assessment
	WeightedScore      float64
	Verdict            string
	Scoring            bool
	ScoringErr         error
	ScoringToolCount   int
	ScoringLastTool    string
	ScoringStatus      string
	parsedFiles        []*diff.File // pre-parsed diff files (nil until ready)
	annotationsApplied bool         // true once hunk annotations have been applied
	Chat               *conversation.ChatSession
}

type commentModal struct {
	active    bool
	isInline  bool
	filePath  string
	fileLine  int
	commitSHA string
	textarea  textarea.Model
}
