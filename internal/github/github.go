package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/sleuth-io/prx/internal/logger"
)

type CheckStatus struct {
	Name       string
	Conclusion string
}

type ReviewComment struct {
	Author      string
	Body        string
	State       string // only set for PR-level reviews
	Path        string // only set for inline comments
	Line        int    // only set for inline comments
	SubmittedAt string
}

type PR struct {
	Number             int
	Title              string
	Author             string
	URL                string
	CreatedAt          string
	Additions          int
	Deletions          int
	FilesChanged       int
	Diff               string
	Body               string
	HeadSHA            string
	Checks             []CheckStatus
	Reviews            []ReviewComment // PR-level review submissions (APPROVED, CHANGES_REQUESTED, etc.)
	InlineComments     []ReviewComment // line-level code comments
	Comments           []ReviewComment // top-level conversation comments
	RequestedReviewers []string
}

func (pr *PR) HasFailingChecks() bool {
	for _, c := range pr.Checks {
		if c.Conclusion == "failure" || c.Conclusion == "cancelled" || c.Conclusion == "timed_out" {
			return true
		}
	}
	return false
}

func (pr *PR) ChecksSummary() string {
	if len(pr.Checks) == 0 {
		return "no checks"
	}
	var failed, pending []string
	passed := 0
	for _, c := range pr.Checks {
		switch c.Conclusion {
		case "success":
			passed++
		case "failure", "cancelled", "timed_out":
			failed = append(failed, c.Name)
		default:
			pending = append(pending, c.Name)
		}
	}
	var parts []string
	if passed > 0 {
		parts = append(parts, fmt.Sprintf("%d passed", passed))
	}
	for _, name := range failed {
		parts = append(parts, fmt.Sprintf("FAIL: %s", name))
	}
	for _, name := range pending {
		parts = append(parts, fmt.Sprintf("pending: %s", name))
	}
	return strings.Join(parts, "  ·  ")
}

