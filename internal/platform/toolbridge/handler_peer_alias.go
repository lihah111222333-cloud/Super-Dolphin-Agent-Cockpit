package toolbridge

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// legacyLSPName 返回 LSP 工具短名对应的旧版 lsp_* 名称，供兼容 surface 暴露。
func legacyLSPName(canonical string) string {
	for legacy, short := range legacyLSPToolAliases {
		if short == canonical {
			return legacy
		}
	}
	return ""
}

// legacyOrchName 返回 orchestration 短名对应的旧版 orchestration_* 名称。
func legacyOrchName(canonical string) string {
	switch strings.TrimSpace(canonical) {
	case "launch_agent":
		return "orchestration_launch_agent"
	case "send_message":
		return "orchestration_send_message"
	case "stop_agent":
		return "orchestration_stop_agent"
	case "recover_agent":
		return "orchestration_recover_agent"
	case "interrupt_agent":
		return "orchestration_interrupt_agent"
	case "list_agents":
		return "orchestration_list_agents"
	case "get_agent_report":
		return "orchestration_get_agent_report"
	case "get_agent_reports":
		return "orchestration_get_agent_reports"
	default:
		return ""
	}
}

// codexSurfaceMCPToolEntry 保存 Codex 暴露名到真实 MCP 工具和 lifecycle key 的映射。
// lifecycle key 固定为 tools/list 原始 server/tool，别名直呼不能改变执行前检查对象。
func codexSurfaceMCPToolEntry(
	surface *codexToolSurface,
	family string,
	canonical string,
	toolName string,
	client mcpClient,
) codexToolEntry {
	serverName := strings.TrimSpace(family)
	realName := strings.TrimSpace(toolName)
	return codexToolEntry{
		name:          canonical,
		realName:      toolName,
		executionKind: "stdio",
		family:        serverName,
		lifecycleKey: contract.MCPToolLifecycleKey{
			WorkspaceRoot: surface.cwd,
			ServerName:    serverName,
			ToolName:      realName,
		},
		client: client,
	}
}

// addCodexSurfaceMCPAliases 把短名、legacy 名和 mcp__server__tool 命名空间名都绑定到同一 canonical entry。
func addCodexSurfaceMCPAliases(surface *codexToolSurface, family, toolName, canonical string) error {
	if err := addMCPToolAlias(surface, family, toolName, canonical); err != nil {
		return err
	}
	for _, alias := range []string{
		WrapMCPToolName(family, toolName),
		WrapMCPToolName(family, canonicalCodexToolName(family, toolName)),
	} {
		if err := addSurfaceAlias(surface, alias, canonical); err != nil {
			return err
		}
	}
	for _, alias := range legacyCodexToolAliases(family, canonical) {
		if err := addSurfaceAlias(surface, alias, canonical); err != nil {
			return err
		}
	}
	return nil
}
