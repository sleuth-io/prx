package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

func CurrentUser() (string, error) {
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return "", fmt.Errorf("gh api user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func DetectRepo(dir string) (string, error) {
	// Check if we're in a git repo first
	gitCheck := exec.Command("git", "rev-parse", "--git-dir")
	gitCheck.Dir = dir
	if err := gitCheck.Run(); err != nil {
		return "", fmt.Errorf("not a git repository — prx must be run inside a git repo")
	}

	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no 'origin' remote found — prx requires a GitHub remote")
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

func MergePR(repo string, number int, method string) error {
	flag := "--merge"
	switch method {
	case "squash":
		flag = "--squash"
	case "rebase":
		flag = "--rebase"
	}
	out, err := exec.Command("gh", "pr", "merge", fmt.Sprintf("%d", number),
		"--repo", repo, flag, "--delete-branch").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
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

func ApprovePR(repo string, number int, body string) error {
	args := []string{"pr", "review", fmt.Sprintf("%d", number),
		"--repo", repo, "--approve"}
	if body != "" {
		args = append(args, "--body", body)
	}
	return exec.Command("gh", args...).Run()
}

func RequestChanges(repo string, number int, body string) error {
	return exec.Command("gh", "pr", "review", fmt.Sprintf("%d", number),
		"--repo", repo, "--request-changes", "--body", body).Run()
}

// GetReactions returns all reactions on a PR (GitHub treats PRs as issues for reactions).
func GetReactions(repo string, number int) ([]Reaction, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/reactions", repo, number),
		"--jq", `[.[] | {id: .id, user: .user.login, content: .content}]`,
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing reactions: %w", err)
	}
	var reactions []Reaction
	for _, r := range raw {
		id := 0
		if v, ok := r["id"].(float64); ok {
			id = int(v)
		}
		reactions = append(reactions, Reaction{
			ID:      id,
			User:    fmt.Sprintf("%v", r["user"]),
			Content: fmt.Sprintf("%v", r["content"]),
		})
	}
	return reactions, nil
}

// AddReaction adds an emoji reaction to a PR.
// content should be "+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", or "eyes".
func AddReaction(repo string, number int, content string) error {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/reactions", repo, number),
		"--method", "POST",
		"-f", fmt.Sprintf("content=%s", content),
	).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("add reaction: %s", msg)
		}
		return fmt.Errorf("add reaction: %w", err)
	}
	return nil
}

// RemoveReaction deletes a reaction by ID.
func RemoveReaction(repo string, number int, reactionID int) error {
	_, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/reactions/%d", repo, number, reactionID),
		"--method", "DELETE",
	).CombinedOutput()
	return err
}

// SetReaction ensures the user has exactly one +1 or -1 reaction on a PR.
// Removes any existing +1/-1 from the user first, then adds the new one.
func SetReaction(repo string, number int, content string, currentUser string) error {
	reactions, err := GetReactions(repo, number)
	if err != nil {
		return fmt.Errorf("get reactions: %w", err)
	}
	for _, r := range reactions {
		if r.User == currentUser && (r.Content == "+1" || r.Content == "-1") {
			if r.Content == content {
				return nil // already has the desired reaction
			}
			if err := RemoveReaction(repo, number, r.ID); err != nil {
				return fmt.Errorf("remove old reaction: %w", err)
			}
		}
	}
	return AddReaction(repo, number, content)
}

// FetchPRReviewAndReactionStatus checks whether a user has left any review
// or any reaction (+1/-1) on a PR. Both checks run concurrently.
func FetchPRReviewAndReactionStatus(repo string, number int, currentUser string) (hasReview bool, hasReaction bool, err error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		reviews, e := getReviews(repo, number)
		if e != nil {
			return
		}
		for _, r := range reviews {
			if r.Author == currentUser {
				hasReview = true
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		reactions, e := GetReactions(repo, number)
		if e != nil {
			return
		}
		for _, r := range reactions {
			if r.User == currentUser && (r.Content == "+1" || r.Content == "-1") {
				hasReaction = true
				return
			}
		}
	}()
	wg.Wait()
	return
}
