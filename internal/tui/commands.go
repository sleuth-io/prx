package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/imgrender"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/mcp"
	"github.com/sleuth-io/prx/internal/tui/chat"
	"github.com/sleuth-io/prx/internal/tui/diff"
)

func parseDiffCmd(pr *github.PR) tea.Cmd {
	return func() tea.Msg {
		return prDiffParsedMsg{prNumber: pr.Number, files: diff.ParseDiff(pr.Diff)}
	}
}

func fetchPRListCmd(repo string) tea.Cmd {
	return func() tea.Msg {
		rawPRs, err := github.ListOpenPRsMeta(repo)
		return prListFetchedMsg{rawPRs: rawPRs, err: err}
	}
}

func fetchPRDetailsCmd(raw map[string]any, a *app.App) tea.Cmd {
	return func() tea.Msg {
		pr, err := github.FetchPRDetails(a.Repo, raw)
		return prDetailsFetchedMsg{pr: pr, raw: raw, err: err}
	}
}

func refreshPRCmd(pr *github.PR, a *app.App) tea.Cmd {
	return func() tea.Msg {
		activity, err := github.FetchPRActivity(a.Repo, pr.Number)
		if err != nil {
			return prRefreshedMsg{prNumber: pr.Number, err: err}
		}
		var newDiff string
		if activity.HeadSHA != "" && activity.HeadSHA != pr.HeadSHA {
			logger.Info("PR #%d: SHA changed (%s → %s), re-fetching diff", pr.Number, pr.HeadSHA[:8], activity.HeadSHA[:8])
			newDiff, _ = github.FetchDiff(a.Repo, pr.Number)
		}
		return prRefreshedMsg{prNumber: pr.Number, activity: activity, newDiff: newDiff}
	}
}

// forceScorePRCmd scores a PR unconditionally, bypassing the cache.
func forceScorePRCmd(pr *github.PR, a *app.App, program *tea.Program) tea.Cmd {
	return func() tea.Msg {
		callbacks := scoringCallbacks(pr.Number, program)
		assessment, err := ai.AssessPR(context.Background(), pr, a.RepoDir, a.Config.Criteria, a.Config.Review.Model, callbacks)
		if err != nil {
			return prScoredMsg{prNumber: pr.Number, err: err}
		}
		key := cache.Key(a.Repo, pr.Number, pr.Diff, reviewsText(pr, a.CurrentUser), a.Config.Criteria)
		a.Cache.Set(key, *assessment)
		return prScoredMsg{prNumber: pr.Number, assessment: assessment}
	}
}

func scorePRCmd(pr *github.PR, a *app.App, program *tea.Program) tea.Cmd {
	return func() tea.Msg {
		key := cache.Key(a.Repo, pr.Number, pr.Diff, reviewsText(pr, a.CurrentUser), a.Config.Criteria)

		if assessment, ok := a.Cache.Get(key); ok {
			logger.Info("PR #%d: cache hit", pr.Number)
			assessment.DiffTruncated = len(pr.Diff) > 30000
			return prScoredMsg{prNumber: pr.Number, assessment: &assessment, fromCache: true}
		}

		callbacks := scoringCallbacks(pr.Number, program)
		assessment, err := ai.AssessPR(context.Background(), pr, a.RepoDir, a.Config.Criteria, a.Config.Review.Model, callbacks)
		if err != nil {
			return prScoredMsg{prNumber: pr.Number, err: err}
		}
		a.Cache.Set(key, *assessment)
		return prScoredMsg{prNumber: pr.Number, assessment: assessment}
	}
}

// scoringCallbacks wires up StreamCallbacks that send scoring progress messages to the TUI.
func scoringCallbacks(prNumber int, program *tea.Program) ai.StreamCallbacks {
	return ai.StreamCallbacks{
		OnStatus: func(status string) {
			program.Send(scoringStatusMsg{prNumber: prNumber, status: status})
		},
		OnToolCall: func(count int, summary string) {
			program.Send(scoringToolCallMsg{prNumber: prNumber, count: count, lastTool: summary})
		},
	}
}

func mergeCmd(repo string, number int, method string) tea.Cmd {
	return func() tea.Msg {
		err := github.MergePR(repo, number, method)
		return actionDoneMsg{pr: number, action: actionMerge, err: err}
	}
}

func approveCmd(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		err := github.ApprovePR(repo, number)
		return actionDoneMsg{pr: number, action: actionApprove, err: err}
	}
}

func postGlobalCommentCmd(repo string, prNumber int, body string, item *diff.CommentItem) tea.Cmd {
	return func() tea.Msg {
		err := github.PostComment(repo, prNumber, body)
		return commentSubmittedMsg{prNumber: prNumber, body: body, pendingItem: item, err: err}
	}
}

