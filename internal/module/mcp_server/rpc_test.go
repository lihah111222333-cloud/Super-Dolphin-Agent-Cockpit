package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestAddRPCCreatesMCPServerConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	server := newMCPServerTestServer()
	payload, err := json.Marshal(AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"my-search": {
				Transport: "http",
				URL:       "https://your-domain.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer YOUR_API_KEY",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "mcpServer/add", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/add: %v", err)
	}
	var got AddServersResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.ConfigPath != filepath.Join(project, ".agent", "mcp_server", "config.json") {
		t.Fatalf("ConfigPath = %q", got.ConfigPath)
	}
	if _, err := os.Stat(got.ConfigPath); err != nil {
		t.Fatalf("stat config path: %v", err)
	}
}

func TestAddRPCRejectsMissingMCPServers(t *testing.T) {
	server := newMCPServerTestServer()
	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "mcpServer/add", payload); err == nil {
		t.Fatalf("Dispatch mcpServer/add accepted missing mcpServers")
	}
}

func TestListRPCReturnsMCPServerConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	configPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	existing := []byte(`{
  "mcpServers": {
    "my-search": {
      "transport": "http",
      "url": "https://your-domain.com/mcp"
    }
  }
}`)
	if err := os.WriteFile(configPath, existing, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := newMCPServerTestServer()
	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "mcpServer/list", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/list: %v", err)
	}
	var got ListServersResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, configPath)
	}
	if got.MCPServers["my-search"].URL != "https://your-domain.com/mcp" {
		t.Fatalf("mcpServers = %#v", got.MCPServers)
	}
}

func newMCPServerTestServer() *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService()).Handlers)
	return server
}
