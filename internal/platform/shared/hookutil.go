package shared

import (
	"strings"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// NormalizeSelectorScope 清理 hook selector scope 中的空白字段，nil scope 返回零值。
func NormalizeSelectorScope(scope *mcp.SelectorScope) mcp.SelectorScope {
	if scope == nil {
		return mcp.SelectorScope{}
	}
	return mcp.SelectorScope{
		AgentID:    strings.TrimSpace(scope.AgentID),
		ThreadID:   strings.TrimSpace(scope.ThreadID),
		ClientKind: strings.TrimSpace(scope.ClientKind),
		InstanceID: strings.TrimSpace(scope.InstanceID),
	}
}
