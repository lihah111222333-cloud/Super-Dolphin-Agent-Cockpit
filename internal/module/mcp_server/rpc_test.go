package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

func TestListRPCDoesNotExposeToolLifecycleFields(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	store := newMemoryMCPServerStore()
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})
	server := newMCPServerTestServer(store)

	raw, err := server.Dispatch(context.Background(), "mcpServer/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch mcpServer/list: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	assertNoLifecycleJSONFields(t, "mcpServer/list result", payload)

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(payload["mcpServers"], &servers); err != nil {
		t.Fatalf("unmarshal mcpServers: %v", err)
	}
	var configFields map[string]json.RawMessage
	if err := json.Unmarshal(servers["my-search"], &configFields); err != nil {
		t.Fatalf("unmarshal server config: %v", err)
	}
	assertNoLifecycleJSONFields(t, "mcpServer/list server config", configFields)
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

func TestToolsRPCDoesNotExposeToolLifecycleFields(t *testing.T) {
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
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	assertNoLifecycleJSONFields(t, "mcpServer/tools result", result)

	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(result["tools"], &tools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", tools)
	}
	assertNoLifecycleJSONFields(t, "mcpServer/tools tool", tools[0])
}

func TestLifecycleRPCUpsertGetAndList(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	configStore := newMemoryMCPServerStore()
	lifecycleStore := newMemoryMCPToolLifecycleStore()
	configStore.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	server := newMCPServerLifecycleTestServer(configStore, lifecycleStore)

	upserted := dispatchLifecycleRecord(t, server, "mcpServer/toolLifecycle/upsert", map[string]any{
		"workspaceRoot": project,
		"serverName":    "my-search",
		"toolName":      "remote_search",
		"state":         "suspended",
		"reason":        " operator pause ",
	})
	assertUserLifecycleRecord(t, upserted, "remote_search", contract.MCPToolLifecycleStateSuspended, "operator pause")

	dispatchLifecycleRecord(t, server, "mcpServer/toolLifecycle/upsert", map[string]any{
		"workspaceRoot": project,
		"serverName":    "my-search",
		"toolName":      "remote_inspect",
		"state":         "active",
	})

	got := dispatchLifecycleRecord(t, server, "mcpServer/toolLifecycle/get", map[string]any{
		"workspaceRoot": project,
		"serverName":    "my-search",
		"toolName":      "remote_search",
	})
	assertUserLifecycleRecord(t, got, "remote_search", contract.MCPToolLifecycleStateSuspended, "operator pause")

	listed := dispatchLifecycleRecords(t, server, "mcpServer/toolLifecycle/list", map[string]any{
		"workspaceRoot": project,
		"serverName":    "my-search",
	})
	assertLifecycleToolNames(t, listed, []string{"remote_inspect", "remote_search"})
}

func TestLifecycleRPCRejectsInvalidPayloads(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	configStore := newMemoryMCPServerStore()
	configStore.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	server := newMCPServerLifecycleTestServer(configStore, newMemoryMCPToolLifecycleStore())
	tests := []struct {
		name    string
		method  string
		payload map[string]any
		wantErr string
	}{
		{
			name:   "list missing workspace",
			method: "mcpServer/toolLifecycle/list",
			payload: map[string]any{
				"serverName": "my-search",
			},
			wantErr: "workspaceRoot is required",
		},
		{
			name:   "get missing tool",
			method: "mcpServer/toolLifecycle/get",
			payload: map[string]any{
				"workspaceRoot": project,
				"serverName":    "my-search",
			},
			wantErr: "tool name is required",
		},
		{
			name:   "upsert invalid state",
			method: "mcpServer/toolLifecycle/upsert",
			payload: map[string]any{
				"workspaceRoot": project,
				"serverName":    "my-search",
				"toolName":      "remote_search",
				"state":         "paused",
			},
			wantErr: "invalid lifecycle state",
		},
		{
			name:   "upsert missing server",
			method: "mcpServer/toolLifecycle/upsert",
			payload: map[string]any{
				"workspaceRoot": project,
				"toolName":      "remote_search",
				"state":         "active",
			},
			wantErr: "server name is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.Dispatch(context.Background(), tt.method, mustMarshalLifecyclePayload(t, tt.payload))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Dispatch(%s) error = %v, want %q", tt.method, err, tt.wantErr)
			}
		})
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

func newMCPServerLifecycleTestServer(
	configStore MCPServerConfigStore,
	lifecycleStore contract.MCPToolLifecycleStore,
) *platformrpc.Server {
	svc := newServiceWithStoresInstallerAndSQLitePath(
		configStore,
		lifecycleStore,
		&recordingPostgresInstaller{},
		"",
	)
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}

func dispatchLifecycleRecord(
	t *testing.T,
	server *platformrpc.Server,
	method string,
	payload map[string]any,
) contract.MCPToolLifecycleRecord {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), method, mustMarshalLifecyclePayload(t, payload))
	if err != nil {
		t.Fatalf("Dispatch %s: %v", method, err)
	}
	var record contract.MCPToolLifecycleRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("unmarshal %s response: %v", method, err)
	}
	return record
}

func dispatchLifecycleRecords(
	t *testing.T,
	server *platformrpc.Server,
	method string,
	payload map[string]any,
) []contract.MCPToolLifecycleRecord {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), method, mustMarshalLifecyclePayload(t, payload))
	if err != nil {
		t.Fatalf("Dispatch %s: %v", method, err)
	}
	var records []contract.MCPToolLifecycleRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("unmarshal %s response: %v", method, err)
	}
	return records
}

func assertUserLifecycleRecord(
	t *testing.T,
	record contract.MCPToolLifecycleRecord,
	toolName string,
	state contract.MCPToolLifecycleState,
	reason string,
) {
	t.Helper()
	if record.ToolName != toolName || record.State != state || record.Source != contract.MCPToolLifecycleSourceUser {
		t.Fatalf("lifecycle record = %+v, want %s %s from user", record, toolName, state)
	}
	if record.Reason != reason || record.UpdatedBy != "user" {
		t.Fatalf("lifecycle record = %+v, want reason=%q updatedBy=user", record, reason)
	}
}

func assertLifecycleToolNames(t *testing.T, records []contract.MCPToolLifecycleRecord, want []string) {
	t.Helper()
	gotTools := lifecycleToolNames(records)
	if len(gotTools) != len(want) {
		t.Fatalf("listed lifecycle tools = %v, want %v", gotTools, want)
	}
	for i := range want {
		if gotTools[i] != want[i] {
			t.Fatalf("listed lifecycle tools = %v, want %v", gotTools, want)
		}
	}
}

func mustMarshalLifecyclePayload(t *testing.T, payload map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal lifecycle payload: %v", err)
	}
	return raw
}

func assertNoLifecycleJSONFields(t *testing.T, scope string, fields map[string]json.RawMessage) {
	t.Helper()
	for _, forbidden := range []string{
		"lifecycle", "lifecycleState", "state", "reason", "source", "updatedBy",
		"createdAt", "updatedAt",
	} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("%s unexpectedly exposes lifecycle field %q in %#v", scope, forbidden, fields)
		}
	}
}
