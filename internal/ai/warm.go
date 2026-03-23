package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/sleuth-io/prx/internal/logger"
)

// WarmProcess is a pre-started Claude CLI process that has completed its
// initialization (hooks, MCP server connections) but hasn't received a prompt.
// When a prompt is needed, call Send() to feed it via stream-json stdin.
type WarmProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	eventCh  chan string // buffered channel of raw stdout lines
	stderr   strings.Builder
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{} // closed when hooks complete
	initErr  error
	OnStatus func(status string) // optional callback for status updates during warm-up
}

// StartWarm starts a Claude CLI process that initializes hooks and MCP servers
// but does not send a prompt. The process blocks waiting for input on stdin.
// Call Send() to submit a prompt and stream the response.
func StartWarm(ctx context.Context, args []string, dir string) *WarmProcess {
	ctx, cancel := context.WithCancel(ctx)

	// Use --print with stream-json I/O so the process starts,
	// runs hooks/MCP init, then waits for a message on stdin.
	warmArgs := make([]string, 0, len(args)+4)
	warmArgs = append(warmArgs, "--print", "--input-format", "stream-json", "--output-format", "stream-json")
	warmArgs = append(warmArgs, args...)

	cmd := exec.CommandContext(ctx, "claude", warmArgs...)
	cmd.Dir = dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		wp := &WarmProcess{ready: make(chan struct{}), initErr: fmt.Errorf("stdin pipe: %w", err)}
		close(wp.ready)
		return wp
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		wp := &WarmProcess{ready: make(chan struct{}), initErr: fmt.Errorf("stdout pipe: %w", err)}
		close(wp.ready)
		return wp
	}

	wp := &WarmProcess{
		cmd:     cmd,
		stdin:   stdin,
		eventCh: make(chan string, 100),
		ctx:     ctx,
		cancel:  cancel,
		ready:   make(chan struct{}),
	}
	cmd.Stderr = &wp.stderr

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	if err := cmd.Start(); err != nil {
		wp.initErr = fmt.Errorf("start claude: %w", err)
		close(wp.ready)
		cancel()
		return wp
	}
	logger.Info("warm process started (pid=%d)", cmd.Process.Pid)

	// Scanner goroutine: reads all stdout lines into eventCh.
	go func() {
		for scanner.Scan() {
			wp.eventCh <- scanner.Text()
		}
		close(wp.eventCh)
	}()

	// Readiness goroutine: waits for SessionStart hooks to finish.
	go func() {
		defer close(wp.ready)
		sawHookStarted := false

		for {
			select {
			case line, ok := <-wp.eventCh:
				if !ok {
					if wp.ctx.Err() == nil && sawHookStarted {
						wp.initErr = fmt.Errorf("claude exited before hooks completed")
					}
					return
				}
				if line == "" {
					continue
				}
				var event struct {
					Type    string `json:"type"`
					Subtype string `json:"subtype"`
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					continue
				}

				if event.Type == "system" && event.Subtype == "hook_started" {
					sawHookStarted = true
					if wp.OnStatus != nil {
						wp.OnStatus("Running startup hooks...")
					}
				}
				if event.Type == "system" && event.Subtype == "hook_response" {
					logger.Info("warm process hooks done")
					if wp.OnStatus != nil {
						wp.OnStatus("")
					}
					return
				}
				if event.Type == "system" && event.Subtype == "init" {
					if wp.OnStatus != nil {
						wp.OnStatus("")
					}
					return
				}

			case <-wp.ctx.Done():
				return
			}
		}
	}()

	return wp
}

// Ready returns a channel that is closed when the process has completed init.
func (wp *WarmProcess) Ready() <-chan struct{} {
	return wp.ready
}

// IsReady returns true if init is complete (non-blocking).
func (wp *WarmProcess) IsReady() bool {
	select {
	case <-wp.ready:
		return true
	default:
		return false
	}
}

// InitErr returns any error that occurred during initialization.
func (wp *WarmProcess) InitErr() error {
	return wp.initErr
}

// Send submits a prompt to the warm process and streams the response.
// Returns when a "result" event is received (end of one turn).
// The process stays alive and can receive further messages.
func (wp *WarmProcess) Send(prompt string, callbacks StreamCallbacks) (string, error) {
	// Wait for hooks to complete.
	select {
	case <-wp.ready:
	case <-wp.ctx.Done():
		return "", wp.ctx.Err()
	}
	if wp.initErr != nil {
		return "", wp.initErr
	}

	// Write the user message as stream-json input.
	msg := map[string]any{
		"type": "user",
		"message": map[string]string{
			"role":    "user",
			"content": prompt,
		},
		"session_id":         "default",
		"parent_tool_use_id": nil,
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}
	if _, err := wp.stdin.Write(append(msgBytes, '\n')); err != nil {
		return "", fmt.Errorf("write to stdin: %w", err)
	}

	if callbacks.OnStatus != nil {
		callbacks.OnStatus("Thinking...")
	}

	// Read response events until a "result" event marks end of turn.
	var fullResponse strings.Builder
	prevLen := 0
	toolCallCount := 0

	for line := range wp.eventCh {
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			logger.Debug("warm stream parse: %v", err)
			continue
		}
		var eventType string
		if raw, ok := event["type"]; ok {
			_ = json.Unmarshal(raw, &eventType)
		}

		switch eventType {
		case "system":
			continue
		case "assistant":
			var assistantMsg struct {
				Message struct {
					Content []struct {
						Type  string         `json:"type"`
						Text  string         `json:"text"`
						Name  string         `json:"name"`
						Input map[string]any `json:"input"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &assistantMsg); err != nil {
				continue
			}
			var text string
			for _, block := range assistantMsg.Message.Content {
				if block.Type == "text" {
					text += block.Text
				} else if block.Type == "tool_use" && block.Name != "" {
					toolCallCount++
					summary := ToolSummary(block.Name, block.Input)
					if callbacks.OnToolCall != nil {
						callbacks.OnToolCall(toolCallCount, summary)
					}
				}
			}
			if text != "" {
				fullResponse.Reset()
				fullResponse.WriteString(text)
				if len(text) > prevLen {
					delta := text[prevLen:]
					prevLen = len(text)
					if callbacks.OnToken != nil {
						callbacks.OnToken(delta)
					}
				}
			}
		case "result":
			var result struct {
				Result  string `json:"result"`
				IsError bool   `json:"is_error"`
			}
			_ = json.Unmarshal([]byte(line), &result)
			if result.IsError {
				return "", fmt.Errorf("claude: %s", result.Result)
			}
			if result.Result != "" && fullResponse.Len() == 0 {
				fullResponse.WriteString(result.Result)
				if callbacks.OnToken != nil {
					callbacks.OnToken(result.Result)
				}
			}
			// Turn complete — return the response.
			return fullResponse.String(), nil
		}
	}

	// eventCh closed means process exited unexpectedly.
	if wp.ctx.Err() != nil {
		return fullResponse.String(), nil
	}
	return "", fmt.Errorf("claude process exited unexpectedly")
}

// Kill terminates the warm process if it's still running.
func (wp *WarmProcess) Kill() {
	wp.cancel()
	if wp.cmd != nil && wp.cmd.Process != nil {
		_ = wp.cmd.Process.Kill()
		_ = wp.cmd.Wait()
	}
}