func postInlineCommentCmd(repo string, prNumber int, sha, path string, line int, body string, item *diff.CommentItem) tea.Cmd {
	return func() tea.Msg {
		err := github.PostInlineComment(repo, prNumber, sha, path, line, body)
		return commentSubmittedMsg{prNumber: prNumber, isInline: true,
			filePath: path, fileLine: line, body: body, pendingItem: item, err: err}
	}
}

func fetchImageCmd(prNumber int, url string, cache *imgrender.Cache) tea.Cmd {
	return func() tea.Msg {
		_, err := cache.FetchAndRender(url)
		return imageFetchedMsg{prNumber: prNumber, url: url, err: err}
	}
}

func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
		return nil
	}
}

func fetchMergedPRListCmd(repo, currentUser string) tea.Cmd {
	return func() tea.Msg {
		since := time.Now().AddDate(0, 0, -7)
		rawPRs, err := github.ListMergedPRsMeta(repo, currentUser, since)
		return mergedPRListFetchedMsg{rawPRs: rawPRs, err: err}
	}
}

func fetchMergedPRStatusCmd(repo string, number int, currentUser string) tea.Cmd {
	return func() tea.Msg {
		hasReview, hasReaction, err := github.FetchPRReviewAndReactionStatus(repo, number, currentUser)
		return mergedPRStatusMsg{prNumber: number, hasReview: hasReview, hasReaction: hasReaction, err: err}
	}
}

func addReactionCmd(repo string, number int, content, action, currentUser string) tea.Cmd {
	return func() tea.Msg {
		err := github.SetReaction(repo, number, content, currentUser)
		return actionDoneMsg{pr: number, action: action, err: err}
	}
}

func requestChangesCmd(repo string, number int, body string) tea.Cmd {
	return func() tea.Msg {
		err := github.RequestChanges(repo, number, body)
		return actionDoneMsg{pr: number, action: actionRequestChanges, err: err}
	}
}

// skillCatalog converts the app's discovered skills to the catalog format for prompts.
func (m *Model) skillCatalog() []ai.SkillCatalog {
	if len(m.app.Skills) == 0 {
		return nil
	}
	catalog := make([]ai.SkillCatalog, len(m.app.Skills))
	for i, s := range m.app.Skills {
		catalog[i] = ai.SkillCatalog{Name: s.Name, Description: s.Description}
	}
	return catalog
}

// reviewsText builds a stable string for cache keying. Only includes reviews
// and comments with substantive content — bare approvals/rejections without
// commentary don't change the risk assessment and shouldn't bust the cache.
// The current user is always excluded since their own actions aren't risk-relevant.
func reviewsText(pr *github.PR, excludeUser ...string) string {
	exclude := ""
	if len(excludeUser) > 0 {
		exclude = excludeUser[0]
	}
	var sb strings.Builder
	for _, r := range pr.Reviews {
		if r.Author == exclude || r.Body == "" {
			continue
		}
		fmt.Fprintf(&sb, "%s|%s|%s\n", r.Author, r.State, r.Body)
	}
	for _, c := range pr.InlineComments {
		if c.Author == exclude {
			continue
		}
		fmt.Fprintf(&sb, "%s|inline|%s|%s\n", c.Author, c.Path, c.Body)
	}
	for _, c := range pr.Comments {
		if c.Author == exclude {
			continue
		}
		fmt.Fprintf(&sb, "%s|comment|%s\n", c.Author, c.Body)
	}
	return sb.String()
}

func createWorktreeCmd(repoDir string, sha string, prNumber int) tea.Cmd {
	return func() tea.Msg {
		shortSHA := sha[:min(8, len(sha))]
		logger.Info("worktree: fetching %s for PR #%d", shortSHA, prNumber)
		fetchCmd := exec.Command("git", "fetch", "origin", sha)
		fetchCmd.Dir = repoDir
		if out, err := fetchCmd.CombinedOutput(); err != nil {
			logger.Error("fetch for PR #%d sha %s: %v\n%s", prNumber, shortSHA, err, string(out))
			return chatWorktreeReadyMsg{prNumber: prNumber, err: fmt.Errorf("git fetch: %w\n%s", err, string(out))}
		}

		path := fmt.Sprintf("/tmp/prx-%d-%d", prNumber, rand.Intn(100000))
		cmd := exec.Command("git", "worktree", "add", path, sha, "--detach")
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			logger.Error("worktree create for PR #%d: %v\n%s", prNumber, err, string(out))
			return chatWorktreeReadyMsg{prNumber: prNumber, err: fmt.Errorf("git worktree add: %w\n%s", err, string(out))}
		}
		logger.Info("worktree created for PR #%d at %s", prNumber, path)
		return chatWorktreeReadyMsg{prNumber: prNumber, path: path}
	}
}

