package main

import (
	"fmt"
	"os"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/buildinfo"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/mcp"
	"github.com/sleuth-io/prx/internal/skills"
	"github.com/sleuth-io/prx/internal/tui"
)

func main() {
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

	rootCmd.AddCommand(mcpCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
