package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	chatUserStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true)
	chatAssistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	chatDimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// Message is a single message in a PR chat conversation.
type Message struct {
	Role    string // "user" or "assistant"
	Content string
}

// View is a chat panel for asking questions about a PR.
type View struct {
	messages      []Message
	input         textarea.Model
	viewport      viewport.Model
	Status        string // non-empty = shown with spinner
	WelcomeText   string // shown when chat is empty and ready
	Streaming     bool
	StreamContent string // content being streamed for current response
	SpinnerView   string // rendered spinner frame (set by parent)
	ToolCallCount int    // number of tool calls made so far
	LastToolCall  string // name of most recent tool call
	diffContext   *ai.DiffContext
	width, height int
	Focused       bool
}

func New(width, height int) View {
	ta := textarea.New()
	ta.Placeholder = "Ask about this PR..."
	ta.CharLimit = 2000
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()

	vp := viewport.New(width, height-3) // 3 = input(1) + title(1) + border(1)
	return View{
		input:    ta,
		viewport: vp,
		width:    width,
		height:   height,
	}
}

func (c *View) SetSize(width, height int) {
	c.width = width
	c.height = height
	inputH := 1
	titleH := 1
	vpH := height - inputH - titleH
	if vpH < 2 {
		vpH = 2
	}
	c.viewport.Width = width - 1
	c.viewport.Height = vpH
	c.input.SetWidth(width - 4)
	c.RebuildViewport()
}

func (c *View) SetContext(ctx *ai.DiffContext) {
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

func (c *View) SetMessages(msgs []Message) {
	c.messages = msgs
	c.RebuildViewport()
}

func (c *View) AppendToken(token string) {
	c.Status = ""
	c.StreamContent += token
	c.RebuildViewport()
}

func (c *View) FinishStream(fullResponse string) {
	c.Streaming = false
	c.StreamContent = ""
	c.Status = ""
	c.ToolCallCount = 0
	c.LastToolCall = ""
	c.RebuildViewport()
}

func (c *View) RebuildViewport() {
	var lines []string
	w := c.width - 4

	for _, msg := range c.messages {
		if msg.Role == "user" {
			lines = append(lines, "")
			lines = append(lines, chatUserStyle.Render("You:"))
			wrapped := lipgloss.NewStyle().Width(w).Render(msg.Content)
			lines = append(lines, strings.Split(wrapped, "\n")...)
		} else {
			lines = append(lines, "")
			lines = append(lines, chatAssistantStyle.Render("Claude:"))
			wrapped := lipgloss.NewStyle().Width(w).Render(msg.Content)
			lines = append(lines, strings.Split(wrapped, "\n")...)
		}
	}

	if c.Streaming && c.StreamContent != "" {
		lines = append(lines, "")
		lines = append(lines, chatAssistantStyle.Render("Claude:"))
		wrapped := lipgloss.NewStyle().Width(w).Render(c.StreamContent)
		lines = append(lines, strings.Split(wrapped, "\n")...)
		if c.ToolCallCount > 0 && c.LastToolCall != "" {
			s := chatDimStyle.Width(w).Render(fmt.Sprintf("  %s (%d tool calls, last: %s)", c.SpinnerView, c.ToolCallCount, c.LastToolCall))
			lines = append(lines, strings.Split(s, "\n")...)
		} else {
			lines = append(lines, chatDimStyle.Render("\u258a"))
		}
	} else if c.Streaming {
		lines = append(lines, "")
		if c.ToolCallCount > 0 && c.LastToolCall != "" {
			s := chatDimStyle.Width(w).Render(fmt.Sprintf("%s Thinking... (%d tool calls, last: %s)", c.SpinnerView, c.ToolCallCount, c.LastToolCall))
			lines = append(lines, strings.Split(s, "\n")...)
		} else {
			lines = append(lines, chatDimStyle.Render(c.SpinnerView+" Thinking..."))
		}
	} else if c.Status != "" {
		lines = append(lines, "")
		s := chatDimStyle.Width(w).Render(c.SpinnerView + " " + c.Status)
		lines = append(lines, strings.Split(s, "\n")...)
	} else if len(c.messages) == 0 && c.WelcomeText != "" {
		lines = append(lines, "")
		wrapped := chatDimStyle.Width(w).Render(c.WelcomeText)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}

	c.viewport.SetContent(strings.Join(lines, "\n"))
	c.viewport.GotoBottom()
}

func (c View) Update(msg tea.Msg) (View, tea.Cmd) {
	if !c.Focused {
		return c, nil
	}

	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

func (c *View) InputValue() string {
	return strings.TrimSpace(c.input.Value())
}

func (c *View) ResetInput() {
	c.input.Reset()
}

func (c *View) Focus() tea.Cmd {
	return c.input.Focus()
}

func (c *View) Blur() {
	c.input.Blur()
}

// TabName returns the label for the chat tab.
func (c View) TabName() string {
	if c.diffContext != nil && c.diffContext.File != "" {
		if c.diffContext.Line > 0 {
			return fmt.Sprintf("Chat \u2014 %s:%d", c.diffContext.File, c.diffContext.Line)
		}
		return fmt.Sprintf("Chat \u2014 %s", c.diffContext.File)
	}
	return "Chat"
}

// ViewContent renders the chat body without a title bar (for tabbed layout).
func (c View) ViewContent() string {
	width := c.width
	if width == 0 {
		width = 80
	}
	vpContent := lipgloss.JoinHorizontal(lipgloss.Top, c.viewport.View(), style.RenderScrollbar(c.viewport))

	inputBorder := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(lipgloss.Color("237")).
		Width(width)
	inputArea := inputBorder.Render(c.input.View())

	return lipgloss.JoinVertical(lipgloss.Left, vpContent, inputArea)
}

func (c View) View() string {
	hint := "tab to focus"
	if c.Focused {
		hint = "enter send  alt+enter newline  esc back to diff"
	}
	width := c.width
	if width == 0 {
		width = 80
	}
	title := style.RenderPanelTitle(c.TabName(), hint, c.Focused, width)
	return lipgloss.JoinVertical(lipgloss.Left, title, c.ViewContent())
}
