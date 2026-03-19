package mcp

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/sleuth-io/prx/internal/config"
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
	"approve_pr":       {requiresPermission: true, isMutation: true},
	"request_changes":  {requiresPermission: true, isMutation: true},
	"comment_on_pr":    {requiresPermission: true, isMutation: true},
	"merge_pr":         {requiresPermission: true, isMutation: true},
	"get_config":       {requiresPermission: false, isMutation: false},
	"set_model":        {requiresPermission: true, isMutation: false},
	"set_merge_method": {requiresPermission: true, isMutation: false},
	"set_criterion":    {requiresPermission: true, isMutation: false},
	"remove_criterion": {requiresPermission: true, isMutation: false},
	"set_thresholds":   {requiresPermission: true, isMutation: false},
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
	{
		"name":        "get_config",
		"description": "Get the current prx configuration: model, scoring criteria, and thresholds",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		"name":        "set_model",
		"description": "Change the Claude model used for PR scoring",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"model": map[string]interface{}{
					"type":        "string",
					"description": "Model name (e.g. 'sonnet', 'opus', 'haiku')",
				},
			},
			"required": []string{"model"},
		},
	},
	{
		"name":        "set_merge_method",
		"description": "Change the merge method used when merging PRs",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"method": map[string]interface{}{
					"type":        "string",
					"description": "Merge method: 'merge' (merge commit), 'squash' (squash and merge), or 'rebase' (rebase and merge)",
				},
			},
			"required": []string{"method"},
		},
	},
	{
		"name":        "set_criterion",
		"description": "Add or update a scoring criterion",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Unique identifier for the criterion (e.g. 'blast_radius')",
				},
				"label": map[string]interface{}{
					"type":        "string",
					"description": "Short display label (e.g. 'Blast')",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Detailed description used in the AI prompt",
				},
				"weight": map[string]interface{}{
					"type":        "number",
					"description": "Weighting factor (must be > 0)",
				},
			},
			"required": []string{"name", "label", "description", "weight"},
		},
	},
	{
		"name":        "remove_criterion",
		"description": "Remove a scoring criterion by name",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the criterion to remove",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		"name":        "set_thresholds",
		"description": "Update the approve_below and review_above score thresholds",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"approve_below": map[string]interface{}{
					"type":        "number",
					"description": "PRs scoring below this are auto-approved (1.0–5.0)",
				},
				"review_above": map[string]interface{}{
					"type":        "number",
					"description": "PRs scoring above this require careful review (1.0–5.0)",
				},
			},
			"required": []string{"approve_below", "review_above"},
		},
	},
}

