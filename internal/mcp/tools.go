package mcp

import (
	"fmt"
	"strings"

	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
)

var toolDefs = []map[string]interface{}{
	{
		"name":        "approve_pr",
		"description": "Approve the pull request, optionally with a review comment",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"body": map[string]interface{}{
					"type":        "string",
					"description": "Optional review body included with the approval",
				},
			},
		},
	},
	{
		"name":        "request_changes",
		"description": "Request changes on the pull request",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"body": map[string]interface{}{
					"type":        "string",
					"description": "The review body describing requested changes",
				},
			},
			"required": []string{"body"},
		},
	},
	{
		"name":        "comment_on_pr",
		"description": "Post a comment on the pull request. Optionally post as an inline code comment by providing path and line.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"body": map[string]interface{}{
					"type":        "string",
					"description": "The comment body",
				},
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path for an inline comment (e.g. 'src/foo.go'). Must be combined with line.",
				},
				"line": map[string]interface{}{
					"type":        "integer",
					"description": "Line number in the diff for an inline comment. Must be combined with path.",
				},
			},
			"required": []string{"body"},
		},
	},
	{
		"name":        "skip_pr",
		"description": "Skip the pull request — hides it from the review queue until un-skipped",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		"name":        "merge_pr",
		"description": "Merge the pull request",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		"name":        "get_config",
		"description": "Get the current prx configuration: model, scoring criteria, and thresholds",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		"name":        "set_model",
		"description": "Change the Claude model used for PR scoring",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"model": map[string]interface{}{
					"type":        "string",
					"description": "Model name (e.g. 'sonnet', 'opus', 'haiku')",
				},
			},
			"required": []string{"model"},
		},
	},
	{
		"name":        "set_merge_method",
		"description": "Change the merge method used when merging PRs",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"method": map[string]interface{}{
					"type":        "string",
					"description": "Merge method: 'merge' (merge commit), 'squash' (squash and merge), or 'rebase' (rebase and merge)",
				},
			},
			"required": []string{"method"},
		},
	},
	{
		"name":        "set_criterion",
		"description": "Add or update a scoring criterion",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Unique identifier for the criterion (e.g. 'blast_radius')",
				},
				"label": map[string]interface{}{
					"type":        "string",
					"description": "Short display label (e.g. 'Blast')",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Detailed description used in the AI prompt",
				},
				"weight": map[string]interface{}{
					"type":        "number",
					"description": "Weighting factor (must be > 0)",
				},
			},
			"required": []string{"name", "label", "description", "weight"},
		},
	},
	{
		"name":        "remove_criterion",
		"description": "Remove a scoring criterion by name",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the criterion to remove",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		"name":        "set_thresholds",
		"description": "Update the approve_below and review_above score thresholds",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"approve_below": map[string]interface{}{
					"type":        "number",
					"description": "PRs scoring below this are auto-approved (1.0–5.0)",
				},
				"review_above": map[string]interface{}{
					"type":        "number",
					"description": "PRs scoring above this require careful review (1.0–5.0)",
				},
			},
			"required": []string{"approve_below", "review_above"},
		},
	},
}

// ToolNames returns the MCP-prefixed names of all registered tools.
// Commands can use this to build --allowedTools lists without hardcoding names.
func ToolNames() []string {
	names := make([]string, 0, len(toolMetas))
	for name := range toolMetas {
		names = append(names, "mcp__prx__"+name)
	}
	return names
}

// allToolDefs returns the static tool definitions plus a dynamic activate_skill
// tool whose name parameter is constrained to the set of discovered skill names.
func (s *Server) allToolDefs() []map[string]interface{} {
	defs := make([]map[string]interface{}, len(toolDefs))
	copy(defs, toolDefs)

	if len(s.skills) > 0 {
		names := make([]interface{}, len(s.skills))
		for i, sk := range s.skills {
			names[i] = sk.Name
		}
		defs = append(defs, map[string]interface{}{
			"name":        "activate_skill",
			"description": "Load the full instructions for a skill. Call this when you need specialized guidance for a task that matches a skill's description. The response lists available resource files — use read_skill_resource to load them.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The skill name to activate",
						"enum":        names,
					},
				},
				"required": []string{"name"},
			},
		})
		defs = append(defs, map[string]interface{}{
			"name":        "read_skill_resource",
			"description": "Read a resource file bundled with a skill. Use this to load reference docs, scripts, or other files listed in the skill's resources.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"skill": map[string]interface{}{
						"type":        "string",
						"description": "The skill name",
						"enum":        names,
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Relative path to the resource file (as listed in skill_resources)",
					},
				},
				"required": []string{"skill", "path"},
			},
		})
	}

	return defs
}