func CurrentUser() (string, error) {
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return "", fmt.Errorf("gh api user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func MergePR(repo string, number int) error {
	return exec.Command("gh", "pr", "merge", fmt.Sprintf("%d", number),
		"--repo", repo, "--squash", "--delete-branch").Run()
}

func DetectRepo(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	url := strings.TrimSpace(string(out))
	var path string
	if strings.HasPrefix(url, "git@") {
		parts := strings.SplitN(url, ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("cannot parse git remote: %s", url)
		}
		path = parts[1]
	} else if strings.Contains(url, "github.com") {
		parts := strings.SplitN(url, "github.com/", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("cannot parse github remote: %s", url)
		}
		path = parts[1]
	} else {
		return "", fmt.Errorf("not a GitHub remote: %s", url)
	}
	return strings.TrimSuffix(path, ".git"), nil
}

// ListOpenPRsMeta returns lightweight PR metadata (no diffs) — fast single API call.
func ListOpenPRsMeta(repo string) ([]map[string]any, error) {
	logger.Info("listing open PRs for %s", repo)
	out, err := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--state", "open",
		"--json", "number,title,author,url,createdAt,additions,deletions,files,body,reviewRequests,headRefOid",
		"--limit", "50",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	var rawPRs []map[string]any
	if err := json.Unmarshal(out, &rawPRs); err != nil {
		return nil, fmt.Errorf("parsing pr list: %w", err)
	}
	return rawPRs, nil
}

// FetchPRDetails fetches the diff, checks, and reviews for a single PR.
func FetchPRDetails(repo string, raw map[string]any) (*PR, error) {
	num := int(raw["number"].(float64))
	logger.Info("fetching details for PR #%d", num)

	var (
		wg             sync.WaitGroup
		diff           string
		checks         []CheckStatus
		reviews        []ReviewComment
		inlineComments []ReviewComment
		comments       []ReviewComment
	)
	wg.Add(5)
	go func() {
		defer wg.Done()
		var err error
		diff, err = getDiff(repo, num)
		if err != nil {
			logger.Error("PR #%d: getDiff: %v", num, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		checks, err = getChecks(repo, num)
		if err != nil {
			logger.Error("PR #%d: getChecks: %v", num, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		reviews, err = getReviews(repo, num)
		if err != nil {
			logger.Error("PR #%d: getReviews: %v", num, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		inlineComments, err = getInlineComments(repo, num)
		if err != nil {
			logger.Error("PR #%d: getInlineComments: %v", num, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		comments, err = getComments(repo, num)
		if err != nil {
			logger.Error("PR #%d: getComments: %v", num, err)
		}
	}()
	wg.Wait()

	author := ""
	if a, ok := raw["author"].(map[string]any); ok {
		author = fmt.Sprintf("%v", a["login"])
	}
	files := 0
	if f, ok := raw["files"].([]any); ok {
		files = len(f)
	}
	body := ""
	if b, ok := raw["body"].(string); ok {
		body = b
	}

	var requestedReviewers []string
	if rr, ok := raw["reviewRequests"].([]any); ok {
		for _, r := range rr {
			if m, ok := r.(map[string]any); ok {
				if login, ok := m["login"].(string); ok && login != "" {
					requestedReviewers = append(requestedReviewers, login)
				}
			}
		}
	}

	return &PR{
		Number:             num,
		Title:              fmt.Sprintf("%v", raw["title"]),
		Author:             author,
		URL:                fmt.Sprintf("%v", raw["url"]),
		CreatedAt:          fmt.Sprintf("%v", raw["createdAt"]),
		Additions:          int(raw["additions"].(float64)),
		Deletions:          int(raw["deletions"].(float64)),
		FilesChanged:       files,
		Diff:               diff,
		Body:               body,
		HeadSHA:            fmt.Sprintf("%v", raw["headRefOid"]),
		Checks:             checks,
		Reviews:            reviews,
		InlineComments:     inlineComments,
		Comments:           comments,
		RequestedReviewers: requestedReviewers,
	}, nil
}

func getDiff(repo string, number int) (string, error) {
	out, err := exec.Command("gh", "pr", "diff", fmt.Sprintf("%d", number), "--repo", repo).Output()
	if err != nil {
		return "", fmt.Errorf("gh pr diff: %w", err)
	}
	return string(out), nil
}

func getChecks(repo string, number int) ([]CheckStatus, error) {
	out, err := exec.Command("gh", "pr", "checks", fmt.Sprintf("%d", number),
		"--repo", repo, "--json", "name,state").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, nil
	}
	checks := make([]CheckStatus, 0, len(raw))
	for _, c := range raw {
		state := strings.ToLower(fmt.Sprintf("%v", c["state"]))
		checks = append(checks, CheckStatus{
			Name:       fmt.Sprintf("%v", c["name"]),
			Conclusion: state,
		})
	}
	return checks, nil
}

func getReviews(repo string, number int) ([]ReviewComment, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number),
		"--jq", `[.[] | {author: .user.login, body: .body, state: .state, submitted_at: .submitted_at}]`,
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, nil
	}
	var reviews []ReviewComment
	for _, r := range raw {
		body := fmt.Sprintf("%v", r["body"])
		if body == "" || body == "<nil>" {
			continue
		}
		reviews = append(reviews, ReviewComment{
			Author:      fmt.Sprintf("%v", r["author"]),
			Body:        body,
			State:       fmt.Sprintf("%v", r["state"]),
			SubmittedAt: fmt.Sprintf("%v", r["submitted_at"]),
		})
	}
	return reviews, nil
}

func getComments(repo string, number int) ([]ReviewComment, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/comments", repo, number),
		"--paginate",
		"--jq", `.[] | {author: .user.login, body: .body, submitted_at: .created_at}`,
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var comments []ReviewComment
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			logger.Error("getComments: parse line: %v", err)
			continue
		}
		body := fmt.Sprintf("%v", r["body"])
		if body == "" || body == "<nil>" {
			continue
		}
		comments = append(comments, ReviewComment{
			Author:      fmt.Sprintf("%v", r["author"]),
			Body:        body,
			SubmittedAt: fmt.Sprintf("%v", r["submitted_at"]),
		})
	}
	return comments, nil
}

func getInlineComments(repo string, number int) ([]ReviewComment, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number),
		"--paginate",
		"--jq", `.[] | {author: .user.login, body: .body, path: .path, line: (.line // .original_line // 0), submitted_at: .created_at}`,
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var comments []ReviewComment
	for _, lineStr := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if lineStr == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(lineStr), &r); err != nil {
			logger.Error("getInlineComments: parse line: %v", err)
			continue
		}
		body := fmt.Sprintf("%v", r["body"])
		if body == "" || body == "<nil>" {
			continue
		}
		line := 0
		if l, ok := r["line"].(float64); ok {
			line = int(l)
		}
		comments = append(comments, ReviewComment{
			Author:      fmt.Sprintf("%v", r["author"]),
			Body:        body,
			Path:        fmt.Sprintf("%v", r["path"]),
			Line:        line,
			SubmittedAt: fmt.Sprintf("%v", r["submitted_at"]),
		})
	}
	return comments, nil
}

func PostComment(repo string, number int, body string) error {
	return exec.Command("gh", "pr", "comment", fmt.Sprintf("%d", number),
		"--repo", repo, "--body", body).Run()
}

func PostInlineComment(repo string, number int, commitSHA, path string, line int, body string) error {
	payload := fmt.Sprintf(`{"body":%q,"commit_id":%q,"path":%q,"line":%d,"side":"RIGHT"}`,
		body, commitSHA, path, line)
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number),
		"--method", "POST", "--input", "-")
	cmd.Stdin = strings.NewReader(payload)
	return cmd.Run()
}

func ApprovePR(repo string, number int) error {
	return exec.Command("gh", "pr", "review", fmt.Sprintf("%d", number),
		"--repo", repo, "--approve").Run()
}

func RequestChanges(repo string, number int, body string) error {
	return exec.Command("gh", "pr", "review", fmt.Sprintf("%d", number),
		"--repo", repo, "--request-changes", "--body", body).Run()
}
