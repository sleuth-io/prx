package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

func parseDiffCmd(pr *github.PR) tea.Cmd {
	return func() tea.Msg {
		return prDiffParsedMsg{prNumber: pr.Number, files: parseDiff(pr.Diff)}
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

func scorePRCmd(pr *github.PR, a *app.App) tea.Cmd {
	return func() tea.Msg {
		key := cache.Key(a.Repo, pr.Number, pr.Diff, reviewsText(pr), a.Config.Criteria)

		if assessment, ok := a.Cache.Get(key); ok {
			logger.Info("PR #%d: cache hit", pr.Number)
			return prScoredMsg{prNumber: pr.Number, assessment: &assessment, fromCache: true}
		}

		assessment, err := ai.AssessPR(pr, a.RepoDir, a.Config.Criteria)
		if err != nil {
			return prScoredMsg{prNumber: pr.Number, err: err}
		}
		a.Cache.Set(key, *assessment)
		return prScoredMsg{prNumber: pr.Number, assessment: assessment}
	}
}

func mergeCmd(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		err := github.MergePR(repo, number)
		return actionDoneMsg{pr: number, action: "merge", err: err}
	}
}

func approveCmd(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		err := github.ApprovePR(repo, number)
		return actionDoneMsg{pr: number, action: "approve", err: err}
	}
}

func postGlobalCommentCmd(repo string, prNumber int, body string, item *commentItem) tea.Cmd {
	return func() tea.Msg {
		err := github.PostComment(repo, prNumber, body)
		return commentSubmittedMsg{prNumber: prNumber, body: body, pendingItem: item, err: err}
	}
}

func postInlineCommentCmd(repo string, prNumber int, sha, path string, line int, body string, item *commentItem) tea.Cmd {
	return func() tea.Msg {
		err := github.PostInlineComment(repo, prNumber, sha, path, line, body)
		return commentSubmittedMsg{prNumber: prNumber, isInline: true,
			filePath: path, fileLine: line, body: body, pendingItem: item, err: err}
	}
}

func requestChangesCmd(repo string, number int, body string) tea.Cmd {
	return func() tea.Msg {
		err := github.RequestChanges(repo, number, body)
		return actionDoneMsg{pr: number, action: "request-changes", err: err}
	}
}

func reviewsText(pr *github.PR) string {
	var sb strings.Builder
	for _, r := range pr.Reviews {
		fmt.Fprintf(&sb, "%s|%s|%s\n", r.Author, r.State, r.Body)
	}
	for _, c := range pr.InlineComments {
		fmt.Fprintf(&sb, "%s|inline|%s|%s\n", c.Author, c.Path, c.Body)
	}
	for _, c := range pr.Comments {
		fmt.Fprintf(&sb, "%s|comment|%s\n", c.Author, c.Body)
	}
	return sb.String()
}

func weightedScore(assessment *ai.Assessment, app *app.App) float64 {
	var totalWeight, weighted float64
	for _, c := range app.Config.Criteria {
		if f, ok := assessment.Factors[c.Name]; ok {
			totalWeight += c.Weight
			weighted += float64(f.Score) * c.Weight
		}
	}
	if totalWeight == 0 {
		return 0
	}
	return math.Round(weighted/totalWeight*10) / 10
}

func computeVerdict(score float64, app *app.App) string {
	t := app.Config.Thresholds
	if score < t.ApproveBelow {
		return "approve"
	}
	if score > t.ReviewAbove {
		return "reject"
	}
	return "review"
}

