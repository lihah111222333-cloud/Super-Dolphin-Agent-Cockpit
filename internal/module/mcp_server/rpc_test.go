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
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	server := newMCPServerTestServerWithHTTPClient(store, &scriptedMCPHTTPDoer{t: t})
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

func TestStartPostgresRPCCreatesDefaultStdioConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	server := newMCPServerTestServer(store)

	raw, err := server.Dispatch(context.Background(), "mcpServer/postgres/start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch mcpServer/postgres/start: %v", err)
	}
	var got StartPostgresServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || got.Config.Command != "mcp-server-postgres" {
		t.Fatalf("StartPostgresServerResult = %#v, want added mcp-server-postgres config", got)
	}
	if store.servers[project][DefaultPostgresServerName].Command != "mcp-server-postgres" {
		t.Fatalf("stored servers = %#v, want postgres mcp-server-postgres config", store.servers[project])
	}
}

func TestStartSQLiteRPCCreatesDefaultNPXConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	dbPath := filepath.Join(project, "super-dolphin.db")
	store := newMemoryMCPServerStore()
	server := newMCPServerTestServerWithSQLitePath(store, dbPath)

	raw, err := server.Dispatch(context.Background(), "mcpServer/sqlite/start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch mcpServer/sqlite/start: %v", err)
	}
	var got StartSQLiteServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || !got.Enabled || got.Config.Command != "npx" {
		t.Fatalf("StartSQLiteServerResult = %#v, want added enabled npx sqlite config", got)
	}
	if store.servers[project][DefaultSQLiteServerName].Command != "npx" {
		t.Fatalf("stored servers = %#v, want sqlite npx config", store.servers[project])
	}
}

func TestStopSQLiteRPCDisablesDefaultConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	dbPath := filepath.Join(project, "super-dolphin.db")
	store := newMemoryMCPServerStore()
	store.seed(project, DefaultSQLiteServerName, defaultSQLiteServerConfig(dbPath))
	server := newMCPServerTestServerWithSQLitePath(store, dbPath)

	raw, err := server.Dispatch(context.Background(), "mcpServer/sqlite/stop", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch mcpServer/sqlite/stop: %v", err)
	}
	var got StopSQLiteServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Enabled {
		t.Fatalf("StopSQLiteServerResult = %#v, want disabled", got)
	}
}

func TestStartPlaywrightRPCCreatesDefaultNPXConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	server := newMCPServerTestServer(store)

	raw, err := server.Dispatch(context.Background(), "mcpServer/playwright/start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch mcpServer/playwright/start: %v", err)
	}
	var got StartPlaywrightServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || !got.Enabled || got.Config.Command != "npx" {
		t.Fatalf("StartPlaywrightServerResult = %#v, want added enabled npx playwright config", got)
	}
	if args := store.servers[project][DefaultPlaywrightServerName].Args; len(args) != 1 || args[0] != "@playwright/mcp@latest" {
		t.Fatalf("stored servers = %#v, want playwright npx config", store.servers[project])
	}
}

func TestStopPlaywrightRPCDisablesDefaultConfig(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	store.seed(project, DefaultPlaywrightServerName, ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"@playwright/mcp@latest"},
		Enabled:   boolPtr(true),
	})
	server := newMCPServerTestServer(store)

	raw, err := server.Dispatch(context.Background(), "mcpServer/playwright/stop", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch mcpServer/playwright/stop: %v", err)
	}
	var got StopPlaywrightServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Enabled {
		t.Fatalf("StopPlaywrightServerResult = %#v, want disabled", got)
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
	return newMCPServerTestServerWithSQLitePath(store, "")
}

func newMCPServerTestServerWithSQLitePath(store MCPServerConfigStore, sqlitePath string) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, sqlitePath)).Handlers)
	return server
}

func newMCPServerTestServerWithHTTPClient(store MCPServerConfigStore, client mcpHTTPDoer) *platformrpc.Server {
	svc := newServiceWithStoresInstallerAndSQLitePath(
		store,
		newMemoryMCPToolLifecycleStore(),
		&recordingPostgresInstaller{},
		"",
	)
	svc.httpClient = client
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}
