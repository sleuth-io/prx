package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/skills"
)

// Server is an MCP JSON-RPC server over stdin/stdout.
type Server struct {
	repo       string
	prNumber   int
	commitSHA  string
	socketPath string
	skills     []skills.Skill
}

// New creates a new MCP server.
func New(repo string, prNumber int, commitSHA, socketPath string, sk []skills.Skill) *Server {
	return &Server{repo: repo, prNumber: prNumber, commitSHA: commitSHA, socketPath: socketPath, skills: sk}
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
			Result:  map[string]interface{}{"tools": s.allToolDefs()},
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

// notifySkip signals the TUI to reload its skip store and update visibility.
func (s *Server) notifySkip() {
	if s.socketPath == "" {
		return
	}
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		logger.Error("mcp: notify skip: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_ = json.NewEncoder(conn).Encode(map[string]interface{}{
		"type":      "skip",
		"pr_number": s.prNumber,
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
