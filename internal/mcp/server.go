package mcp

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

// Server is an MCP JSON-RPC server over stdin/stdout.
type Server struct {
	repo       string
	prNumber   int
	socketPath string
}

// New creates a new MCP server.
func New(repo string, prNumber int, socketPath string) *Server {
	return &Server{repo: repo, prNumber: prNumber, socketPath: socketPath}
}

// ParseAndRun parses flags from args (e.g. os.Args[2:]) and runs the server.
func ParseAndRun(args []string) {
	fs := flag.NewFlagSet("mcp-server", flag.ExitOnError)
	socket := fs.String("socket", "", "Unix socket path for permission requests")
	repo := fs.String("repo", "", "GitHub repo (owner/name)")
	pr := fs.String("pr", "", "PR number")
	_ = fs.Parse(args)

	prNumber, _ := strconv.Atoi(*pr)
	New(*repo, prNumber, *socket).Run()
}

// JSON-RPC 2.0 types
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolMeta describes the permission and side-effect properties of each tool.
// requiresPermission: user must approve before execution.
// isMutation: the action changes GitHub state → TUI should refresh the PR after success.
type toolMeta struct {
	requiresPermission bool
	isMutation         bool
}

var toolMetas = map[string]toolMeta{
	"approve_pr":      {requiresPermission: true, isMutation: true},
	"request_changes": {requiresPermission: true, isMutation: true},
	"comment_on_pr":   {requiresPermission: true, isMutation: true},
	"merge_pr":        {requiresPermission: true, isMutation: true},
	// future read-only tools: requiresPermission: false, isMutation: false
}

var toolDefs = []map[string]interface{}{
	{
		"name":        "approve_pr",
		"description": "Approve the pull request",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		"name":        "request_changes",
		"description": "Request changes on the pull request",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"body": map[string]interface{}{
					"type":        "string",
					"description": "The review body describing requested changes",
				},
			},
			"required": []string{"body"},
		},
	},
	{
		"name":        "comment_on_pr",
		"description": "Post a comment on the pull request",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"body": map[string]interface{}{
					"type":        "string",
					"description": "The comment body",
				},
			},
			"required": []string{"body"},
		},
	},
	{
		"name":        "merge_pr",
		"description": "Merge the pull request",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
}

// Run reads JSON-RPC requests from stdin and writes responses to stdout.
func (s *Server) Run() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			logger.Error("mcp: parse request: %v", err)
			continue
		}
		// Notifications have no ID; just ignore them.
		if req.ID == nil {
			continue
		}
		resp := s.handle(req)
		if err := enc.Encode(resp); err != nil {
			logger.Error("mcp: encode response: %v", err)
		}
	}
}

func (s *Server) handle(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "prx",
					"version": "1.0.0",
				},
			},
		}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"tools": toolDefs},
		}
	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if req.Params != nil {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return errResp(req.ID, -32602, "invalid params")
			}
		}
		result, callErr := s.callTool(params.Name, params.Arguments)
		if callErr != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": "Error: " + callErr.Error()},
					},
					"isError": true,
				},
			}
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": result},
				},
			},
		}
	default:
		return errResp(req.ID, -32601, "method not found")
	}
}

func (s *Server) callTool(name string, args map[string]interface{}) (string, error) {
	meta, ok := toolMetas[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	if meta.requiresPermission {
		description := s.toolDescription(name, args)
		if !s.requestPermission(description) {
			return "", fmt.Errorf("action denied by user")
		}
	}

	result, err := s.executeAction(name, args)
	if err != nil {
		return "", err
	}

	if meta.isMutation {
		s.notifyRefresh()
	}
	return result, nil
}

func (s *Server) toolDescription(name string, args map[string]interface{}) string {
	switch name {
	case "approve_pr":
		return fmt.Sprintf("Approve PR #%d", s.prNumber)
	case "request_changes":
		body, _ := args["body"].(string)
		return fmt.Sprintf("Request changes on PR #%d: %s", s.prNumber, truncate(body, 100))
	case "comment_on_pr":
		body, _ := args["body"].(string)
		return fmt.Sprintf("Post comment on PR #%d: %s", s.prNumber, truncate(body, 100))
	case "merge_pr":
		return fmt.Sprintf("Merge PR #%d", s.prNumber)
	default:
		return name
	}
}

func (s *Server) executeAction(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "approve_pr":
		if err := github.ApprovePR(s.repo, s.prNumber); err != nil {
			return "", err
		}
		return "PR approved successfully", nil
	case "request_changes":
		body, _ := args["body"].(string)
		if err := github.RequestChanges(s.repo, s.prNumber, body); err != nil {
			return "", err
		}
		return "Changes requested successfully", nil
	case "comment_on_pr":
		body, _ := args["body"].(string)
		if err := github.PostComment(s.repo, s.prNumber, body); err != nil {
			return "", err
		}
		return "Comment posted successfully", nil
	case "merge_pr":
		if err := github.MergePR(s.repo, s.prNumber); err != nil {
			return "", err
		}
		return "PR merged successfully", nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// notifyRefresh signals the TUI to re-fetch this PR's data.
func (s *Server) notifyRefresh() {
	if s.socketPath == "" {
		return
	}
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		logger.Error("mcp: notify refresh: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_ = json.NewEncoder(conn).Encode(map[string]interface{}{
		"type":      "refresh",
		"pr_number": s.prNumber,
	})
}

func (s *Server) requestPermission(description string) bool {
	if s.socketPath == "" {
		return false
	}
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		logger.Error("mcp: connect to perm socket: %v", err)
		return false
	}
	defer func() { _ = conn.Close() }()

	req := map[string]string{"type": "permission", "description": description}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		logger.Error("mcp: send perm request: %v", err)
		return false
	}
	var resp map[string]bool
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		logger.Error("mcp: read perm response: %v", err)
		return false
	}
	return resp["allowed"]
}

func errResp(id interface{}, code int, msg string) rpcResponse {
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
