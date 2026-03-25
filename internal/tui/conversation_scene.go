package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sleuth-io/prx/internal/tui/chat"
	"github.com/sleuth-io/prx/internal/tui/conversation"
	"github.com/sleuth-io/prx/internal/tui/scoring"
	"github.com/sleuth-io/prx/internal/tui/style"
)

const maxInputLines = 5

// ConversationScene is the primary review/chat scene.
type ConversationScene struct {
	viewport     viewport.Model
	input        textarea.Model
	confirm      *confirmDialog
	actionStatus string
	actionDone   bool
	width        int
	height       int

	// Image overlay: rendered outside viewport to avoid layout corruption.
	imageOverlay    string // raw escape sequence
	imageContentRow int    // line in viewport content where image goes
	imageLinkRow    int    // line in viewport content where clickable link is
	imageURL        string // URL for clickable link below image
}

func newConversationScene() *ConversationScene {
	ta := textarea.New()
	ta.Placeholder = ""
	ta.CharLimit = 2000
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = lipgloss.NewStyle()
	ta.Prompt = ""

	vp := viewport.New(80, 20)

	return &ConversationScene{
		viewport: vp,
		input:    ta,
	}
}

// FocusInput gives focus to the input textarea.
func (s *ConversationScene) FocusInput() tea.Cmd {
	return s.input.Focus()
}

// ---------------------------------------------------------------------------
// Scene interface
// ---------------------------------------------------------------------------

func (s *ConversationScene) Update(msg tea.Msg, m *Model) (Scene, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return s.handleKey(msg, m)
	case tea.MouseMsg:
		return s.handleMouse(msg, m)
	case actionDoneMsg:
		return s.handleActionDone(msg, m)
	}
	return s, nil
}

func (s *ConversationScene) View(m *Model) string {
	width := s.width
	if width == 0 {
		width = 80
	}

	vpContent := lipgloss.JoinHorizontal(lipgloss.Top, s.viewport.View(), style.RenderScrollbar(s.viewport))

	// Claude Code-style input area with horizontal rules
	ruleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#0891b2"))
	rule := ruleStyle.Render(strings.Repeat("─", width))

	// Top rule with PR info right-aligned (max 30% of width)
	topRule := rule
	if card := m.currentCard(); card != nil && card.PR != nil {
		var prLabel string
		if m.multiRepo() {
			parts := strings.Split(card.Ctx.Repo, "/")
			prLabel = fmt.Sprintf("%s #%d - %s", parts[len(parts)-1], card.PR.Number, card.PR.Title)
		} else {
			prLabel = fmt.Sprintf("#%d - %s", card.PR.Number, card.PR.Title)
		}
		maxLen := width * 3 / 10
		if len(prLabel) > maxLen {
			prLabel = prLabel[:maxLen-1] + "…"
		}
		blueRule := lipgloss.NewStyle().Foreground(lipgloss.Color("#0891b2"))
		titleStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(lipgloss.Color("#0891b2"))
		label := blueRule.Render(" ") + titleStyle.Render(" "+prLabel+" ") + blueRule.Render(" ──")
		fillLen := width - lipgloss.Width(label)
		if fillLen > 0 {
			topRule = blueRule.Render(strings.Repeat("─", fillLen)) + label
		}
	}

	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	inputLine := promptStyle.Render("❯ ") + s.input.View()
	inputArea := lipgloss.JoinVertical(lipgloss.Left, topRule, inputLine, rule)

	parts := []string{vpContent, inputArea}
	if s.confirm != nil {
		parts = append(parts[:1], s.renderConfirmBanner(width))
		parts = append(parts, inputArea)
	} else if m.pendingPerm != nil {
		parts = append(parts[:1], renderPermBanner(m.pendingPerm, width))
		parts = append(parts, inputArea)
	}
	parts = append(parts, s.renderFooter(m))
	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Always clear previous Kitty images so they don't persist when
	// switching to a PR without an image.
	result += "\x1b_Ga=d,d=a\x1b\\"

	// Overlay image using cursor positioning (outside viewport content to
	// avoid layout corruption from Kitty/sixel escape sequences).
	if s.imageOverlay != "" {
		screenRow := s.imageContentRow - s.viewport.YOffset
		if screenRow >= 0 && screenRow < s.viewport.Height {
			// CSI save cursor, move to row, output image, restore cursor
			result += fmt.Sprintf("\x1b7\x1b[%d;1H%s\x1b8", screenRow+1, s.imageOverlay)
		}
	}

	return result
}

func (s *ConversationScene) Resize(width, height int) {
	s.width = width
	s.height = height
	inputH := s.input.Height() + 2 // +2 for top and bottom rules
	footerH := 1
	vpH := height - inputH - footerH
	if vpH < 4 {
		vpH = 4
	}
	s.viewport.Width = width - 1 // reserve for scrollbar
	s.viewport.Height = vpH
	s.input.SetWidth(width - 4)
}

// ---------------------------------------------------------------------------
// Scrollback
// ---------------------------------------------------------------------------

