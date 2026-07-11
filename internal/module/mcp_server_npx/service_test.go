package mcpservernpx

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type startServerRPCResponse struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
	Enabled    bool   `json:"enabled"`
}

func TestStartPostgresServerAddsDefaultServerOnExplicitCall(t *testing.T) {
	base := &recordingMCPServerService{
		startResult: contract.MCPPostgresServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPostgresServerName,
			Added:      true,
			Config:     defaultPostgresServerConfigForNPXTest(),
		},
	}
	svc := NewService(base)

	got, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err != nil {
		t.Fatalf("StartPostgresServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultPostgresServerName {
		t.Fatalf("StartPostgresServer() = %#v, want added postgres", got)
	}
	if base.startCalls != 1 {
		t.Fatalf("StartPostgresServer calls = %d, want 1", base.startCalls)
	}
}

func TestStartPostgresServerDoesNotAutoOverrideExistingServer(t *testing.T) {
	existing := contract.MCPServerConfig{Transport: "stdio", Command: "custom-postgres-mcp"}
	base := &recordingMCPServerService{
		startResult: contract.MCPPostgresServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPostgresServerName,
			Added:      false,
			Config:     existing,
		},
	}
	svc := NewService(base)

	got, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err != nil {
		t.Fatalf("StartPostgresServer() error = %v", err)
	}
	if got.Added {
		t.Fatalf("StartPostgresServer() Added = true, want false for existing server")
	}
	if base.startCalls != 1 {
		t.Fatalf("StartPostgresServer calls = %d, want 1", base.startCalls)
	}
	if !reflect.DeepEqual(got.Config, existing) {
		t.Fatalf("Config = %#v, want existing %#v", got.Config, existing)
	}
}

func TestStartPostgresServerRPCAddsDefaultServer(t *testing.T) {
	base := &recordingMCPServerService{
		startResult: contract.MCPPostgresServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPostgresServerName,
			Added:      true,
			Config:     defaultPostgresServerConfigForNPXTest(),
		},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(base)).Handlers)

	raw, err := server.Dispatch(context.Background(), "mcpServer/postgres/start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(mcpServer/postgres/start) error = %v", err)
	}
	assertNoMCPConfigInRPCResponse(t, raw, `"config":`, "mcp-server-postgres")
	var got startServerRPCResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || got.ServerName != DefaultPostgresServerName {
		t.Fatalf("StartPostgresServerResult = %#v, want added postgres", got)
	}
}

func TestStartSQLiteServerDelegatesToBaseService(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".super-dolphin", "super-dolphin.db")
	base := &recordingMCPServerService{
		startSQLiteResult: contract.MCPSQLiteServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultSQLiteServerName,
			Added:      true,
			Enabled:    true,
			Config: contract.MCPServerConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:///" + filepath.ToSlash(dbPath)},
			},
		},
	}
	svc := NewService(base)

	got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
	if err != nil {
		t.Fatalf("StartSQLiteServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultSQLiteServerName || got.Config.Command != "npx" {
		t.Fatalf("StartSQLiteServer() = %#v, want delegated sqlite start", got)
	}
	if base.startSQLiteCalls != 1 {
		t.Fatalf("StartSQLiteServer calls = %d, want 1", base.startSQLiteCalls)
	}
}

// TestStartSQLiteServerRPCDelegatesToBaseService 覆盖兼容 RPC 入口的公开响应边界。
// service 仍可保留完整 Config，RPC result 不能再暴露命令、参数或本地数据库路径。
func TestStartSQLiteServerRPCDelegatesToBaseService(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".super-dolphin", "super-dolphin.db")
	base := &recordingMCPServerService{
		startSQLiteResult: contract.MCPSQLiteServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultSQLiteServerName,
			Added:      true,
			Enabled:    true,
			Config: contract.MCPServerConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:///" + filepath.ToSlash(dbPath)},
			},
		},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(base)).Handlers)

	raw, err := server.Dispatch(context.Background(), "mcpServer/sqlite/start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(mcpServer/sqlite/start) error = %v", err)
	}
	assertNoMCPConfigInRPCResponse(t, raw, `"config":`, "npx", "@bytebase/dbhub", dbPath)
	var got startServerRPCResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || !got.Enabled || got.ServerName != DefaultSQLiteServerName {
		t.Fatalf("StartSQLiteServerResult = %#v, want added enabled sqlite", got)
	}
	if base.startSQLiteCalls != 1 {
		t.Fatalf("StartSQLiteServer calls = %d, want 1", base.startSQLiteCalls)
	}
}

func TestStopSQLiteServerRPCDelegatesToBaseService(t *testing.T) {
	base := &recordingMCPServerService{
		stopSQLiteResult: contract.MCPSQLiteServerStopResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultSQLiteServerName,
			Enabled:    false,
		},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(base)).Handlers)

	raw, err := server.Dispatch(context.Background(), "mcpServer/sqlite/stop", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(mcpServer/sqlite/stop) error = %v", err)
	}
	var got StopSQLiteServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Enabled {
		t.Fatalf("StopSQLiteServerResult = %#v, want disabled", got)
	}
	if base.stopSQLiteCalls != 1 {
		t.Fatalf("StopSQLiteServer calls = %d, want 1", base.stopSQLiteCalls)
	}
}