func (s *Server) toolDescription(name string, args map[string]interface{}) string {
	switch name {
	case "approve_pr":
		return fmt.Sprintf("Approve PR #%d", s.prNumber)
	case "request_changes":
		body, _ := args["body"].(string)
		return fmt.Sprintf("Request changes on PR #%d: %s", s.prNumber, truncate(body, 100))
	case "comment_on_pr":
		body, _ := args["body"].(string)
		path, _ := args["path"].(string)
		if path != "" {
			line, _ := args["line"].(float64)
			return fmt.Sprintf("Post inline comment on PR #%d at %s:%d: %s", s.prNumber, path, int(line), truncate(body, 100))
		}
		return fmt.Sprintf("Post comment on PR #%d: %s", s.prNumber, truncate(body, 100))
	case "skip_pr":
		return fmt.Sprintf("Skip PR #%d", s.prNumber)
	case "merge_pr":
		return fmt.Sprintf("Merge PR #%d", s.prNumber)
	case "set_model":
		model, _ := args["model"].(string)
		return fmt.Sprintf("Change scoring model to %q", model)
	case "set_merge_method":
		method, _ := args["method"].(string)
		return fmt.Sprintf("Change merge method to %q", method)
	case "set_criterion":
		n, _ := args["name"].(string)
		label, _ := args["label"].(string)
		desc, _ := args["description"].(string)
		weight, _ := args["weight"].(float64)
		return fmt.Sprintf("Add/update criterion %q: label=%q, weight=%.1f, description=%q", n, label, weight, desc)
	case "remove_criterion":
		n, _ := args["name"].(string)
		return fmt.Sprintf("Remove scoring criterion %q", n)
	case "set_thresholds":
		ab, _ := args["approve_below"].(float64)
		ra, _ := args["review_above"].(float64)
		return fmt.Sprintf("Set thresholds: approve_below=%.1f, review_above=%.1f", ab, ra)
	default:
		return name
	}
}

