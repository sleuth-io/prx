package app

import (
	"fmt"
	"os/exec"
	"strings"
)

// CheckDeps verifies that required external tools (gh, claude) are installed and authenticated.
func CheckDeps() error {
	if err := checkGH(); err != nil {
		return err
	}
	if err := checkClaude(); err != nil {
		return err
	}
	return nil
}

func checkGH() error {
	path, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("GitHub CLI (gh) not found in PATH.\nInstall it: https://cli.github.com/")
	}
	_ = path

	out, err := exec.Command("gh", "auth", "status").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		return fmt.Errorf("GitHub CLI is not authenticated.\nRun: gh auth login\n\n%s", msg)
	}
	return nil
}

func checkClaude() error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("Claude Code CLI (claude) not found in PATH.\nInstall it: https://docs.anthropic.com/en/docs/claude-code/overview")
	}
	_ = path

	// Verify claude can respond — a quick non-interactive check.
	// "claude --version" doesn't require auth but confirms the binary works.
	// There's no "auth status" equivalent, so we check the binary runs.
	out, err := exec.Command("claude", "--version").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		return fmt.Errorf("Claude Code CLI found but not working.\nCheck your installation: https://docs.anthropic.com/en/docs/claude-code/overview\n\n%s", msg)
	}
	return nil
}
