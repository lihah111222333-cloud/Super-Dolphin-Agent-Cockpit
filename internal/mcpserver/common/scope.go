package common

import (
	"context"
	"encoding/json"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const ToolScopeContextKey = contextKey("mcp_tool_scope")
const RuntimeWorkspaceScopeFallbackContextKey = contextKey("mcp_runtime_workspace_scope_fallback")

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

// DecodeToolCallParams 解码工具callparams。
func DecodeToolCallParams(raw json.RawMessage) (ToolCallParams, error) {
	var params ToolCallParams
	if err := platformshared.DecodeInput(raw, &params); err != nil {
		return ToolCallParams{}, err
	}
	return params, nil
}

// Scope 处理作用域。
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

// NormalizeToolScope 规范化工具作用域。
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

// WithToolScope 设置工具作用域。
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

// ToolScopeFromContext 从上下文处理工具作用域。
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

// WithRuntimeWorkspaceScopeFallback 设置运行时工作区作用域兜底。
func WithRuntimeWorkspaceScopeFallback(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, RuntimeWorkspaceScopeFallbackContextKey, true)
}

// RuntimeWorkspaceScopeFallbackFromContext 从上下文处理运行时工作区作用域兜底。
func RuntimeWorkspaceScopeFallbackFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(RuntimeWorkspaceScopeFallbackContextKey).(bool)
	return value
}

// isEmpty 判断empty是否可用。
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
	if runtime.GOOS == "windows" && isSlashRootedPOSIXPath(cwd) {
		return pathpkg.Clean(cwd)
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return ""
}

// normalizeWorkspaceRoots 规范化工作区根目录。
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

// normalizeWorkspaceRoot 规范化工作区根目录。
func normalizeWorkspaceRoot(base, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if runtime.GOOS == "windows" && isSlashRootedPOSIXPath(root) {
		return pathpkg.Clean(root)
	}
	if strings.TrimSpace(base) != "" && !filepath.IsAbs(root) {
		root = filepath.Join(base, root)
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return ""
}

func isSlashRootedPOSIXPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !isWindowsDriveAlias(path)
}

func isWindowsDriveAlias(path string) bool {
	return len(path) >= 3 && path[0] == '/' && isWindowsDriveLetter(path[1]) && path[2] == ':'
}

func isWindowsDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
