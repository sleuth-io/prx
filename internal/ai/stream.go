package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sleuth-io/prx/internal/logger"
)

// StreamCallbacks receives events from a streaming claude subprocess.
// All callbacks are optional — nil callbacks are silently skipped.
type StreamCallbacks struct {
	OnStatus   func(status string)
	OnToolCall func(count int, toolSummary string)
	OnToken    func(delta string)
}

// RunClaude runs the claude CLI with --output-format stream-json and dispatches
// parsed events to the provided callbacks. Returns the full text response on success.
func RunClaude(ctx context.Context, args []string, dir string, callbacks StreamCallbacks) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude: %w", err)
	}

	var fullResponse strings.Builder
	prevLen := 0
	toolCallCount := 0
	sentInit := false
	hadToolCalls := false
	sentWriting := false

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			logger.Debug("stream parse: %v", err)
			continue
		}
		var eventType string
		if raw, ok := event["type"]; ok {
			_ = json.Unmarshal(raw, &eventType)
		}
		logger.Debug("stream event: %s", eventType)

		switch eventType {
		case "system":
			if !sentInit && callbacks.OnStatus != nil {
				sentInit = true
				callbacks.OnStatus("Thinking...")
			}
		case "assistant":
			var msg struct {
				Message struct {
					Content []struct {
						Type  string         `json:"type"`
						Text  string         `json:"text"`
						Name  string         `json:"name"`
						Input map[string]any `json:"input"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			var text string
			for _, block := range msg.Message.Content {
				if block.Type == "text" {
					text += block.Text
				} else if block.Type == "tool_use" && block.Name != "" {
					toolCallCount++
					hadToolCalls = true
					summary := ToolSummary(block.Name, block.Input)
					if callbacks.OnToolCall != nil {
						callbacks.OnToolCall(toolCallCount, summary)
					}
				}
			}
			if text != "" {
				if !sentWriting && hadToolCalls && callbacks.OnStatus != nil {
					sentWriting = true
					callbacks.OnStatus("Writing assessment...")
				} else if !sentWriting && !hadToolCalls && callbacks.OnStatus != nil {
					sentWriting = true
					callbacks.OnStatus("Analyzing...")
				}
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
				_ = cmd.Wait()
				return "", fmt.Errorf("claude: %s", result.Result)
			}
			if result.Result != "" && fullResponse.Len() == 0 {
				fullResponse.WriteString(result.Result)
				if callbacks.OnToken != nil {
					callbacks.OnToken(result.Result)
				}
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.Error("stream scanner error: %v", scanErr)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fullResponse.String(), nil
		}
		logger.Error("claude exit: %v\nstderr: %s", err, stderrBuf.String())
		if fullResponse.Len() == 0 {
			errMsg := stderrBuf.String()
			if errMsg == "" {
				errMsg = err.Error()
			}
			return "", fmt.Errorf("claude failed: %s", errMsg)
		}
	}

	return fullResponse.String(), nil
}

// ToolSummary returns a short display string like "Read foo.py" or "Grep pattern".
func ToolSummary(name string, input map[string]any) string {
	// Translate mcp__prx__* tool names to friendly labels.
	if strings.HasPrefix(name, "mcp__prx__") {
		short := strings.TrimPrefix(name, "mcp__prx__")
		switch short {
		case "activate_skill":
			if n, ok := input["name"].(string); ok {
				return "Loading skill: " + n
			}
			return "Loading skill"
		case "read_skill_resource":
			if p, ok := input["path"].(string); ok {
				return "Reading: " + p
			}
			return "Reading skill resource"
		case "get_config":
			return "Reading config"
		case "set_model":
			if m, ok := input["model"].(string); ok {
				return "Setting model → " + m
			}
			return "Setting model"
		case "set_criterion":
			if n, ok := input["name"].(string); ok {
				return "Updating criterion: " + n
			}
			return "Updating criterion"
		case "remove_criterion":
			if n, ok := input["name"].(string); ok {
				return "Removing criterion: " + n
			}
			return "Removing criterion"
		case "set_thresholds":
			return "Updating thresholds"
		case "approve_pr":
			return "Approving PR"
		case "request_changes":
			return "Requesting changes"
		case "comment_on_pr":
			return "Posting comment"
		case "merge_pr":
			return "Merging PR"
		default:
			return strings.ReplaceAll(short, "_", " ")
		}
	}

	// ToolSearch is Claude's internal tool for loading deferred tool schemas.
	// Show a friendly label instead of the raw query (e.g. "select:mcp__prx__activate_skill").
	if name == "ToolSearch" {
		if q, ok := input["query"].(string); ok && strings.Contains(q, "activate_skill") {
			return "Loading skill tools"
		}
		return "Loading tools"
	}

	if len(input) == 0 {
		return name
	}
	// Pick the most informative arg for common tools.
	for _, key := range []string{"file_path", "pattern", "command", "query", "path"} {
		if v, ok := input[key]; ok {
			s := fmt.Sprintf("%v", v)
			// Use basename for file paths.
			if key == "file_path" {
				if i := strings.LastIndex(s, "/"); i >= 0 {
					s = s[i+1:]
				}
			}
			// Truncate long values.
			if len(s) > 40 {
				s = s[:37] + "..."
			}
			return name + " " + s
		}
	}
	return name
}
