package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/ai"
)

var (
	chatUserStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true)
	chatAssistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	chatDimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// ChatView is a chat panel for asking questions about a PR.
type ChatView struct {
	messages      []chatMessage
	input         textarea.Model
	viewport      viewport.Model
	status        string // non-empty = shown with spinner (e.g. "Creating worktree...", "Starting Claude...")
	welcomeText   string // shown when chat is empty and ready
	streaming     bool
	streamContent string // content being streamed for current response
	spinnerView   string // rendered spinner frame (set by parent)
	diffContext   *ai.DiffContext // file/line context from diff cursor
	width, height int
	Focused       bool
}

func NewChatView(width, height int) ChatView {
	ta := textarea.New()
	ta.Placeholder = "Ask about this PR..."
	ta.CharLimit = 2000
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()

	vp := viewport.New(width, height-3) // 3 = input(1) + title(1) + border(1)
	return ChatView{
		input:    ta,
		viewport: vp,
		width:    width,
		height:   height,
	}
}

func (c *ChatView) SetSize(width, height int) {
	c.width = width
	c.height = height
	inputH := 1
	titleH := 1
	vpH := height - inputH - titleH
	if vpH < 2 {
		vpH = 2
	}
	c.viewport.Width = width - 1 // reserve 1 for scrollbar
	c.viewport.Height = vpH
	c.input.SetWidth(width - 4)
	c.rebuildViewport()
}

func (c *ChatView) SetContext(ctx *ai.DiffContext) {
	c.diffContext = ctx
	if ctx != nil && ctx.File != "" {
		if ctx.Line > 0 {
			c.input.Placeholder = fmt.Sprintf("Ask about %s:%d...", ctx.File, ctx.Line)
		} else {
			c.input.Placeholder = fmt.Sprintf("Ask about %s...", ctx.File)
		}
	} else {
		c.input.Placeholder = "Ask about this PR..."
	}
}

func (c *ChatView) SetMessages(msgs []chatMessage) {
	c.messages = msgs
	c.rebuildViewport()
}

func (c *ChatView) AppendToken(token string) {
	c.status = "" // clear any init status once tokens flow
	c.streamContent += token
	c.rebuildViewport()
}

func (c *ChatView) FinishStream(fullResponse string) {
	c.streaming = false
	c.streamContent = ""
	c.status = ""
	// The full message is added to messages by the caller
	c.rebuildViewport()
}

func (c *ChatView) rebuildViewport() {
	var lines []string
	w := c.width - 4

	for _, msg := range c.messages {
		if msg.role == "user" {
			lines = append(lines, "")
			lines = append(lines, chatUserStyle.Render("You:"))
			wrapped := lipgloss.NewStyle().Width(w).Render(msg.content)
			lines = append(lines, strings.Split(wrapped, "\n")...)
		} else {
			lines = append(lines, "")
			lines = append(lines, chatAssistantStyle.Render("Claude:"))
			wrapped := lipgloss.NewStyle().Width(w).Render(msg.content)
			lines = append(lines, strings.Split(wrapped, "\n")...)
		}
	}

	// Status indicators
	if c.streaming && c.streamContent != "" {
		lines = append(lines, "")
		lines = append(lines, chatAssistantStyle.Render("Claude:"))
		wrapped := lipgloss.NewStyle().Width(w).Render(c.streamContent)
		lines = append(lines, strings.Split(wrapped, "\n")...)
		lines = append(lines, chatDimStyle.Render("▊"))
	} else if c.streaming {
		lines = append(lines, "")
		lines = append(lines, chatDimStyle.Render(c.spinnerView+" Thinking..."))
	} else if c.status != "" {
		lines = append(lines, "")
		lines = append(lines, chatDimStyle.Render(c.spinnerView+" "+c.status))
	} else if len(c.messages) == 0 && c.welcomeText != "" {
		lines = append(lines, "")
		for _, wl := range strings.Split(c.welcomeText, "\n") {
			lines = append(lines, chatDimStyle.Render("  "+wl))
		}
	}

	c.viewport.SetContent(strings.Join(lines, "\n"))
	c.viewport.GotoBottom()
}

func (c ChatView) Update(msg tea.Msg) (ChatView, tea.Cmd) {
	if !c.Focused {
		return c, nil
	}

	// Enter and esc are handled by the parent (tui.go); only forward other keys to textarea
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

func (c *ChatView) InputValue() string {
	return strings.TrimSpace(c.input.Value())
}

func (c *ChatView) ResetInput() {
	c.input.Reset()
}

func (c *ChatView) Focus() tea.Cmd {
	return c.input.Focus()
}

func (c *ChatView) Blur() {
	c.input.Blur()
}

// ChatTabName returns the label for the chat tab.
func (c ChatView) ChatTabName() string {
	if c.diffContext != nil && c.diffContext.File != "" {
		if c.diffContext.Line > 0 {
			return fmt.Sprintf("Chat — %s:%d", c.diffContext.File, c.diffContext.Line)
		}
		return fmt.Sprintf("Chat — %s", c.diffContext.File)
	}
	return "Chat"
}

// ViewContent renders the chat body without a title bar (for tabbed layout).
func (c ChatView) ViewContent() string {
	width := c.width
	if width == 0 {
		width = 80
	}
	vpContent := lipgloss.JoinHorizontal(lipgloss.Top, c.viewport.View(), renderScrollbar(c.viewport))

	inputBorder := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(lipgloss.Color("237")).
		Width(width)
	inputArea := inputBorder.Render(c.input.View())

	return lipgloss.JoinVertical(lipgloss.Left, vpContent, inputArea)
}

func (c ChatView) View() string {
	titleStyle := panelTitleBlurred
	hint := " tab to focus"
	if c.Focused {
		titleStyle = panelTitleFocused
		hint = " enter send  alt+enter newline  esc back to diff"
	}
	width := c.width
	if width == 0 {
		width = 80
	}
	panelName := c.ChatTabName()
	title := titleStyle.Render(panelName) + dimPanelHint(hint, titleStyle, width, panelName)
	return lipgloss.JoinVertical(lipgloss.Left, title, c.ViewContent())
}
