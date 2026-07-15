package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type listServersRPCResponse struct {
	ConfigPath string                             `json:"configPath"`
	MCPServers map[string]mcpServerRPCStatusEntry `json:"mcpServers"`
}

type mcpServerRPCStatusEntry struct {
	Enabled bool `json:"enabled"`
}

type startServerRPCResponse struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
	Enabled    bool   `json:"enabled"`
}

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

func TestListRPCReturnsPublicMCPServerStatus(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	configPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer YOUR_API_KEY",
		},
		Command: "secret-command",
		Args:    []string{"--token", "secret-arg-token"},
		Env: map[string]string{
			"API_TOKEN": "secret-env-token",
		},
		Enabled: boolPtr(false),
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
	assertJSONOmitsMCPConfigDetails(t, raw, "Bearer YOUR_API_KEY", "secret-command", "secret-arg-token", "secret-env-token")
	var got listServersRPCResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, configPath)
	}
	status, ok := got.MCPServers["my-search"]
	if !ok {
		t.Fatalf("mcpServers missing my-search: %#v", got.MCPServers)
	}
	if status.Enabled {
		t.Fatalf("mcpServers my-search enabled = true, want false")
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

func TestPostgresStartRPCIsNotRegistered(t *testing.T) {
	handlers := NewHandlers(NewServiceWithStore(newMemoryMCPServerStore())).Handlers
	if _, ok := handlers["mcpServer/postgres/start"]; ok {
		t.Fatal("mcpServer/postgres/start is registered, want removed built-in RPC")
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
	assertJSONOmitsMCPConfigDetails(t, raw, `"config":`, dbPath, "npx", "@bytebase/dbhub")
	var got startServerRPCResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || !got.Enabled || got.ServerName != DefaultSQLiteServerName {
		t.Fatalf("StartSQLiteServerResult = %#v, want added enabled sqlite", got)
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
	assertJSONOmitsMCPConfigDetails(t, raw, `"config":`, "npx", "@playwright/mcp@latest")
	var got startServerRPCResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || !got.Enabled || got.ServerName != DefaultPlaywrightServerName {
		t.Fatalf("StartPlaywrightServerResult = %#v, want added enabled playwright", got)
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

func TestToolLifecycleRPCSetListAndExport(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	server := newMCPServerTestServer(seedToolLifecycleRPCStore(project))

	setDecision := dispatchSetToolLifecycleRPC(t, server, SetMCPToolLifecycleRequest{
		ServerName:      "my-search",
		ManifestName:    "search_v1",
		ToolName:        "remote_search",
		State:           contract.MCPToolLifecycleDisabled,
		Reason:          "manual review",
		ReplacementTool: "remote_search_v2",
	})
	assertToolLifecycleDecision(
		t,
		setDecision,
		"my-search",
		"remote_search",
		contract.MCPToolLifecycleDisabled,
		contract.MCPToolLifecycleDenyCodeDisabled,
	)

	listed := dispatchListToolLifecycleRPC(t, server, ListMCPToolLifecycleRequest{ServerName: "my-search"})
	if len(listed) != 1 || listed[0].ToolName != "remote_search" || listed[0].DenyCode != contract.MCPToolLifecycleDenyCodeDisabled {
		t.Fatalf("listed decisions = %#v, want remote_search disabled", listed)
	}

	dispatchSetToolLifecycleRPC(t, server, SetMCPToolLifecycleRequest{
		ServerName: "my-worker",
		ToolName:   "remote_worker",
		State:      contract.MCPToolLifecycleSuspended,
	})

	exported := dispatchExportToolLifecycleRPC(t, server, ExportMCPToolLifecycleRequest{})
	if len(exported) != 2 {
		t.Fatalf("exported decisions len = %d, want 2: %#v", len(exported), exported)
	}
	if exported[0].ServerName != "my-search" || exported[1].ServerName != "my-worker" {
		t.Fatalf("exported decisions = %#v, want decisions from both servers", exported)
	}
}

func seedToolLifecycleRPCStore(project string) *memoryMCPServerStore {
	store := newMemoryMCPServerStore()
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})
	store.seed(project, "my-worker", ServerConfig{
		Transport: "http",
		URL:       "https://worker.example.com/mcp",
	})
	return store
}

func dispatchSetToolLifecycleRPC(
	t *testing.T,
	server *platformrpc.Server,
	req SetMCPToolLifecycleRequest,
) contract.MCPToolLifecycleDecision {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal set payload: %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "mcpServer/toolLifecycle/set", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/toolLifecycle/set: %v", err)
	}
	var decision contract.MCPToolLifecycleDecision
	if err := json.Unmarshal(raw, &decision); err != nil {
		t.Fatalf("unmarshal set response: %v", err)
	}
	return decision
}

func dispatchListToolLifecycleRPC(
	t *testing.T,
	server *platformrpc.Server,
	req ListMCPToolLifecycleRequest,
) []contract.MCPToolLifecycleDecision {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal list payload: %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "mcpServer/toolLifecycle/list", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/toolLifecycle/list: %v", err)
	}
	var decisions []contract.MCPToolLifecycleDecision
	if err := json.Unmarshal(raw, &decisions); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	return decisions
}

func dispatchExportToolLifecycleRPC(
	t *testing.T,
	server *platformrpc.Server,
	req ExportMCPToolLifecycleRequest,
) []contract.MCPToolLifecycleDecision {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal export payload: %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "mcpServer/toolLifecycle/export", payload)
	if err != nil {
		t.Fatalf("Dispatch mcpServer/toolLifecycle/export: %v", err)
	}
	var decisions []contract.MCPToolLifecycleDecision
	if err := json.Unmarshal(raw, &decisions); err != nil {
		t.Fatalf("unmarshal export response: %v", err)
	}
	return decisions
}

