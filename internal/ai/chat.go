package ai

import (
	"fmt"
	"strings"

	"github.com/sleuth-io/prx/internal/github"
)

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

// DiffContext is the file/line the reviewer was looking at when they opened chat.
type DiffContext struct {
	File string
	Line int
}

// BuildChatPrompt constructs the prompt for an interactive chat session about a PR.
func BuildChatPrompt(pr *github.PR, assessment *Assessment, history []ChatMessage, ctx *DiffContext) string {
	var sb strings.Builder

	sb.WriteString(`You are helping a code reviewer understand a pull request. You have tools to explore the codebase. Answer the reviewer's question concisely.

`)

	// Diff context: what the reviewer was looking at when they started chatting
	if ctx != nil && ctx.File != "" {
		fmt.Fprintf(&sb, "## Reviewer's Current Position\n")
		if ctx.Line > 0 {
			fmt.Fprintf(&sb, "The reviewer is currently looking at `%s` line %d in the diff. Unless they ask about something else, focus your answers on this area.\n\n", ctx.File, ctx.Line)
		} else {
			fmt.Fprintf(&sb, "The reviewer is currently looking at `%s` in the diff. Unless they ask about something else, focus your answers on this file.\n\n", ctx.File)
		}
	}

	// PR metadata
	fmt.Fprintf(&sb, "## PR #%d: %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&sb, "Author: %s\n", pr.Author)
	fmt.Fprintf(&sb, "Files changed: %d | +%d/-%d\n", pr.FilesChanged, pr.Additions, pr.Deletions)

	if pr.Body != "" {
		body := pr.Body
		if len(body) > 2000 {
			body = body[:2000] + "..."
		}
		fmt.Fprintf(&sb, "\n## PR Description\n%s\n", body)
	}

	// Assessment context
	if assessment != nil {
		sb.WriteString("\n## AI Risk Assessment\n")
		if assessment.RiskSummary != "" {
			fmt.Fprintf(&sb, "Summary: %s\n", assessment.RiskSummary)
		}
		if assessment.ReviewNotes != "" {
			fmt.Fprintf(&sb, "Notes:\n%s\n", assessment.ReviewNotes)
		}
		for name, f := range assessment.Factors {
			fmt.Fprintf(&sb, "- %s: %d/5 — %s\n", name, f.Score, f.Reason)
		}
	}

	// Review comments
	if len(pr.Reviews) > 0 {
		sb.WriteString("\n## Review Submissions\n")
		for _, r := range pr.Reviews {
			body := r.Body
			if len(body) > 300 {
				body = body[:300] + "..."
			}
			fmt.Fprintf(&sb, "- **%s** (%s): %s\n", r.Author, r.State, body)
		}
	}

	if len(pr.InlineComments) > 0 {
		sb.WriteString("\n## Inline Comments\n")
		for _, c := range pr.InlineComments {
			body := c.Body
			if len(body) > 300 {
				body = body[:300] + "..."
			}
			fmt.Fprintf(&sb, "- **%s** on `%s`: %s\n", c.Author, c.Path, body)
		}
	}

	// Diff (truncated)
	diff := pr.Diff
	if len(diff) > 30000 {
		diff = diff[:30000] + "\n... [diff truncated]"
	}
	fmt.Fprintf(&sb, "\n## Diff\n```\n%s\n```\n", diff)

	// Conversation history
	if len(history) > 0 {
		sb.WriteString("\n## Conversation\n")
		for _, msg := range history {
			if msg.Role == "user" {
				fmt.Fprintf(&sb, "\nReviewer: %s\n", msg.Content)
			} else {
				fmt.Fprintf(&sb, "\nAssistant: %s\n", msg.Content)
			}
		}
	}

	// Final instruction
	sb.WriteString("\nAnswer the reviewer's latest question concisely. Use tools to explore the codebase if needed.")

	return sb.String()
}
