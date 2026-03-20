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

// SkillCatalog holds the name and description of a discovered skill for prompt injection.
type SkillCatalog struct {
	Name        string
	Description string
}

// BuildChatPrompt constructs the prompt for an interactive chat session about a PR.
// availableActions lists MCP tool names (e.g. "mcp__prx__approve_pr") that Claude may call.
// skillCatalog lists skills available via the activate_skill tool.
func BuildChatPrompt(pr *github.PR, assessment *Assessment, history []ChatMessage, ctx *DiffContext, availableActions []string, skillCatalog []SkillCatalog) string {
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

	if len(pr.Comments) > 0 {
		sb.WriteString("\n## PR Comments\n")
		for _, c := range pr.Comments {
			body := c.Body
			if len(body) > 300 {
				body = body[:300] + "..."
			}
			fmt.Fprintf(&sb, "- **%s**: %s\n", c.Author, body)
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

	// Configuration tools (always available)
	sb.WriteString("\n## Configuration Tools\n")
	sb.WriteString("You can inspect and modify how PRs are reviewed:\n")
	sb.WriteString("- get_config: show current model, criteria, and thresholds\n")
	sb.WriteString("- set_model: change the Claude model used for scoring\n")
	sb.WriteString("- set_criterion: add or update a scoring criterion (name, label, description, weight)\n")
	sb.WriteString("- remove_criterion: remove a criterion by name\n")
	sb.WriteString("- set_thresholds: update approve_below and review_above thresholds\n")
	sb.WriteString("Only modify config when explicitly asked. Write actions require user confirmation.\n")

	// Available PR actions
	if len(availableActions) > 0 {
		sb.WriteString("\n## Available PR Actions\n")
		sb.WriteString("You can take the following actions if the reviewer explicitly asks:\n")
		for _, a := range availableActions {
			switch a {
			case "mcp__prx__comment_on_pr":
				sb.WriteString("- comment_on_pr: post a comment on the PR\n")
			case "mcp__prx__approve_pr":
				sb.WriteString("- approve_pr: approve the PR\n")
			case "mcp__prx__request_changes":
				sb.WriteString("- request_changes: request changes on the PR\n")
			case "mcp__prx__merge_pr":
				sb.WriteString("- merge_pr: merge the PR\n")
			}
		}
		sb.WriteString("Only use these when explicitly asked. The user will be prompted to confirm before execution.\n")
	}

	// Skills catalog
	if len(skillCatalog) > 0 {
		sb.WriteString("\n## Available Skills\n")
		sb.WriteString("The following skills provide specialized instructions for specific tasks.\n")
		sb.WriteString("When a task matches a skill's description, call the activate_skill tool\n")
		sb.WriteString("with the skill's name to load its full instructions.\n")
		sb.WriteString("Skills are also available as slash commands: the user can type /skill-name to activate.\n\n")
		for _, s := range skillCatalog {
			fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Description)
		}
	}

	// Final instruction
	sb.WriteString("\nAnswer the reviewer's latest question concisely. Use tools to explore the codebase if needed.")

	return sb.String()
}
