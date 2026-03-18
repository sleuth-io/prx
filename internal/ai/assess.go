package ai

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

type FactorScore struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type Assessment struct {
	Factors     map[string]FactorScore `json:"factors"`
	RiskSummary string                 `json:"risk_summary"`
	ReviewNotes string                 `json:"review_notes"`
}

func buildSystemPrompt(criteria []config.Criterion) string {
	var sb strings.Builder

	sb.WriteString(`You are a senior engineer helping a human reviewer prioritize their attention.
Your job is NOT to review the code — it is to assess how much HUMAN JUDGMENT this PR requires.

Some PRs can be confidently evaluated by automated tools alone. Others fundamentally require
a human to say "yes, this is the direction we want to go." Score for the latter.

You have tools to explore the codebase. Use them to:
- Read the full source of modified files (not just diff hunks)
- Check callers/usages of changed functions or classes
- Examine related config, migration, or infrastructure files

Be efficient — only explore when the diff raises questions you can't answer from context alone.

Score each factor from 1 (lowest — machine can handle) to 5 (highest — needs experienced human):

`)

	for i, c := range criteria {
		fmt.Fprintf(&sb, "%d. %s: %s\n", i+1, c.Name, c.Description)
	}

	sb.WriteString(`
Important:
- Failing CI checks should increase scores for affected factors
- Review comments from other developers highlight areas needing human attention
- A PR with no description or vague intent inherently needs more human judgment

Respond with ONLY a JSON object in this exact format:
{
  "factors": {
`)

	for i, c := range criteria {
		comma := ","
		if i == len(criteria)-1 {
			comma = ""
		}
		fmt.Fprintf(&sb, `    "%s": {"score": <1-5>, "reason": "<brief reason>"}%s
`, c.Name, comma)
	}

	sb.WriteString(`  },
  "risk_summary": "<one sentence, max 80 chars>",
  "review_notes": "- <what the PR does>\n- <key concern 1>\n- <key concern 2>"
}`)

	return sb.String()
}

func AssessPR(pr *github.PR, repoDir string, criteria []config.Criterion) (*Assessment, error) {
	logger.Info("assessing PR #%d: %s", pr.Number, pr.Title)
	prompt := buildPrompt(pr, criteria)

	cmd := exec.Command("claude",
		"-p", prompt,
		"--output-format", "json",
		"--allowedTools", "Read,Bash,Glob",
	)
	cmd.Dir = repoDir

	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		logger.Error("claude failed for PR #%d: %v\nstderr: %s", pr.Number, err, stderr)
		return nil, fmt.Errorf("claude assessment failed: %w\n%s", err, stderr)
	}

	logger.Debug("claude raw output for PR #%d: %s", pr.Number, string(out))

	var envelope struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		logger.Error("parsing claude envelope for PR #%d: %v\nraw: %s", pr.Number, err, string(out))
		return nil, fmt.Errorf("parsing claude output: %w", err)
	}
	if envelope.IsError {
		logger.Error("claude error for PR #%d: %s", pr.Number, envelope.Result)
		return nil, fmt.Errorf("claude returned error: %s", envelope.Result)
	}

	jsonStr := extractJSON(envelope.Result)
	logger.Debug("extracted JSON for PR #%d: %s", pr.Number, jsonStr)

	var assessment Assessment
	if err := json.Unmarshal([]byte(jsonStr), &assessment); err != nil {
		logger.Error("parsing assessment JSON for PR #%d: %v\nraw: %s", pr.Number, err, jsonStr)
		return nil, fmt.Errorf("parsing assessment JSON: %w\nraw: %s", err, jsonStr)
	}
	logger.Info("PR #%d assessed with %d factors", pr.Number, len(assessment.Factors))
	return &assessment, nil
}

func extractJSON(s string) string {
	if idx := strings.Index(s, "```json"); idx >= 0 {
		s = s[idx+7:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	} else if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx+3:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end >= start {
		return s[start : end+1]
	}
	return s
}

func buildPrompt(pr *github.PR, criteria []config.Criterion) string {
	var sb strings.Builder

	sb.WriteString(buildSystemPrompt(criteria))
	sb.WriteString("\n\n---\n\n")
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

	fmt.Fprintf(&sb, "\n## CI Checks\n%s\n", pr.ChecksSummary())

	if len(pr.Comments) > 0 {
		sb.WriteString("\n## Conversation Comments\n")
		for _, c := range pr.Comments {
			body := c.Body
			if len(body) > 300 {
				body = body[:300] + "..."
			}
			fmt.Fprintf(&sb, "- **%s**: %s\n", c.Author, body)
		}
	}

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
		sb.WriteString("\n## Inline Review Comments\n")
		for _, c := range pr.InlineComments {
			body := c.Body
			if len(body) > 300 {
				body = body[:300] + "..."
			}
			fmt.Fprintf(&sb, "- **%s** on `%s`: %s\n", c.Author, c.Path, body)
		}
	}

	diff := pr.Diff
	if len(diff) > 30000 {
		diff = diff[:30000] + "\n... [diff truncated]"
	}
	fmt.Fprintf(&sb, "\n## Diff\n```\n%s\n```\n", diff)

	sb.WriteString("\nUse the tools to explore the codebase if needed. Then respond with the JSON assessment.")

	return sb.String()
}
