package diff

import (
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/reviewstate"
)

var (
	diffAddedStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Background(lipgloss.Color("#1a3a1a"))
	diffRemovedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Background(lipgloss.Color("#3a1a1a"))
	diffAddedHighlightStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Background(lipgloss.Color("22"))
	diffRemovedHighlightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Background(lipgloss.Color("52"))
	diffHunkStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Faint(true)
	diffFileStyle             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215"))

	commentStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	commentAuthorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	commentExpandedStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("214")).
				PaddingLeft(1)

	cursorLineStyle    = lipgloss.NewStyle().Background(lipgloss.Color("238"))
	cursorHunkStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("26"))
	cursorTrivialStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("240"))

	// Incremental review styles
	diffSeenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Background(lipgloss.Color("235")).Faint(true)
	diffNewBadge    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("82")).Padding(0, 1)
	diffEditedBadge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("69")).Padding(0, 1)
)

// collapsibleKind identifies what kind of item is at a viewport line.
type collapsibleKind int

const (
	kindFile         collapsibleKind = iota
	kindHunk                         // individual hunk within a file
	kindFilesGroup                   // header that wraps all diff files
	kindCommentGroup                 // top-level comments grouped by author
	kindComment                      // individual comment (inline, or expanded from group)
)

type collapsible struct {
	lineIdx    int
	kind       collapsibleKind
	fileIdx    int
	hunkIdx    int
	comment    *CommentItem
	group      *CommentGroup
	rawContent string         // unstyled text for re-rendering with cursor style
	baseStyle  lipgloss.Style // style used when not under cursor
}

// Hunk is a single diff hunk within a file.
type Hunk struct {
	HeaderLine    string
	Rendered      []string
	RawLines      []string // unstyled line content (parallel to Rendered)
	LineNums      []int
	Additions     int
	Deletions     int
	Collapsed     bool
	Trivial       bool
	Annotated     bool // true if AI provided an annotation for this hunk
	TrivialReason string
	StartLine     int // new-file line number from fragment header

	ReviewStatus reviewstate.HunkStatus // incremental: Seen or New
}

// File is a parsed diff file with pre-rendered colored lines.
type File struct {
	Name      string
	Collapsed bool
	Hunks     []*Hunk
}

// CommentItem is a single comment (inline or top-level).
type CommentItem struct {
	ID           int // GitHub comment ID
	InReplyToID  int // parent comment ID (0 if not a reply)
	Author       string
	Path         string // empty for top-level
	LineNum      int    // diff line number (0 if not line-specific)
	Body         string
	Collapsed    bool
	Pending      bool   // true while the API call is in-flight
	IsNew        bool   // incremental: comment not in last-seen set
	IsEdited     bool   // incremental: same ID, different body hash
	renderedBody string // cached markdown render
}

// CommentGroup groups top-level comments by author.
type CommentGroup struct {
	Author    string
	Comments  []*CommentItem
	Collapsed bool
}

// DiffView is a scrollable diff viewer with collapsible files and comments.
type DiffView struct {
	files          []*File
	filesCollapsed bool            // controls the files group header
	commentGroups  []*CommentGroup // top-level PR comments grouped by author
	inline         []*CommentItem  // inline code comments (grouped into files during render)
	collapsibles   []collapsible   // ordered list of all collapsible positions
	lines          []string        // built lines (no cursor highlight applied)
	cursorLine     int
	viewport       viewport.Model
	width          int
	height         int
	Focused        bool

	incrementalMode bool                          // true when showing incremental view
	incremental     *reviewstate.IncrementalState // nil if no prior review state
}

// DiffQuote captures a quoted line from the diff for embedding in chat.
type DiffQuote struct {
	File       string // file path
	Line       int    // line number
	RawContent string // unstyled line content (e.g. "+  def foo():")
	StyledLine string // ANSI-styled rendered line from the diff
}
