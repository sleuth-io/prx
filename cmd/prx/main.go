package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/autoupdate"
	"github.com/sleuth-io/prx/internal/buildinfo"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/mcp"
	"github.com/sleuth-io/prx/internal/skills"
	"github.com/sleuth-io/prx/internal/tui"
)

func main() {
	// Resolve executable path before any update replaces the binary on disk.
	exe, _ := os.Executable()

	// Apply any pending update from a previous background check.
	// If an update was applied, re-exec so the new binary handles this invocation.
	if autoupdate.ApplyPendingUpdate() && exe != "" {
		_ = syscall.Exec(exe, os.Args, os.Environ())
		// If re-exec fails, fall through to run current binary
	}

	// Check for updates in the background (non-blocking, once per day).
	// Skip if user is running the update or mcp-server subcommand.
	if len(os.Args) < 2 || (os.Args[1] != "mcp-server" && os.Args[1] != "update") {
		autoupdate.CheckAndUpdateInBackground()
	}

	rootCmd := &cobra.Command{
		Use:           "prx [path]",
		Short:         "AI-powered pull request review TUI",
		Long:          "prx reviews your GitHub pull requests with AI-powered risk assessment, right in the terminal.",
		Args:          cobra.MaximumNArgs(1),
		Version:       fmt.Sprintf("%s (%s, %s)", buildinfo.Version, buildinfo.Commit, buildinfo.Date),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := logger.Init(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not init log: %v\n", err)
			}

			if err := app.CheckDeps(); err != nil {
				return err
			}

			repoDir := "."
			if len(args) > 0 {
				repoDir = args[0]
			}

			a, err := app.New(repoDir)
			if err != nil {
				return err
			}

			m := tui.New(a)
			p := tea.NewProgram(m, tea.WithAltScreen())
			go func() {
				p.Send(tui.SetProgramMsg{Program: p})
			}()
			if _, err := p.Run(); err != nil {
				return err
			}
			return nil
		},
	}

	mcpCmd := &cobra.Command{
		Use:   "mcp-server",
		Short: "Run the MCP JSON-RPC server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := logger.Init(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not init log: %v\n", err)
			}

			socket, _ := cmd.Flags().GetString("socket")
			repo, _ := cmd.Flags().GetString("repo")
			pr, _ := cmd.Flags().GetString("pr")
			commit, _ := cmd.Flags().GetString("commit")

			prNumber, _ := strconv.Atoi(pr)
			mcp.New(repo, prNumber, commit, socket, skills.Discover()).Run()
			return nil
		},
	}
	mcpCmd.Flags().String("socket", "", "Unix socket path for permission requests")
	mcpCmd.Flags().String("repo", "", "GitHub repo (owner/name)")
	mcpCmd.Flags().String("pr", "", "PR number")
	mcpCmd.Flags().String("commit", "", "Head commit SHA (required for inline comments)")

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Update prx to the latest version",
		Long:  "Check for and install the latest version of prx from GitHub releases.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			currentVersion := buildinfo.Version
			if currentVersion == "dev" || currentVersion == "" {
				fmt.Fprintln(os.Stderr, "Cannot update development builds. Please install from a release.")
				return nil
			}

			checkOnly, _ := cmd.Flags().GetBool("check")

			fmt.Printf("Current version: %s\n", currentVersion)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			slug := selfupdate.ParseSlug(fmt.Sprintf("%s/%s", autoupdate.GithubOwner, autoupdate.GithubRepo))

			fmt.Print("Checking for updates... ")
			latest, found, err := selfupdate.DetectLatest(ctx, slug)
			if err != nil {
				fmt.Println("failed")
				return fmt.Errorf("failed to check for updates: %w", err)
			}
			fmt.Println("done")

			if !found || autoupdate.IsNewerThanRelease(currentVersion, latest.Version()) {
				fmt.Printf("You are already using the latest version (%s)\n", currentVersion)
				return nil
			}

			fmt.Printf("New version available: %s\n", latest.Version())

			if checkOnly {
				fmt.Println("\nRun 'prx update' to install the new version")
				return nil
			}

			fmt.Print("Downloading and installing... ")
			release, err := selfupdate.UpdateSelf(ctx, currentVersion, slug)
			if err != nil {
				fmt.Println("failed")
				return fmt.Errorf("failed to update: %w", err)
			}
			fmt.Println("done")

			fmt.Printf("\nSuccessfully updated to %s!\n", release.Version())
			autoupdate.ClearPendingUpdate()
			return nil
		},
	}
	updateCmd.Flags().Bool("check", false, "Only check for updates without installing")

	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(updateCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
