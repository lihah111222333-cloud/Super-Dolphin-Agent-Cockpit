package mcpcontrol

import (
	"path/filepath"
	"strings"
)

// ToolScope 是从 tools/call 元数据推导的可信控制面路由 scope。
// 它有意不包含 session 维度；路由只看 clientKind/family 与 agentID/threadID。
type ToolScope struct {
	AgentID  string
	ThreadID string
	TurnID   string
	CallID   string
	CWD      string
	Family   string
}

// normalizeToolScope 清理路由字段，并只接受绝对 cwd 参与后续匹配。
func normalizeToolScope(scope ToolScope) ToolScope {
	scope.AgentID = strings.TrimSpace(scope.AgentID)
	scope.ThreadID = strings.TrimSpace(scope.ThreadID)
	scope.TurnID = strings.TrimSpace(scope.TurnID)
	scope.CallID = strings.TrimSpace(scope.CallID)
	scope.CWD = normalizeScopeCWD(scope.CWD)
	scope.Family = strings.TrimSpace(scope.Family)
	return scope
}

// normalizeScopeCWD 返回清理后的绝对路径；相对路径不会进入可信路由 scope。
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
