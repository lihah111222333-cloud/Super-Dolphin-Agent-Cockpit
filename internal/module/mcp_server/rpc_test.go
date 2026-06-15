package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestAddRPCCreatesMCPServerConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	server := newMCPServerTestServer(newMemoryMCPServerStore())
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
}

func TestAddRPCRejectsMissingMCPServers(t *testing.T) {
	server := newMCPServerTestServer(newMemoryMCPServerStore())
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
	store := newMemoryMCPServerStore()
	configPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})
	server := newMCPServerTestServer(store)
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

func TestToolsRPCReturnsMCPServerTools(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	toolsServer, _ := newToolsListHTTPMCPTestServer(t, "")
	defer toolsServer.Close()
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       toolsServer.URL,
	})
	server := newMCPServerTestServer(store)
	payload, err := json.Marshal(ListServerToolsRequest{ServerName: "my-search"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "mcpServer/tools", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/tools: %v", err)
	}
	var got ListServerToolsResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.ServerName != "my-search" || len(got.Tools) != 1 || got.Tools[0].Name != "remote_search" {
		t.Fatalf("ListServerToolsResult = %#v, want remote_search", got)
	}
}

func TestDeleteRPCRemovesMCPServerConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})
	server := newMCPServerTestServer(store)
	payload, err := json.Marshal(DeleteServerRequest{ServerName: "my-search"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "mcpServer/delete", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/delete: %v", err)
	}
	var got DeleteServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Deleted || got.ServerName != "my-search" {
		t.Fatalf("DeleteServerResult = %#v, want deleted my-search", got)
	}
	if _, ok := store.servers[project]["my-search"]; ok {
		t.Fatalf("server still stored after delete: %#v", store.servers[project])
	}
}

func newMCPServerTestServer(store MCPServerConfigStore) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewServiceWithStore(store)).Handlers)
	return server
}
