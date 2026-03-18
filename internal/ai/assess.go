package ai

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

type FactorScore struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type Assessment struct {
	BlastRadius  FactorScore `json:"blast_radius"`
	TestCoverage FactorScore `json:"test_coverage"`
	Sensitivity  FactorScore `json:"sensitivity"`
	Complexity   FactorScore `json:"complexity"`
	ScopeFocus   FactorScore `json:"scope_focus"`
	RiskSummary  string      `json:"risk_summary"`
	ReviewNotes  string      `json:"review_notes"`
}

const systemPrompt = `You are a senior code reviewer performing risk triage on pull requests.
Your job is to assess the RISK of each PR — not summarize it.

Risk = blast radius in the codebase, not diff size.
A 1-line change to a critical dependency or core business logic is far riskier than a 500-line change to a dev tool or test file.

You have tools to explore the codebase. Use them to:
- Read the full source of modified files (not just diff hunks)
- Check callers/usages of changed functions or classes
- Verify test coverage for modified code paths
- Examine related config, migration, or infrastructure files

Be efficient — only explore when the diff raises questions you can't answer from context alone.

Score each factor from 1 (lowest risk) to 5 (highest risk):

1. blast_radius: How many parts of the system could break?
2. test_coverage: How well are these changes covered by tests? Score 5 if critical paths lack tests.
3. sensitivity: Are security, auth, payments, data models, or migrations touched?
4. complexity: How subtle are the changes? Could they have non-obvious side effects?
5. scope_focus: Is this PR focused on one concern, or does it touch many unrelated areas?

Important:
- Deleting large amounts of business logic is HIGH blast_radius (4-5), not low
- Failing CI checks should significantly increase test_coverage score
- Review comments from other developers highlight areas of concern

Respond with ONLY a JSON object in this exact format:
{
  "blast_radius": {"score": <1-5>, "reason": "<brief reason>"},
  "test_coverage": {"score": <1-5>, "reason": "<brief reason>"},
  "sensitivity": {"score": <1-5>, "reason": "<brief reason>"},
  "complexity": {"score": <1-5>, "reason": "<brief reason>"},
  "scope_focus": {"score": <1-5>, "reason": "<brief reason>"},
  "risk_summary": "<one sentence, max 80 chars>",
  "review_notes": "- <what the PR does>\n- <key risk 1>\n- <key risk 2>"
}`

func AssessPR(pr *github.PR, repoDir string) (*Assessment, error) { //nolint:unparam
	logger.Info("assessing PR #%d: %s", pr.Number, pr.Title)
	prompt := buildPrompt(pr)

	cmd := exec.Command("claude",
		"-p", prompt,
		"--output-format", "json",
		"--allowedTools", "Read,Bash,Glob",
	)
	cmd.Dir = repoDir

	out, err := cmd.Output()
	if err != nil {
		// capture stderr for better error messages
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
	logger.Info("PR #%d scored: %.1f", pr.Number, float64(assessment.BlastRadius.Score))
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

func buildPrompt(pr *github.PR) string {
	var sb strings.Builder

	sb.WriteString(systemPrompt)
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

	fmt.Fprintf(&sb, "\n## Diff\n```\n%s\n```\n", pr.Diff)

	sb.WriteString("\nUse the tools to explore the codebase if needed. Then respond with the JSON assessment.")

	return sb.String()
}