func TestStartPlaywrightServerDelegatesToBaseService(t *testing.T) {
	base := &recordingMCPServerService{
		startPlaywrightResult: contract.MCPPlaywrightServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPlaywrightServerName,
			Added:      true,
			Enabled:    true,
			Config: contract.MCPServerConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"@playwright/mcp@latest"},
			},
		},
	}
	svc := NewService(base)

	got, err := svc.StartPlaywrightServer(context.Background(), StartPlaywrightServerRequest{})
	if err != nil {
		t.Fatalf("StartPlaywrightServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultPlaywrightServerName || got.Config.Command != "npx" {
		t.Fatalf("StartPlaywrightServer() = %#v, want delegated playwright start", got)
	}
	if base.startPlaywrightCalls != 1 {
		t.Fatalf("StartPlaywrightServer calls = %d, want 1", base.startPlaywrightCalls)
	}
}

func TestStartPlaywrightServerRPCDelegatesToBaseService(t *testing.T) {
	base := &recordingMCPServerService{
		startPlaywrightResult: contract.MCPPlaywrightServerStartResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPlaywrightServerName,
			Added:      true,
			Enabled:    true,
			Config: contract.MCPServerConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"@playwright/mcp@latest"},
			},
		},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(base)).Handlers)

	raw, err := server.Dispatch(context.Background(), "mcpServer/playwright/start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(mcpServer/playwright/start) error = %v", err)
	}
	assertNoMCPConfigInRPCResponse(t, raw, `"config":`, "npx", "@playwright/mcp@latest")
	var got startServerRPCResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || !got.Enabled || got.ServerName != DefaultPlaywrightServerName {
		t.Fatalf("StartPlaywrightServerResult = %#v, want added enabled playwright", got)
	}
	if base.startPlaywrightCalls != 1 {
		t.Fatalf("StartPlaywrightServer calls = %d, want 1", base.startPlaywrightCalls)
	}
}

func TestStopPlaywrightServerRPCDelegatesToBaseService(t *testing.T) {
	base := &recordingMCPServerService{
		stopPlaywrightResult: contract.MCPPlaywrightServerStopResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPlaywrightServerName,
			Enabled:    false,
		},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(base)).Handlers)

	raw, err := server.Dispatch(context.Background(), "mcpServer/playwright/stop", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(mcpServer/playwright/stop) error = %v", err)
	}
	var got StopPlaywrightServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Enabled {
		t.Fatalf("StopPlaywrightServerResult = %#v, want disabled", got)
	}
	if base.stopPlaywrightCalls != 1 {
		t.Fatalf("StopPlaywrightServer calls = %d, want 1", base.stopPlaywrightCalls)
	}
}

type recordingMCPServerService struct {
	startResult           contract.MCPPostgresServerStartResult
	startSQLiteResult     contract.MCPSQLiteServerStartResult
	stopSQLiteResult      contract.MCPSQLiteServerStopResult
	startPlaywrightResult contract.MCPPlaywrightServerStartResult
	stopPlaywrightResult  contract.MCPPlaywrightServerStopResult
	startErr              error
	startSQLiteErr        error
	stopSQLiteErr         error
	startPlaywrightErr    error
	stopPlaywrightErr     error
	startCalls            int
	startSQLiteCalls      int
	stopSQLiteCalls       int
	startPlaywrightCalls  int
	stopPlaywrightCalls   int
}

func (s *recordingMCPServerService) StartPostgresServer(context.Context, contract.MCPPostgresServerStartRequest) (contract.MCPPostgresServerStartResult, error) {
	s.startCalls++
	if s.startErr != nil {
		return contract.MCPPostgresServerStartResult{}, s.startErr
	}
	return s.startResult, nil
}

func (s *recordingMCPServerService) StartSQLiteServer(context.Context, contract.MCPSQLiteServerStartRequest) (contract.MCPSQLiteServerStartResult, error) {
	s.startSQLiteCalls++
	if s.startSQLiteErr != nil {
		return contract.MCPSQLiteServerStartResult{}, s.startSQLiteErr
	}
	return s.startSQLiteResult, nil
}

func (s *recordingMCPServerService) StopSQLiteServer(context.Context, contract.MCPSQLiteServerStopRequest) (contract.MCPSQLiteServerStopResult, error) {
	s.stopSQLiteCalls++
	if s.stopSQLiteErr != nil {
		return contract.MCPSQLiteServerStopResult{}, s.stopSQLiteErr
	}
	return s.stopSQLiteResult, nil
}

func (s *recordingMCPServerService) StartPlaywrightServer(context.Context, contract.MCPPlaywrightServerStartRequest) (contract.MCPPlaywrightServerStartResult, error) {
	s.startPlaywrightCalls++
	if s.startPlaywrightErr != nil {
		return contract.MCPPlaywrightServerStartResult{}, s.startPlaywrightErr
	}
	return s.startPlaywrightResult, nil
}

func (s *recordingMCPServerService) StopPlaywrightServer(context.Context, contract.MCPPlaywrightServerStopRequest) (contract.MCPPlaywrightServerStopResult, error) {
	s.stopPlaywrightCalls++
	if s.stopPlaywrightErr != nil {
		return contract.MCPPlaywrightServerStopResult{}, s.stopPlaywrightErr
	}
	return s.stopPlaywrightResult, nil
}

func assertNoMCPConfigInRPCResponse(t *testing.T, raw json.RawMessage, forbidden ...string) {
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

func defaultPostgresServerConfigForNPXTest() contract.MCPServerConfig {
	return contract.MCPServerConfig{
		Transport: "stdio",
		Command:   "mcp-server-postgres",
		Args: []string{
			"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
		},
	}
}
