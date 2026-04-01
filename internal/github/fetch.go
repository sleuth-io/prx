package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sleuth-io/prx/internal/logger"
)

// ListOpenPRsMeta returns lightweight PR metadata (no diffs) — fast single API call.
func ListOpenPRsMeta(repo string) ([]map[string]any, error) {
	logger.Info("listing open PRs for %s", repo)
	out, err := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--state", "open",
		"--json", "number,title,author,url,createdAt,additions,deletions,files,body,reviewRequests,headRefOid,headRefName,mergeStateStatus",
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

	mergeStateStatus := ""
	if m, ok := raw["mergeStateStatus"].(string); ok {
		mergeStateStatus = m
	}

	state := "OPEN"
	if s, ok := raw["state"].(string); ok && s != "" {
		state = s
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
		HeadRefName:        fmt.Sprintf("%v", raw["headRefName"]),
		State:              state,
		MergeStateStatus:   mergeStateStatus,
		Checks:             checks,
		Reviews:            reviews,
		InlineComments:     inlineComments,
		Comments:           comments,
		RequestedReviewers: requestedReviewers,
	}, nil
}

// FetchPRActivity re-fetches activity data and current metadata for an existing PR,
// without re-downloading the diff. The returned PRActivity includes Title, Body,
// HeadSHA, HeadRefName so callers can detect renames, description edits, and new commits.
func FetchPRActivity(repo string, number int) (*PRActivity, error) {
	var (
		wg             sync.WaitGroup
		title          string
		body           string
		headSHA        string
		headRefName    string
		checks         []CheckStatus
		reviews        []ReviewComment
		inlineComments []ReviewComment
		comments       []ReviewComment
	)
	wg.Add(5)
	var state, mergeStateStatus string
	go func() {
		defer wg.Done()
		out, err := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", number),
			"--repo", repo, "--json", "title,body,headRefOid,headRefName,state,mergeStateStatus").Output()
		if err != nil {
			logger.Error("PR #%d: pr view meta: %v", number, err)
			return
		}
		var meta struct {
			Title            string `json:"title"`
			Body             string `json:"body"`
			HeadRefOid       string `json:"headRefOid"`
			HeadRefName      string `json:"headRefName"`
			State            string `json:"state"`
			MergeStateStatus string `json:"mergeStateStatus"`
		}
		if err := json.Unmarshal(out, &meta); err != nil {
			logger.Error("PR #%d: parse pr view meta: %v", number, err)
			return
		}
		title, body, headSHA, headRefName, state, mergeStateStatus = meta.Title, meta.Body, meta.HeadRefOid, meta.HeadRefName, meta.State, meta.MergeStateStatus
	}()
	go func() {
		defer wg.Done()
		var err error
		checks, err = getChecks(repo, number)
		if err != nil {
			logger.Error("PR #%d: getChecks: %v", number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		reviews, err = getReviews(repo, number)
		if err != nil {
			logger.Error("PR #%d: getReviews: %v", number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		inlineComments, err = getInlineComments(repo, number)
		if err != nil {
			logger.Error("PR #%d: getInlineComments: %v", number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		comments, err = getComments(repo, number)
		if err != nil {
			logger.Error("PR #%d: getComments: %v", number, err)
		}
	}()
	wg.Wait()
	return &PRActivity{
		Title:            title,
		Body:             body,
		HeadSHA:          headSHA,
		HeadRefName:      headRefName,
		State:            state,
		MergeStateStatus: mergeStateStatus,
		Checks:           checks,
		Reviews:          reviews,
		InlineComments:   inlineComments,
		Comments:         comments,
	}, nil
}

// FetchDiff returns the unified diff for a PR.
func FetchDiff(repo string, number int) (string, error) {
	return getDiff(repo, number)
}

// FetchPRFull re-fetches all PR data (diff + activity) for an existing PR.
// Metadata fields (title, author, body, etc.) are copied from existing.
// Used for force-refresh where the diff may also have changed.
func FetchPRFull(repo string, existing *PR) (*PR, error) {
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
		diff, err = getDiff(repo, existing.Number)
		if err != nil {
			logger.Error("PR #%d: getDiff: %v", existing.Number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		checks, err = getChecks(repo, existing.Number)
		if err != nil {
			logger.Error("PR #%d: getChecks: %v", existing.Number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		reviews, err = getReviews(repo, existing.Number)
		if err != nil {
			logger.Error("PR #%d: getReviews: %v", existing.Number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		inlineComments, err = getInlineComments(repo, existing.Number)
		if err != nil {
			logger.Error("PR #%d: getInlineComments: %v", existing.Number, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		comments, err = getComments(repo, existing.Number)
		if err != nil {
			logger.Error("PR #%d: getComments: %v", existing.Number, err)
		}
	}()
	wg.Wait()

	fresh := *existing // copy metadata
	fresh.Diff = diff
	fresh.Checks = checks
	fresh.Reviews = reviews
	fresh.InlineComments = inlineComments
	fresh.Comments = comments
	return &fresh, nil
}

// ListMergedPRsMeta returns lightweight metadata for recently merged PRs (no diffs).
// Excludes PRs authored by currentUser.
func ListMergedPRsMeta(repo, currentUser string, since time.Time) ([]map[string]any, error) {
	sinceStr := since.Format("2006-01-02")
	logger.Info("listing merged PRs for %s since %s", repo, sinceStr)
	out, err := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--state", "merged",
		"--search", fmt.Sprintf("merged:>%s", sinceStr),
		"--json", "number,title,author,url,createdAt,additions,deletions,files,body,reviewRequests,headRefOid,headRefName,mergeStateStatus,state",
		"--limit", "50",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list (merged): %w", err)
	}
	var rawPRs []map[string]any
	if err := json.Unmarshal(out, &rawPRs); err != nil {
		return nil, fmt.Errorf("parsing merged pr list: %w", err)
	}
	// Filter out own PRs.
	var filtered []map[string]any
	for _, raw := range rawPRs {
		if a, ok := raw["author"].(map[string]any); ok {
			if fmt.Sprintf("%v", a["login"]) == currentUser {
				continue
			}
		}
		filtered = append(filtered, raw)
	}
	return filtered, nil
}

// FetchPRMeta returns lightweight metadata for a single PR by number.
func FetchPRMeta(repo string, number int) (map[string]any, error) {
	out, err := exec.Command("gh", "pr", "view",
		fmt.Sprintf("%d", number),
		"--repo", repo,
		"--json", "number,title,author,url,createdAt,additions,deletions,files,body,reviewRequests,headRefOid,headRefName,mergeStateStatus,state",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view %d: %w", number, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing pr view %d: %w", number, err)
	}
	return raw, nil
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
		if body == "<nil>" {
			body = ""
		}
		state := fmt.Sprintf("%v", r["state"])
		// Keep reviews with a meaningful state (APPROVED, CHANGES_REQUESTED, etc.)
		// even if the body is empty. Only skip empty-body COMMENTED reviews.
		if body == "" && (state == "" || state == "COMMENTED" || state == "PENDING") {
			continue
		}
		reviews = append(reviews, ReviewComment{
			Author:      fmt.Sprintf("%v", r["author"]),
			Body:        body,
			State:       state,
			SubmittedAt: fmt.Sprintf("%v", r["submitted_at"]),
		})
	}
	return reviews, nil
}

func getComments(repo string, number int) ([]ReviewComment, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/comments", repo, number),
		"--paginate",
		"--jq", `.[] | {id: .id, author: .user.login, body: .body, submitted_at: .created_at}`,
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
		var commentID int
		if id, ok := r["id"].(float64); ok {
			commentID = int(id)
		}
		comments = append(comments, ReviewComment{
			ID:          commentID,
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
		"--jq", `.[] | {id: .id, in_reply_to_id: (.in_reply_to_id // 0), author: .user.login, body: .body, path: .path, line: (.line // .original_line // 0), submitted_at: .created_at}`,
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
		var commentID int
		if id, ok := r["id"].(float64); ok {
			commentID = int(id)
		}
		var inReplyToID int
		if id, ok := r["in_reply_to_id"].(float64); ok {
			inReplyToID = int(id)
		}
		comments = append(comments, ReviewComment{
			ID:          commentID,
			InReplyToID: inReplyToID,
			Author:      fmt.Sprintf("%v", r["author"]),
			Body:        body,
			Path:        fmt.Sprintf("%v", r["path"]),
			Line:        line,
			SubmittedAt: fmt.Sprintf("%v", r["submitted_at"]),
		})
	}
	return comments, nil
}