func createWorktreeCmd(repoDir string, headRefName string, prNumber int) tea.Cmd {
	return func() tea.Msg {
		logger.Info("worktree: fetching branch %s for PR #%d", headRefName, prNumber)
		// Fetch the PR branch so the ref exists locally
		fetchCmd := exec.Command("git", "fetch", "origin", headRefName)
		fetchCmd.Dir = repoDir
		if out, err := fetchCmd.CombinedOutput(); err != nil {
			logger.Error("fetch for PR #%d branch %s: %v\n%s", prNumber, headRefName, err, string(out))
			return chatWorktreeReadyMsg{prNumber: prNumber, err: fmt.Errorf("git fetch: %w\n%s", err, string(out))}
		}
		logger.Info("worktree: fetch done for PR #%d, creating worktree", prNumber)

		path := fmt.Sprintf("/tmp/prx-%d-%d", prNumber, rand.Intn(100000))
		cmd := exec.Command("git", "worktree", "add", path, "FETCH_HEAD", "--detach")
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

func sendChatCmd(ctx context.Context, worktreePath string, pr *github.PR, assessment *ai.Assessment, messages []chatMessage, diffCtx *ai.DiffContext, program *tea.Program) tea.Cmd {
	return func() tea.Msg {
		// Convert to ai.ChatMessage
		history := make([]ai.ChatMessage, len(messages))
		for i, m := range messages {
			history[i] = ai.ChatMessage{Role: m.role, Content: m.content}
		}

		prompt := ai.BuildChatPrompt(pr, assessment, history, diffCtx)
		cmd := exec.CommandContext(ctx, "claude",
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--allowedTools", "Read,Glob,Grep",
			"--strict-mcp-config",
			"--no-session-persistence",
		)
		cmd.Dir = worktreePath

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return chatDoneMsg{prNumber: pr.Number, err: fmt.Errorf("stdout pipe: %w", err)}
		}
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf
		if err := cmd.Start(); err != nil {
			return chatDoneMsg{prNumber: pr.Number, err: fmt.Errorf("start claude: %w", err)}
		}
		var fullResponse strings.Builder
		// Track what we've already sent as tokens so we can send only deltas
		prevLen := 0
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		sentInit := false
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var event map[string]json.RawMessage
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				logger.Debug("chat stream parse: %v", err)
				continue
			}
			var eventType string
			if raw, ok := event["type"]; ok {
				_ = json.Unmarshal(raw, &eventType)
			}
			logger.Debug("chat event: %s", eventType)

			switch eventType {
			case "system":
				if !sentInit {
					sentInit = true
					program.Send(chatStatusMsg{prNumber: pr.Number, status: "Thinking..."})
				}
			case "assistant":
				// Extract text from message.content[].text
				var msg struct {
					Message struct {
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
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
					}
				}
				if text != "" {
					fullResponse.Reset()
					fullResponse.WriteString(text)
					// Send only the new delta
					if len(text) > prevLen {
						delta := text[prevLen:]
						prevLen = len(text)
						program.Send(chatTokenMsg{prNumber: pr.Number, token: delta})
					}
				}
			case "result":
				var result struct {
					Result  string `json:"result"`
					IsError bool   `json:"is_error"`
				}
				_ = json.Unmarshal([]byte(line), &result)
				if result.IsError {
					return chatDoneMsg{prNumber: pr.Number, err: fmt.Errorf("claude: %s", result.Result)}
				}
				if result.Result != "" && fullResponse.Len() == 0 {
					fullResponse.WriteString(result.Result)
					program.Send(chatTokenMsg{prNumber: pr.Number, token: result.Result})
				}
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			logger.Error("chat scanner error for PR #%d: %v", pr.Number, scanErr)
		}

		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				// User cancelled
				return chatDoneMsg{prNumber: pr.Number, fullResponse: fullResponse.String()}
			}
			logger.Error("chat claude exit for PR #%d: %v\nstderr: %s", pr.Number, err, stderrBuf.String())
			if fullResponse.Len() == 0 {
				errMsg := stderrBuf.String()
				if errMsg == "" {
					errMsg = err.Error()
				}
				return chatDoneMsg{prNumber: pr.Number, err: fmt.Errorf("claude chat failed: %s", errMsg)}
			}
		}

		return chatDoneMsg{prNumber: pr.Number, fullResponse: fullResponse.String()}
	}
}
