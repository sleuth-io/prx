package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/buildinfo"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/mcp"
	"github.com/sleuth-io/prx/internal/tui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp-server" {
		if err := logger.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not init log: %v\n", err)
		}
		mcp.ParseAndRun(os.Args[2:])
		return
	}

	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("prx %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return
	}

	if err := logger.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init log: %v\n", err)
	}

	if err := app.CheckDeps(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	repoDir := "."
	if len(os.Args) > 1 {
		repoDir = os.Args[1]
	}

	a, err := app.New(repoDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	m := tui.New(a)
	p := tea.NewProgram(m, tea.WithAltScreen())
	go func() {
		p.Send(tui.SetProgramMsg{Program: p})
	}()
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
