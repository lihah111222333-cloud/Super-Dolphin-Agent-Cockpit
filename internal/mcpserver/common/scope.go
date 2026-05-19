package common

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const ToolScopeContextKey = contextKey("mcp_tool_scope")

// ToolScope is the trusted per-call scope carried on top-level tools/call
// metadata. Tool arguments are intentionally excluded so model-provided
// agent_id/cwd fields cannot override this server-owned scope.
type ToolScope struct {
	AgentID        string
	ThreadID       string
	TurnID         string
	CallID         string
	CWD            string
	WorkspaceRoots []string
	Family         string
}

// ToolCallParams is the shared stdio/control-plane tools/call payload. Private
// metadata keys are part of the peer wire contract; keep the leading
// underscores and do not replace them with public argument fields.
type ToolCallParams struct {
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	MetaAgentID  string          `json:"_agentId,omitempty"`
	MetaThreadID string          `json:"_threadId,omitempty"`
	MetaCallID   string          `json:"_callId,omitempty"`
	MetaCWD      string          `json:"_cwd,omitempty"`
	// Both spellings are accepted as private top-level metadata. Tool
	// arguments with these names are intentionally ignored by this decoder.
	MetaWorkspaceRoots      []string `json:"_workspaceRoots,omitempty"`
	MetaWorkspaceRootsSnake []string `json:"_workspace_roots,omitempty"`
}

func DecodeToolCallParams(raw json.RawMessage) (ToolCallParams, error) {
	var params ToolCallParams
	if err := platformshared.DecodeInput(raw, &params); err != nil {
		return ToolCallParams{}, err
	}
	return params, nil
}

func (p ToolCallParams) Scope(family string) ToolScope {
	return NormalizeToolScope(ToolScope{
		AgentID:  p.MetaAgentID,
		ThreadID: p.MetaThreadID,
		CallID:   p.MetaCallID,
		CWD:      p.MetaCWD,
		WorkspaceRoots: append(
			append([]string(nil), p.MetaWorkspaceRoots...),
			p.MetaWorkspaceRootsSnake...,
		),
		Family: family,
	})
}

func NormalizeToolScope(scope ToolScope) ToolScope {
	scope.AgentID = strings.TrimSpace(scope.AgentID)
	scope.ThreadID = strings.TrimSpace(scope.ThreadID)
	scope.TurnID = strings.TrimSpace(scope.TurnID)
	scope.CallID = strings.TrimSpace(scope.CallID)
	scope.CWD = normalizeScopeCWD(scope.CWD)
	scope.WorkspaceRoots = normalizeWorkspaceRoots(scope.CWD, scope.WorkspaceRoots)
	scope.Family = normalizeScopeFamily(scope.Family)
	return scope
}

func WithToolScope(ctx context.Context, scope ToolScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	scope = NormalizeToolScope(scope)
	if scope.isEmpty() {
		return ctx
	}
	ctx = context.WithValue(ctx, ToolScopeContextKey, scope)
	if scope.CWD != "" {
		ctx = context.WithValue(ctx, CwdContextKey, scope.CWD)
	}
	return ctx
}

func ToolScopeFromContext(ctx context.Context) (ToolScope, bool) {
	if ctx == nil {
		return ToolScope{}, false
	}
	scope, ok := ctx.Value(ToolScopeContextKey).(ToolScope)
	if !ok {
		return ToolScope{}, false
	}
	scope = NormalizeToolScope(scope)
	return scope, !scope.isEmpty()
}

func (scope ToolScope) isEmpty() bool {
	return scope.AgentID == "" &&
		scope.ThreadID == "" &&
		scope.TurnID == "" &&
		scope.CallID == "" &&
		scope.CWD == "" &&
		len(scope.WorkspaceRoots) == 0 &&
		scope.Family == ""
}

func normalizeScopeFamily(family string) string {
	trimmed := strings.TrimSpace(family)
	switch strings.ToLower(trimmed) {
	case "mcp-lsp":
		return "lsp"
	case "mcp-orch":
		return "orch"
	case "mcp-ida":
		return "ida"
	default:
		return trimmed
	}
}

func normalizeScopeCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return ""
}

func normalizeWorkspaceRoots(cwd string, roots []string) []string {
	out := make([]string, 0, len(roots)+1)
	seen := map[string]struct{}{}
	add := func(base, path string) {
		path = normalizeWorkspaceRoot(base, path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	primary := normalizeWorkspaceRoot("", cwd)
	if primary == "" {
		return nil
	}
	add("", primary)
	for _, root := range roots {
		add(primary, root)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeWorkspaceRoot(base, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if strings.TrimSpace(base) != "" && !filepath.IsAbs(root) {
		root = filepath.Join(base, root)
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return ""
}
