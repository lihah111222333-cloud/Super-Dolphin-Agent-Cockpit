package toolbridge

import "strings"

// MCPToolNamespace 描述 mcp__server__tool 形式工具名拆出的 server 和 tool。
type MCPToolNamespace struct {
	Server string
	Tool   string
}

// WrapMCPToolName 生成 Codex 动态工具面使用的 MCP 命名空间工具名。
func WrapMCPToolName(server, tool string) string {
	server = strings.TrimSpace(server)
	tool = strings.TrimSpace(tool)
	if server == "" || tool == "" {
		return tool
	}
	return "mcp__" + server + "__" + tool
}

// SplitMCPToolName 解析 mcp__server__tool 工具名；非法或非命名空间名返回 false。
func SplitMCPToolName(name string) (MCPToolNamespace, bool) {
	trimmed := strings.TrimSpace(name)
	if !strings.HasPrefix(trimmed, "mcp__") {
		return MCPToolNamespace{}, false
	}
	rest := strings.TrimPrefix(trimmed, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return MCPToolNamespace{}, false
	}
	return MCPToolNamespace{Server: strings.TrimSpace(parts[0]), Tool: strings.TrimSpace(parts[1])}, true
}