// BuildScrollback rebuilds the viewport content for the current PR.
func (s *ConversationScene) BuildScrollback(m *Model) {
	card := m.currentCard()
	if card == nil {
		s.viewport.SetContent("")
		return
	}

	width := s.width - 1 // reserve 1 for scrollbar
	if width < 40 {
		width = 40
	}

	var blocks []conversation.Block

	// 1. Assessment block (includes PR header)
	renderData := m.buildRenderData(card)
	assessmentContent := scoring.RenderInline(&renderData, width)
	blocks = append(blocks, &conversation.AssessmentBlock{
		Content: assessmentContent,
	})

	// Track image overlay (rendered outside viewport to avoid layout issues)
	s.imageOverlay, s.imageURL = scoring.ImageOverlay(&renderData)
	s.imageContentRow = renderData.BodyEndLine
	if m.imageCache != nil {
		s.imageLinkRow = s.imageContentRow + m.imageCache.PlaceholderLines()
	}

	// 2. Chat messages + streaming
	cs := card.Chat
	if len(cs.Messages) > 0 || cs.Streaming || cs.Status != "" {
		blocks = append(blocks, &conversation.ChatBlock{
			Messages: cs.Messages,
			Stream: chat.StreamState{
				Active:        cs.Streaming,
				Content:       cs.StreamContent,
				SpinnerView:   m.spinner.View(),
				ToolCallCount: cs.ToolCallCount,
				LastToolCall:  cs.LastToolCall,
				Status:        cs.Status,
				ThinkingSince: cs.StreamStart,
			},
		})
	}

	// 3. Action status
	if s.actionStatus != "" {
		blocks = append(blocks, &conversation.StatusBlock{
			Status:      s.actionStatus,
			Done:        s.actionDone,
			SpinnerView: m.spinner.View(),
		})
	}

	// Render all blocks
	var sections []string
	for _, b := range blocks {
		rendered := b.Render(width)
		if rendered != "" {
			sections = append(sections, rendered)
		}
	}

	content := strings.Join(sections, "\n")
	atBottom := s.viewport.AtBottom()
	s.viewport.SetContent(content)
	// Only auto-scroll when the user is already at the bottom or chat is actively
	// streaming. This lets users scroll up to read the assessment without being
	// yanked back down by spinner-tick rebuilds (e.g. during "Preparing chat...").
	if atBottom || (card.Chat != nil && card.Chat.Streaming) {
		s.viewport.GotoBottom()
	}
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

func (s *ConversationScene) updateInputHeight() {
	content := s.input.Value()
	lines := strings.Count(content, "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > maxInputLines {
		lines = maxInputLines
	}
	if lines != s.input.Height() {
		s.input.SetHeight(lines)
		s.Resize(s.width, s.height)
	}
}

// ---------------------------------------------------------------------------
// Rendering helpers
// ---------------------------------------------------------------------------

func (s *ConversationScene) renderConfirmBanner(width int) string {
	inner := fmt.Sprintf("%s\n[y] confirm   [n/esc] cancel", s.confirm.description)
	maxW := width - 4
	if maxW < 20 {
		maxW = 20
	}
	return permBannerStyle.Width(maxW).Render(inner)
}

func (s *ConversationScene) renderFooter(m *Model) string {
	width := s.width
	if width == 0 {
		width = 80
	}
	visible := m.visibleCardCount()
	visIdx := 0
	for i := 0; i < m.current && i < len(m.cards); i++ {
		if m.isCardVisible(m.cards[i]) {
			visIdx++
		}
	}
	status := fmt.Sprintf("prx  PR %d/%d", visIdx+1, visible)
	if s.actionStatus != "" && s.actionDone {
		status += fmt.Sprintf("  %s", s.actionStatus)
	} else if s.actionStatus != "" {
		status += fmt.Sprintf("  %s %s", m.spinner.View(), s.actionStatus)
	} else if pending := m.fetching + m.scoring; pending > 0 {
		status += fmt.Sprintf("  %s %d loading", m.spinner.View(), pending)
	}

	var hints string
	card := m.currentCard()
	if card != nil && card.PostMerge {
		toggleHint := "^a show all"
		if m.showAllMerged {
			toggleHint = "^a hide reviewed"
		}
		hints = fmt.Sprintf("^d diff  ^b bulk  ^n/^p nav  /approve  /flag  %s  ^q quit", toggleHint)
	} else {
		toggleHint := ""
		// Only show toggle hint if there are any post-merge cards.
		for _, c := range m.cards {
			if c.PostMerge {
				if m.showAllMerged {
					toggleHint = "  ^a hide reviewed"
				} else {
					toggleHint = "  ^a show all"
				}
				break
			}
		}
		hints = fmt.Sprintf("^d diff  ^b bulk  ^n/^p nav  /approve  /merge  /comment%s  ^q quit", toggleHint)
	}
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}

// renderPermBanner renders the permission request banner.
func renderPermBanner(perm *permRequestMsg, width int) string {
	inner := fmt.Sprintf("Claude wants to: %s\n[y] allow   [n] deny", perm.description)
	maxW := width - 4
	if maxW < 20 {
		maxW = 20
	}
	return permBannerStyle.Width(maxW).Render(inner)
}