func assertToolLifecycleDecision(
	t *testing.T,
	got contract.MCPToolLifecycleDecision,
	serverName string,
	toolName string,
	state contract.MCPToolLifecycleState,
	denyCode string,
) {
	t.Helper()
	if got.ServerName != serverName || got.ToolName != toolName || got.State != state || got.DenyCode != denyCode {
		t.Fatalf("decision = %#v, want %s/%s %s %s", got, serverName, toolName, state, denyCode)
	}
}

func TestToolLifecycleRPCRejectsInvalidInputs(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})
	server := newMCPServerTestServer(store)

	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "invalid state",
			payload: `{"serverName":"my-search","toolName":"remote_search","state":"unknown"}`,
		},
		{
			name:    "missing tool",
			payload: `{"serverName":"my-search","state":"disabled"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.Dispatch(context.Background(), "mcpServer/toolLifecycle/set", json.RawMessage(tt.payload))
			assertMCPServerRPCCode(t, err, platformrpc.CodeInvalidParams)
		})
	}
}

func TestMCPServerRPCErrorMapsToolLifecycleNotFound(t *testing.T) {
	err := mcpServerRPCError(errToolLifecycleNotFound)
	assertMCPServerRPCCode(t, err, platformrpc.CodeNotFound)
}

func assertMCPServerRPCCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want RPC code %d", want)
	}
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(want) {
		t.Fatalf("rpc code = %v, want %d", rpcErr.Code, want)
	}
}

func assertJSONOmitsMCPConfigDetails(t *testing.T, raw json.RawMessage, forbidden ...string) {
	t.Helper()
	payload := string(raw)
	for _, marker := range []string{`"transport"`, `"url"`, `"headers"`, `"command"`, `"args"`, `"env"`} {
		if strings.Contains(payload, marker) {
			t.Fatalf("response leaked MCP config key %s: %s", marker, payload)
		}
	}
	for _, marker := range forbidden {
		if strings.Contains(payload, marker) {
			t.Fatalf("response leaked MCP config marker %q: %s", marker, payload)
		}
	}
}

func newMCPServerTestServer(store MCPServerConfigStore) *platformrpc.Server {
	return newMCPServerTestServerWithSQLitePath(store, "")
}

func newMCPServerTestServerWithSQLitePath(store MCPServerConfigStore, sqlitePath string) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(newServiceWithStoreAndSQLitePath(store, sqlitePath)).Handlers)
	return server
}

func newMCPServerTestServerWithHTTPClient(store MCPServerConfigStore, client mcpHTTPDoer) *platformrpc.Server {
	svc := newServiceWithStoreAndSQLitePath(store, "")
	svc.httpClient = client
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}
