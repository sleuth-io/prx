package conversation

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/tui/chat"
	"github.com/sleuth-io/prx/internal/tui/style"
)

// Block is a renderable section of the conversation scrollback.
type Block interface {
	Render(width int) string
}

// AssessmentBlock renders the PR header and risk assessment.
type AssessmentBlock struct {
	Content string // pre-rendered by scoring.RenderInline
}

func (b *AssessmentBlock) Render(_ int) string {
	return b.Content
}

// ChatBlock renders chat messages and streaming state.
type ChatBlock struct {
	Messages []chat.Message
	Stream   chat.StreamState
}

func (b *ChatBlock) Render(width int) string {
	return chat.RenderMessages(b.Messages, width, b.Stream)
}

var (
	userPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// UserMessageBlock renders a user chat message.
type UserMessageBlock struct {
	Content string
}

func (b *UserMessageBlock) Render(width int) string {
	w := width - 4
	wrapped := lipgloss.NewStyle().Width(w - 2).Render(b.Content)
	bg := lipgloss.NewStyle().Background(lipgloss.Color("236")).Width(w)
	var lines []string
	lines = append(lines, "")
	for i, line := range strings.Split(wrapped, "\n") {
		if i == 0 {
			lines = append(lines, bg.Render(userPromptStyle.Render("❯ ")+line))
		} else {
			lines = append(lines, bg.Render("  "+line))
		}
	}
	return strings.Join(lines, "\n")
}

// AssistantMessageBlock renders an assistant chat message.
type AssistantMessageBlock struct {
	Content string
}

func (b *AssistantMessageBlock) Render(width int) string {
	w := width - 4
	rendered := style.RenderMarkdown(b.Content, w)
	return "\n" + rendered
}

// StreamingBlock renders the active streaming state.
type StreamingBlock struct {
	Content       string
	SpinnerView   string
	ToolCallCount int
	LastToolCall  string
	Status        string
}

func (b *StreamingBlock) Render(width int) string {
	w := width - 4
	var lines []string
	lines = append(lines, "")
	if b.Content != "" {
		wrapped := style.RenderMarkdown(b.Content, w)
		lines = append(lines, strings.Split(wrapped, "\n")...)
		if b.ToolCallCount > 0 && b.LastToolCall != "" {
			s := dimStyle.Width(w).Render(fmt.Sprintf("  %s (%d tool calls, last: %s)", b.SpinnerView, b.ToolCallCount, b.LastToolCall))
			lines = append(lines, strings.Split(s, "\n")...)
		} else {
			lines = append(lines, dimStyle.Render("\u258a"))
		}
	} else if b.Status != "" {
		s := dimStyle.Width(w).Render(b.SpinnerView + " " + b.Status)
		lines = append(lines, strings.Split(s, "\n")...)
	} else {
		if b.ToolCallCount > 0 && b.LastToolCall != "" {
			s := dimStyle.Width(w).Render(fmt.Sprintf("%s Thinking... (%d tool calls, last: %s)", b.SpinnerView, b.ToolCallCount, b.LastToolCall))
			lines = append(lines, strings.Split(s, "\n")...)
		} else {
			lines = append(lines, dimStyle.Render(b.SpinnerView+" Thinking..."))
		}
	}
	return strings.Join(lines, "\n")
}

// StatusBlock renders an action status line.
type StatusBlock struct {
	Status      string
	Done        bool
	SpinnerView string
}

func (b *StatusBlock) Render(_ int) string {
	if b.Done {
		return "\n  " + b.Status
	}
	return fmt.Sprintf("\n  %s %s", b.SpinnerView, b.Status)
}
