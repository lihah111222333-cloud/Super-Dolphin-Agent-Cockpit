package common

import (
	"context"
	"encoding/json"
	"errors"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// ToolScopeContextKey 保存 tools/call 顶层 metadata 归一化后的可信 scope。
const ToolScopeContextKey = contextKey("mcp_tool_scope")

// RuntimeWorkspaceScopeFallbackContextKey 标记运行时可使用 workspace scope fallback。
const RuntimeWorkspaceScopeFallbackContextKey = contextKey("mcp_runtime_workspace_scope_fallback")

// ToolScope 是每次 tools/call 顶层 metadata 携带的可信作用域。
// 工具 arguments 不参与作用域提取，避免模型传入的 agent_id/cwd 覆盖服务端拥有的边界。
type ToolScope struct {
	AgentID        string
	ThreadID       string
	TurnID         string
	CallID         string
	CWD            string
	WorkspaceRoots []string
	Family         string
}

// ToolCallParams 是 stdio/control-plane 共用的 tools/call payload。
// 私有 metadata key 是 peer 线协议的一部分，必须保留下划线形式，不能改成公开参数字段。
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Meta 接收 MCP 标准保留元数据，但不参与可信 scope 提取。
	Meta         json.RawMessage `json:"_meta,omitempty"`
	MetaAgentID  string          `json:"_agentId,omitempty"`
	MetaThreadID string          `json:"_threadId,omitempty"`
	MetaCallID   string          `json:"_callId,omitempty"`
	MetaCWD      string          `json:"_cwd,omitempty"`
	// Both spellings are accepted as private top-level metadata. Tool
	// arguments with these names are intentionally ignored by this decoder.
	MetaWorkspaceRoots      []string `json:"_workspaceRoots,omitempty"`
	MetaWorkspaceRootsSnake []string `json:"_workspace_roots,omitempty"`
}

// DecodeToolCallParams 解码 tools/call 顶层参数，并在进入 provider 前校验工具名。
func DecodeToolCallParams(raw json.RawMessage) (ToolCallParams, error) {
	var params ToolCallParams
	if err := platformshared.DecodeInput(raw, &params); err != nil {
		return ToolCallParams{}, err
	}
	if strings.TrimSpace(params.Name) == "" {
		return ToolCallParams{}, errors.New("tool name is required")
	}
	return params, nil
}

// Scope 从 tools/call 顶层私有 metadata 生成可信 ToolScope。
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

// ToolScopeFromContext 从 context 读取并重新归一化 ToolScope，空 scope 视为不存在。
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

// RuntimeWorkspaceScopeFallbackFromContext 读取运行时 workspace fallback 标记。
func RuntimeWorkspaceScopeFallbackFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(RuntimeWorkspaceScopeFallbackContextKey).(bool)
	return value
}

// isEmpty 判断 scope 是否没有任何可信调用边界字段。
func (scope ToolScope) isEmpty() bool {
	return scope.AgentID == "" &&
		scope.ThreadID == "" &&
		scope.TurnID == "" &&
		scope.CallID == "" &&
		scope.CWD == "" &&
		len(scope.WorkspaceRoots) == 0 &&
		scope.Family == ""
}

// normalizeScopeFamily 将历史 MCP peer 名称收敛为稳定 family，供日志和授权判断使用。
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

// normalizeScopeCWD 只接受绝对路径；Windows 上保留 POSIX 风格根路径给跨平台 peer 使用。
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

// isSlashRootedPOSIXPath 判断路径是否是 POSIX 绝对路径，排除 Windows drive alias。
func isSlashRootedPOSIXPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !isWindowsDriveAlias(path)
}

// isWindowsDriveAlias 判断 /C: 这类 Windows drive alias，避免误当成 POSIX root。
func isWindowsDriveAlias(path string) bool {
	return len(path) >= 3 && path[0] == '/' && isWindowsDriveLetter(path[1]) && path[2] == ':'
}

// isWindowsDriveLetter 判断字节是否为 ASCII drive letter。
func isWindowsDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
