package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

type FactorScore struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type HunkAnnotation struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	Trivial   bool   `json:"trivial"`
	Reason    string `json:"reason"`
}

type KeyHunk struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	Reason    string `json:"reason"`
}

type ReviewGuide struct {
	Summary string `json:"summary"`
	Risk    string `json:"risk"`
	Focus   string `json:"focus"`
}

type Assessment struct {
	Factors         map[string]FactorScore `json:"factors"`
	RiskSummary     string                 `json:"risk_summary"`
	ReviewNotes     string                 `json:"review_notes"`
	Guide           *ReviewGuide           `json:"review_guide,omitempty"`
	HunkAnnotations []HunkAnnotation       `json:"hunk_annotations,omitempty"`
	KeyHunk         *KeyHunk               `json:"key_hunk,omitempty"`
	DiffTruncated   bool                   `json:"-"` // set by caller, not from AI
	RenderedNotes   string                 `json:"-"` // cached markdown render
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
  "review_guide": {
    "summary": "<what this PR does and why, one sentence>",
    "risk": "<single biggest risk in behavior/business terms>",
    "focus": "<the one area a reviewer should look at and why>"
  },
  "hunk_annotations": [
    {"file": "<path>", "start_line": <new-file line number from hunk header>, "trivial": <true|false>, "reason": "<why trivial or not, max 40 chars>"}
  ],
  "key_hunk": {"file": "<path>", "start_line": <new-file line number>, "reason": "<why this is the most important change, max 60 chars>"}
}

For hunk_annotations: examine each hunk (@@-delimited section) in the diff. For each hunk,
taking all the above criteria into account, determine whether a senior reviewer needs to read
it to make their approval decision. A hunk is trivial if it scores low across all criteria —
e.g. import changes, mechanical renames, boilerplate, auto-generated code. Include ALL hunks.
The start_line is the new-file line number from the hunk header (the + number in @@ -x,y +Z,w @@).

For key_hunk: identify the single most important hunk — the one a reviewer should look at first.
Pick the hunk with the highest risk or the most significant behavioral change. Use the same
file and start_line format as hunk_annotations.

For review_guide: write in plain language a non-technical manager could understand.
- summary: one sentence describing what this PR does and why, at a business/feature level.
- risk: the single biggest risk in behavior or business terms (not code-level details).
- focus: the one area a reviewer should pay attention to and why.
Do NOT use variable names, file paths, function names, or code references. Think "what would
I tell a skip-level manager who asked what this PR is about?"`)

	return sb.String()
}

func AssessPR(ctx context.Context, pr *github.PR, repoDir string, criteria []config.Criterion, model string, callbacks StreamCallbacks) (*Assessment, error) {
	logger.Info("assessing PR #%d: %s", pr.Number, pr.Title)
	prompt := buildPrompt(pr, criteria)

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", "Read,Bash,Glob",
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	result, err := RunClaude(ctx, args, repoDir, callbacks)
	if err != nil {
		return nil, fmt.Errorf("claude assessment failed: %w", err)
	}

	logger.Debug("claude raw output for PR #%d: %s", pr.Number, result)

	jsonStr := extractJSON(result)
	logger.Debug("extracted JSON for PR #%d: %s", pr.Number, jsonStr)

	var assessment Assessment
	if err := json.Unmarshal([]byte(jsonStr), &assessment); err != nil {
		logger.Error("parsing assessment JSON for PR #%d: %v\nraw: %s", pr.Number, err, jsonStr)
		return nil, fmt.Errorf("parsing assessment JSON: %w\nraw: %s", err, jsonStr)
	}
	assessment.DiffTruncated = len(pr.Diff) > 30000
	logger.Info("PR #%d assessed with %d factors (truncated=%v)", pr.Number, len(assessment.Factors), assessment.DiffTruncated)
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

	writeDiffSection(&sb, pr.Diff, 30000)

	sb.WriteString("\nUse the tools to explore the codebase if needed — especially for files not included in the diff above. Then respond with the JSON assessment.")

	return sb.String()
}

// writeDiffSection writes the diff to the prompt, splitting large diffs into
// a file summary + as many full file diffs as fit within the budget.
func writeDiffSection(sb *strings.Builder, rawDiff string, budget int) {
	if len(rawDiff) <= budget {
		fmt.Fprintf(sb, "\n## Diff\n```\n%s\n```\n", rawDiff)
		return
	}

	// Parse diff into per-file chunks
	type fileDiff struct {
		name    string
		content string
	}
	var files []fileDiff
	lines := strings.Split(rawDiff, "\n")
	var current strings.Builder
	var currentName string

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			if currentName != "" {
				files = append(files, fileDiff{name: currentName, content: current.String()})
			}
			current.Reset()
			// Extract filename from "diff --git a/path b/path"
			parts := strings.SplitN(line, " b/", 2)
			if len(parts) == 2 {
				currentName = parts[1]
			} else {
				currentName = line
			}
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}
	if currentName != "" {
		files = append(files, fileDiff{name: currentName, content: current.String()})
	}

	// Build file summary with hunk line counts
	sb.WriteString("\n## Files Changed (summary)\n")
	for _, f := range files {
		adds, dels := countLines(f.content)
		fmt.Fprintf(sb, "- `%s` (+%d/-%d)\n", f.name, adds, dels)
	}

	// Include as many full file diffs as fit in the budget
	sb.WriteString("\n## Diff (partial — use Read tool to inspect omitted files)\n```\n")
	used := 0
	included := 0
	for _, f := range files {
		if used+len(f.content) > budget {
			continue
		}
		sb.WriteString(f.content)
		used += len(f.content)
		included++
	}
	sb.WriteString("```\n")
	if included < len(files) {
		fmt.Fprintf(sb, "\n*%d of %d files shown. Use the Read tool to examine the remaining files.*\n", included, len(files))
	}
}

func countLines(diffChunk string) (adds, dels int) {
	for _, line := range strings.Split(diffChunk, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			adds++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			dels++
		}
	}
	return
}