func (s *Server) executeAction(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "approve_pr":
		body, _ := args["body"].(string)
		if err := github.ApprovePR(s.repo, s.prNumber, body); err != nil {
			return "", err
		}
		return "PR approved successfully", nil
	case "request_changes":
		body, _ := args["body"].(string)
		if err := github.RequestChanges(s.repo, s.prNumber, body); err != nil {
			return "", err
		}
		return "Changes requested successfully", nil
	case "comment_on_pr":
		body, _ := args["body"].(string)
		path, _ := args["path"].(string)
		if path != "" {
			if s.commitSHA == "" {
				return "", fmt.Errorf("commit SHA not available; cannot post inline comment")
			}
			lineF, _ := args["line"].(float64)
			if err := github.PostInlineComment(s.repo, s.prNumber, s.commitSHA, path, int(lineF), body); err != nil {
				return "", err
			}
			return "Inline comment posted successfully", nil
		}
		if err := github.PostComment(s.repo, s.prNumber, body); err != nil {
			return "", err
		}
		return "Comment posted successfully", nil
	case "skip_pr":
		store := cache.LoadSkipStore()
		store.Skip(cache.SkipKey(s.repo, s.prNumber))
		s.notifySkip()
		return "PR skipped successfully", nil
	case "merge_pr":
		cfg, err := config.Load()
		if err != nil {
			return "", fmt.Errorf("loading config: %w", err)
		}
		if err := github.MergePR(s.repo, s.prNumber, cfg.Review.MergeMethod); err != nil {
			return "", err
		}
		return "PR merged successfully", nil
	case "activate_skill":
		return s.executeActivateSkill(args)
	case "read_skill_resource":
		return s.executeReadSkillResource(args)
	case "get_config", "set_model", "set_merge_method", "set_criterion", "remove_criterion", "set_thresholds":
		return s.executeConfigAction(name, args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) executeActivateSkill(args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	for _, sk := range s.skills {
		if sk.Name == name {
			var sb strings.Builder
			fmt.Fprintf(&sb, "<skill_content name=%q>\n", sk.Name)
			sb.WriteString(sk.Body)
			if sk.Source != "built-in" {
				fmt.Fprintf(&sb, "\n\nSkill directory: %s/%s", sk.Source, sk.Name)
			}
			if len(sk.Resources) > 0 {
				sb.WriteString("\n\n<skill_resources>\n")
				for _, r := range sk.Resources {
					fmt.Fprintf(&sb, "  <file>%s</file>\n", r)
				}
				sb.WriteString("</skill_resources>")
				sb.WriteString("\nUse read_skill_resource to load any of these files.")
			}
			sb.WriteString("\n</skill_content>")
			return sb.String(), nil
		}
	}
	return "", fmt.Errorf("unknown skill: %s", name)
}

func (s *Server) executeReadSkillResource(args map[string]interface{}) (string, error) {
	skillName, _ := args["skill"].(string)
	path, _ := args["path"].(string)
	if skillName == "" || path == "" {
		return "", fmt.Errorf("skill and path are required")
	}
	for _, sk := range s.skills {
		if sk.Name == skillName {
			return sk.ReadResource(path)
		}
	}
	return "", fmt.Errorf("unknown skill: %s", skillName)
}

func (s *Server) executeConfigAction(name string, args map[string]interface{}) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}

	if name == "get_config" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Model: %s\nMerge method: %s\n\nCriteria:\n", cfg.Review.Model, cfg.Review.MergeMethod)
		for _, c := range cfg.Criteria {
			fmt.Fprintf(&sb, "  - %s (%s): weight=%.1f\n    %s\n", c.Name, c.Label, c.Weight, c.Description)
		}
		fmt.Fprintf(&sb, "\nThresholds:\n  approve_below: %.1f\n  review_above:  %.1f\n", cfg.Thresholds.ApproveBelow, cfg.Thresholds.ReviewAbove)
		return sb.String(), nil
	}

	switch name {
	case "set_model":
		model, _ := args["model"].(string)
		if model == "" {
			return "", fmt.Errorf("model must be a non-empty string")
		}
		cfg.Review.Model = model

	case "set_merge_method":
		method, _ := args["method"].(string)
		switch method {
		case "merge", "squash", "rebase":
		default:
			return "", fmt.Errorf("merge method must be 'merge', 'squash', or 'rebase'")
		}
		cfg.Review.MergeMethod = method

	case "set_criterion":
		n, _ := args["name"].(string)
		label, _ := args["label"].(string)
		desc, _ := args["description"].(string)
		weight, _ := args["weight"].(float64)
		if n == "" {
			return "", fmt.Errorf("name must be non-empty")
		}
		if weight <= 0 {
			return "", fmt.Errorf("weight must be > 0")
		}
		updated := false
		for i, c := range cfg.Criteria {
			if c.Name == n {
				cfg.Criteria[i] = config.Criterion{Name: n, Label: label, Description: desc, Weight: weight}
				updated = true
				break
			}
		}
		if !updated {
			cfg.Criteria = append(cfg.Criteria, config.Criterion{Name: n, Label: label, Description: desc, Weight: weight})
		}

	case "remove_criterion":
		n, _ := args["name"].(string)
		if n == "" {
			return "", fmt.Errorf("name must be non-empty")
		}
		var newCriteria []config.Criterion
		for _, c := range cfg.Criteria {
			if c.Name != n {
				newCriteria = append(newCriteria, c)
			}
		}
		if len(newCriteria) == 0 {
			return "", fmt.Errorf("cannot remove the last criterion")
		}
		cfg.Criteria = newCriteria

	case "set_thresholds":
		ab, _ := args["approve_below"].(float64)
		ra, _ := args["review_above"].(float64)
		if ab < 1.0 || ab > 5.0 {
			return "", fmt.Errorf("approve_below must be between 1.0 and 5.0")
		}
		if ra < 1.0 || ra > 5.0 {
			return "", fmt.Errorf("review_above must be between 1.0 and 5.0")
		}
		if ab >= ra {
			return "", fmt.Errorf("approve_below (%.1f) must be less than review_above (%.1f)", ab, ra)
		}
		cfg.Thresholds.ApproveBelow = ab
		cfg.Thresholds.ReviewAbove = ra
	}

	if err := config.Save(cfg); err != nil {
		return "", fmt.Errorf("saving config: %w", err)
	}
	s.notifyConfigReload()
	return fmt.Sprintf("%s completed successfully", name), nil
}
