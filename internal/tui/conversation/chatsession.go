package conversation

import (
	"fmt"
	"time"

	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/tui/chat"
)

// ChatSession owns all per-PR chat state. It replaces the 7 scattered
// chat fields that were previously on PRCard.
type ChatSession struct {
	Messages      []chat.Message
	WorktreePath  string // git worktree path for chat (empty until created)
	Cancel        func() // cancels the running claude process (nil if not streaming)
	Streaming     bool
	StreamContent string
	ToolCallCount int
	LastToolCall  string
	Status        string          // e.g. "Creating worktree...", "Starting Claude..."
	StreamStart   time.Time       // when streaming started (for elapsed time display)
	Warm          *ai.WarmProcess // pre-started Claude process (nil until pre-warmed)
}

// IsStreaming returns true if a chat response is being streamed.
func (s *ChatSession) IsStreaming() bool {
	return s.Streaming
}

// StartMessage adds a user message and marks the session as streaming.
func (s *ChatSession) StartMessage(body string) {
	s.Messages = append(s.Messages, chat.Message{Role: "user", Content: body})
	s.Streaming = true
	s.StreamStart = time.Now()
	s.Status = "Starting Claude..."
}

// HandleToken accumulates a streamed token.
func (s *ChatSession) HandleToken(token string) {
	s.Status = ""
	s.StreamContent += token
}

// HandleToolCall updates tool call tracking.
func (s *ChatSession) HandleToolCall(count int, lastTool string) {
	s.ToolCallCount = count
	s.LastToolCall = lastTool
}

// HandleStatus updates the status message.
func (s *ChatSession) HandleStatus(status string) {
	s.Status = status
}

// HandleDone finalizes a chat response, appending the assistant message.
func (s *ChatSession) HandleDone(fullResponse string, err error) {
	s.Cancel = nil
	s.Streaming = false
	s.StreamContent = ""
	s.Status = ""
	s.ToolCallCount = 0
	s.LastToolCall = ""
	if err != nil {
		s.Messages = append(s.Messages, chat.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("Error: %v", err),
		})
	} else if fullResponse != "" {
		s.Messages = append(s.Messages, chat.Message{
			Role:    "assistant",
			Content: fullResponse,
		})
	}
}

// HandleWorktreeReady stores the worktree path or records an error message on failure.
func (s *ChatSession) HandleWorktreeReady(path string, err error) {
	if err != nil {
		s.HandleDone("", fmt.Errorf("failed to create worktree: %w", err))
	} else {
		s.WorktreePath = path
		if s.Status == "Preparing chat..." {
			s.Status = ""
		}
	}
}

// NeedsWorktree returns true if no worktree has been created yet.
func (s *ChatSession) NeedsWorktree() bool {
	return s.WorktreePath == ""
}

// HasWarm returns true if a warm process exists (may or may not be ready).
func (s *ChatSession) HasWarm() bool {
	return s.Warm != nil
}

// TakeWarm takes ownership of the warm process, returning it and clearing it
// from the session. Returns nil if no warm process exists.
func (s *ChatSession) TakeWarm() *ai.WarmProcess {
	wp := s.Warm
	s.Warm = nil
	return wp
}

// CancelStreaming cancels an in-flight chat request if one is active.
func (s *ChatSession) CancelStreaming() {
	if s.Cancel != nil {
		s.Cancel()
		s.Cancel = nil
	}
}

// Cleanup kills any warm process and cancels streaming.
func (s *ChatSession) Cleanup() {
	s.CancelStreaming()
	if s.Warm != nil {
		s.Warm.Kill()
		s.Warm = nil
	}
}
