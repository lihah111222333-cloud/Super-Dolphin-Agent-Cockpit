package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MCPBridge generates MCP configuration for tool injection.
// Extracted from go-agent-v2 pkg/agentsdk/claude/client_cli_transport.go.
// Now shared by ALL providers (Claude, Codex, future).
type MCPBridge struct {
	AgentID       string
	ServerCommand string // Path to go-agent-mcp-server binary
	APIServerURL  string
	ToolNames     []string
	ToolGroups    map[string][]string
	ToolsJSON     string // Serialized tool schemas
}

// MCPServerConfig represents a single MCP server entry.
type MCPServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env"`
	CWD         string            `json:"cwd,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
}

// MCPConfig is the top-level MCP configuration.
type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// GenerateConfig writes a temporary MCP config file and returns its path.
// This replaces the provider-specific buildDynamicToolsMCPConfig() from V2.
func (b *MCPBridge) GenerateConfig(cwd string) (string, error) {
	env := map[string]string{
		"GO_AGENT_DYNAMIC_TOOLS_JSON":  b.ToolsJSON,
		"GO_AGENT_DYNAMIC_TOOL_NAMES":  joinNames(b.ToolNames),
		"GO_AGENT_MCP_AGENT_ID":        b.AgentID,
		"AGENT_APISERVER_URL":           b.APIServerURL,
	}

	cfg := MCPConfig{
		MCPServers: map[string]MCPServerConfig{
			"go-agent-tools": {
				Command:     b.ServerCommand,
				Args:        []string{"bridge"},
				Env:         env,
				CWD:         cwd,
				AutoApprove: b.ToolNames,
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}

	tmpFile := filepath.Join(os.TempDir(), "mcp-config-"+b.AgentID+".json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return "", err
	}

	return tmpFile, nil
}

func joinNames(names []string) string {
	result := ""
	for i, n := range names {
		if i > 0 {
			result += ","
		}
		result += n
	}
	return result
}
