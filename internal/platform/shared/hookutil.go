package shared

import (
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// NormalizeSelectorScope 规范化selector作用域。
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
