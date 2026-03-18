package tui

import (
	"fmt"
	"math"
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