// ToolNames returns the MCP-prefixed names of all registered tools.
// Commands can use this to build --allowedTools lists without hardcoding names.
func ToolNames() []string {
	names := make([]string, 0, len(toolMetas))
	for name := range toolMetas {
		names = append(names, "mcp__prx__"+name)
	}
	return names
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
	case "set_model":
		model, _ := args["model"].(string)
		return fmt.Sprintf("Change scoring model to %q", model)
	case "set_merge_method":
		method, _ := args["method"].(string)
		return fmt.Sprintf("Change merge method to %q", method)
	case "set_criterion":
		n, _ := args["name"].(string)
		label, _ := args["label"].(string)
		desc, _ := args["description"].(string)
		weight, _ := args["weight"].(float64)
		return fmt.Sprintf("Add/update criterion %q: label=%q, weight=%.1f, description=%q", n, label, weight, desc)
	case "remove_criterion":
		n, _ := args["name"].(string)
		return fmt.Sprintf("Remove scoring criterion %q", n)
	case "set_thresholds":
		ab, _ := args["approve_below"].(float64)
		ra, _ := args["review_above"].(float64)
		return fmt.Sprintf("Set thresholds: approve_below=%.1f, review_above=%.1f", ab, ra)
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
		cfg, err := config.Load()
		if err != nil {
			return "", fmt.Errorf("loading config: %w", err)
		}
		if err := github.MergePR(s.repo, s.prNumber, cfg.Review.MergeMethod); err != nil {
			return "", err
		}
		return "PR merged successfully", nil
	case "get_config", "set_model", "set_merge_method", "set_criterion", "remove_criterion", "set_thresholds":
		return s.executeConfigAction(name, args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) executeConfigAction(name string, args map[string]interface{}) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}

	if name == "get_config" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Model: %s\nMerge method: %s\n\nCriteria:\n", cfg.Review.Model, cfg.Review.MergeMethod)
		for _, c := range cfg.Criteria {
			fmt.Fprintf(&sb, "  - %s (%s): weight=%.1f\n    %s\n", c.Name, c.Label, c.Weight, c.Description)
		}
		fmt.Fprintf(&sb, "\nThresholds:\n  approve_below: %.1f\n  review_above:  %.1f\n", cfg.Thresholds.ApproveBelow, cfg.Thresholds.ReviewAbove)
		return sb.String(), nil
	}

	switch name {
	case "set_model":
		model, _ := args["model"].(string)
		if model == "" {
			return "", fmt.Errorf("model must be a non-empty string")
		}
		cfg.Review.Model = model

	case "set_merge_method":
		method, _ := args["method"].(string)
		switch method {
		case "merge", "squash", "rebase":
		default:
			return "", fmt.Errorf("merge method must be 'merge', 'squash', or 'rebase'")
		}
		cfg.Review.MergeMethod = method

	case "set_criterion":
		n, _ := args["name"].(string)
		label, _ := args["label"].(string)
		desc, _ := args["description"].(string)
		weight, _ := args["weight"].(float64)
		if n == "" {
			return "", fmt.Errorf("name must be non-empty")
		}
		if weight <= 0 {
			return "", fmt.Errorf("weight must be > 0")
		}
		updated := false
		for i, c := range cfg.Criteria {
			if c.Name == n {
				cfg.Criteria[i] = config.Criterion{Name: n, Label: label, Description: desc, Weight: weight}
				updated = true
				break
			}
		}
		if !updated {
			cfg.Criteria = append(cfg.Criteria, config.Criterion{Name: n, Label: label, Description: desc, Weight: weight})
		}

	case "remove_criterion":
		n, _ := args["name"].(string)
		if n == "" {
			return "", fmt.Errorf("name must be non-empty")
		}
		var newCriteria []config.Criterion
		for _, c := range cfg.Criteria {
			if c.Name != n {
				newCriteria = append(newCriteria, c)
			}
		}
		if len(newCriteria) == 0 {
			return "", fmt.Errorf("cannot remove the last criterion")
		}
		cfg.Criteria = newCriteria

	case "set_thresholds":
		ab, _ := args["approve_below"].(float64)
		ra, _ := args["review_above"].(float64)
		if ab < 1.0 || ab > 5.0 {
			return "", fmt.Errorf("approve_below must be between 1.0 and 5.0")
		}
		if ra < 1.0 || ra > 5.0 {
			return "", fmt.Errorf("review_above must be between 1.0 and 5.0")
		}
		if ab >= ra {
			return "", fmt.Errorf("approve_below (%.1f) must be less than review_above (%.1f)", ab, ra)
		}
		cfg.Thresholds.ApproveBelow = ab
		cfg.Thresholds.ReviewAbove = ra
	}

	if err := config.Save(cfg); err != nil {
		return "", fmt.Errorf("saving config: %w", err)
	}
	s.notifyConfigReload()
	return fmt.Sprintf("%s completed successfully", name), nil
}

// notifyConfigReload signals the TUI to reload config from disk.
func (s *Server) notifyConfigReload() {
	if s.socketPath == "" {
		return
	}
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		logger.Error("mcp: notify config reload: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_ = json.NewEncoder(conn).Encode(map[string]interface{}{
		"type": "config_reload",
	})
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
