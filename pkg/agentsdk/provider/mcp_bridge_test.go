package provider

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGenerateConfig_OmitsAgentIDWhenEmpty(t *testing.T) {
	bridge := &MCPBridge{
		ServerCommand: "/tmp/go-agent-mcp-server",
		APIServerURL:  "http://127.0.0.1:8080",
		ToolNames:     []string{"tool.alpha"},
		ToolsJSON:     `{"tools":[]}`,
	}
	path, err := bridge.GenerateConfig("/tmp/work")
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	defer os.Remove(path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var cfg MCPConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	env := cfg.MCPServers["go-agent-tools"].Env
	if _, ok := env["GO_AGENT_MCP_AGENT_ID"]; ok {
		t.Fatalf("env = %#v, want no GO_AGENT_MCP_AGENT_ID", env)
	}
}