func removeWorktreeCmd(repoDir, path string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("git", "worktree", "remove", path, "--force")
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			logger.Error("worktree remove %s: %v\n%s", path, err, string(out))
		} else {
			logger.Info("worktree removed: %s", path)
		}
		return nil
	}
}

// sendChatCmdWarm sends a chat message using a pre-warmed Claude process.
func sendChatCmdWarm(wp *ai.WarmProcess, pr *github.PR, assessment *ai.Assessment, messages []chat.Message, diffCtx *ai.DiffContext, isOwnPR bool, socketPath string, program *tea.Program, skillCatalog []ai.SkillCatalog) tea.Cmd {
	return func() tea.Msg {
		history := make([]ai.ChatMessage, len(messages))
		for i, m := range messages {
			history[i] = ai.ChatMessage{Role: m.Role, Content: m.Content}
		}

		var availableActions []string
		if socketPath != "" {
			availableActions = ActionToolNames(isOwnPR)
		}

		prompt := ai.BuildChatPrompt(pr, assessment, history, diffCtx, availableActions, skillCatalog)

		callbacks := ai.StreamCallbacks{
			OnStatus: func(status string) {
				program.Send(chatStatusMsg{prNumber: pr.Number, status: status})
			},
			OnToolCall: func(count int, summary string) {
				program.Send(chatToolCallMsg{prNumber: pr.Number, count: count, lastTool: summary})
			},
			OnToken: func(delta string) {
				program.Send(chatTokenMsg{prNumber: pr.Number, token: delta})
			},
		}

		result, err := wp.Send(prompt, callbacks)
		if err != nil {
			return chatDoneMsg{prNumber: pr.Number, err: err}
		}
		return chatDoneMsg{prNumber: pr.Number, fullResponse: result}
	}
}

func sendChatCmd(ctx context.Context, worktreePath string, pr *github.PR, assessment *ai.Assessment, messages []chat.Message, diffCtx *ai.DiffContext, model string, repo string, isOwnPR bool, socketPath string, program *tea.Program, skillCatalog []ai.SkillCatalog) tea.Cmd {
	return func() tea.Msg {
		history := make([]ai.ChatMessage, len(messages))
		for i, m := range messages {
			history[i] = ai.ChatMessage{Role: m.Role, Content: m.Content}
		}

		actionTools := ActionToolNames(isOwnPR)

		// Write a temp MCP config so Claude can call our mcp-server subprocess.
		var mcpConfigFile string
		binPath, binErr := os.Executable()
		if binErr == nil && socketPath != "" {
			mcpCfg := map[string]any{
				"mcpServers": map[string]any{
					"prx": map[string]any{
						"command": binPath,
						"args": []string{
							"mcp-server",
							"--socket=" + socketPath,
							"--repo=" + repo,
							"--pr=" + strconv.Itoa(pr.Number),
							"--commit=" + pr.HeadSHA,
						},
					},
				},
			}
			if cfgBytes, err := json.Marshal(mcpCfg); err == nil {
				if tmp, err := os.CreateTemp("", "prx-mcp-*.json"); err == nil {
					mcpConfigFile = tmp.Name()
					_, _ = tmp.Write(cfgBytes)
					_ = tmp.Close()
					defer func() { _ = os.Remove(mcpConfigFile) }()
				}
			}
		}
		var availableActions []string
		if socketPath != "" {
			availableActions = actionTools
		}

		prompt := ai.BuildChatPrompt(pr, assessment, history, diffCtx, availableActions, skillCatalog)
		allTools := append([]string{"Read", "Glob", "Grep"}, mcp.ToolNames()...)
		if len(availableActions) > 0 {
			allTools = append(allTools, availableActions...)
		}
		allowedTools := strings.Join(allTools, ",")
		args := []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--allowedTools", allowedTools,
			"--strict-mcp-config",
			"--no-session-persistence",
		}
		if mcpConfigFile != "" {
			args = append(args, "--mcp-config", mcpConfigFile)
		}
		if model != "" {
			args = append(args, "--model", model)
		}

		callbacks := ai.StreamCallbacks{
			OnStatus: func(status string) {
				program.Send(chatStatusMsg{prNumber: pr.Number, status: status})
			},
			OnToolCall: func(count int, summary string) {
				program.Send(chatToolCallMsg{prNumber: pr.Number, count: count, lastTool: summary})
			},
			OnToken: func(delta string) {
				program.Send(chatTokenMsg{prNumber: pr.Number, token: delta})
			},
		}

		result, err := ai.RunClaude(ctx, args, worktreePath, callbacks)
		if err != nil {
			return chatDoneMsg{prNumber: pr.Number, err: err}
		}
		return chatDoneMsg{prNumber: pr.Number, fullResponse: result}
	}
}
