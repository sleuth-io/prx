package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sleuth-io/prx/internal/tui/style"
)

var (
	chatUserPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	chatDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// Message is a single message in a PR chat conversation.
type Message struct {
	Role    string // "user" or "assistant"
	Content string
}

// StreamState captures the streaming state for rendering messages inline.
type StreamState struct {
	Active        bool
	Content       string
	SpinnerView   string
	ToolCallCount int
	LastToolCall  string
	Status        string
}

// RenderMessages renders chat messages as a string for embedding in a scrollback.
func RenderMessages(messages []Message, width int, stream StreamState) string {
	var lines []string
	w := width - 4

	for _, msg := range messages {
		if msg.Role == "user" {
			lines = append(lines, "")
			wrapped := lipgloss.NewStyle().Width(w - 2).Render(msg.Content)
			bg := lipgloss.NewStyle().Background(lipgloss.Color("236")).Width(w)
			for i, line := range strings.Split(wrapped, "\n") {
				if i == 0 {
					lines = append(lines, bg.Render(chatUserPrompt.Render("❯ ")+line))
				} else {
					lines = append(lines, bg.Render("  "+line))
				}
			}
		} else {
			lines = append(lines, "")
			rendered := style.RenderMarkdown(msg.Content, w)
			lines = append(lines, strings.Split(rendered, "\n")...)
		}
	}

	if stream.Active && stream.Content != "" {
		lines = append(lines, "")
		wrapped := style.RenderMarkdown(stream.Content, w)
		lines = append(lines, strings.Split(wrapped, "\n")...)
		if stream.ToolCallCount > 0 && stream.LastToolCall != "" {
			s := chatDimStyle.Width(w).Render(fmt.Sprintf("  %s (%d tool calls, last: %s)", stream.SpinnerView, stream.ToolCallCount, stream.LastToolCall))
			lines = append(lines, strings.Split(s, "\n")...)
		} else {
			lines = append(lines, chatDimStyle.Render("\u258a"))
		}
	} else if stream.Active {
		lines = append(lines, "")
		if stream.ToolCallCount > 0 && stream.LastToolCall != "" {
			s := chatDimStyle.Width(w).Render(fmt.Sprintf("%s Thinking... (%d tool calls, last: %s)", stream.SpinnerView, stream.ToolCallCount, stream.LastToolCall))
			lines = append(lines, strings.Split(s, "\n")...)
		} else {
			lines = append(lines, chatDimStyle.Render(stream.SpinnerView+" Thinking..."))
		}
	} else if stream.Status != "" {
		lines = append(lines, "")
		s := chatDimStyle.Width(w).Render(stream.SpinnerView + " " + stream.Status)
		lines = append(lines, strings.Split(s, "\n")...)
	}

	return strings.Join(lines, "\n")
}
