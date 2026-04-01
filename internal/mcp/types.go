package mcp

import "encoding/json"

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
	"approve_pr":          {requiresPermission: true, isMutation: true},
	"request_changes":     {requiresPermission: true, isMutation: true},
	"comment_on_pr":       {requiresPermission: true, isMutation: true},
	"skip_pr":             {requiresPermission: false, isMutation: false},
	"merge_pr":            {requiresPermission: true, isMutation: true},
	"get_config":          {requiresPermission: false, isMutation: false},
	"set_model":           {requiresPermission: true, isMutation: false},
	"set_merge_method":    {requiresPermission: true, isMutation: false},
	"set_criterion":       {requiresPermission: true, isMutation: false},
	"remove_criterion":    {requiresPermission: true, isMutation: false},
	"set_thresholds":      {requiresPermission: true, isMutation: false},
	"activate_skill":      {requiresPermission: false, isMutation: false},
	"read_skill_resource": {requiresPermission: false, isMutation: false},
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
