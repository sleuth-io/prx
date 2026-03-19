// Package perm bridges the MCP server and the TUI via a Unix socket.
// The MCP server connects to the socket to request user permission before
// executing an action; the TUI socket listener receives requests, fires a
// Msg into the Bubble Tea program, and blocks until the user responds.
// After a successful mutation, the MCP server sends a RefreshMsg so the TUI
// can re-fetch the PR's updated data.
package perm

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sleuth-io/prx/internal/logger"
)

// SocketPath returns the Unix socket path for the current process.
func SocketPath() string {
	return fmt.Sprintf("/tmp/prx-perm-%d.sock", os.Getpid())
}

// Msg is sent to the TUI when a permission request arrives from the MCP server.
type Msg struct {
	Description string
	Respond     func(allowed bool)
}

// RefreshMsg is sent to the TUI when a mutating action completed successfully
// and the PR data should be re-fetched.
type RefreshMsg struct {
	PRNumber int
}

// Listen starts the Unix socket listener and returns a cleanup function.
// It sends a perm.Msg or perm.RefreshMsg to program for each incoming connection.
func Listen(program *tea.Program) (socketPath string, cleanup func(), err error) {
	path := SocketPath()
	_ = os.Remove(path) // clean up any stale socket

	ln, err := net.Listen("unix", path)
	if err != nil {
		return "", nil, fmt.Errorf("listen on %s: %w", path, err)
	}

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return // listener was closed
			}
			go handleConn(conn, program)
		}
	}()

	cleanup = func() {
		_ = ln.Close()
		_ = os.Remove(path)
	}
	return path, cleanup, nil
}

func handleConn(conn net.Conn, program *tea.Program) {
	defer func() { _ = conn.Close() }()

	var req struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		PRNumber    int    `json:"pr_number"`
	}
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		logger.Error("perm: decode request: %v", err)
		return
	}

	switch req.Type {
	case "permission":
		ch := make(chan bool, 1)
		program.Send(Msg{
			Description: req.Description,
			Respond:     func(allowed bool) { ch <- allowed },
		})
		allowed := <-ch
		if err := json.NewEncoder(conn).Encode(map[string]bool{"allowed": allowed}); err != nil {
			logger.Error("perm: encode response: %v", err)
		}
	case "refresh":
		program.Send(RefreshMsg{PRNumber: req.PRNumber})
	default:
		logger.Error("perm: unknown request type: %q", req.Type)
	}
}
