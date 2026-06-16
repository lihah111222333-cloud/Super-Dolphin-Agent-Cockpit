package mcpservernpx

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

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
	var got StartPostgresServerResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Added || got.Config.Command != "mcp-server-postgres" {
		t.Fatalf("StartPostgresServerResult = %#v, want added mcp-server-postgres config", got)
	}
}

type recordingMCPServerService struct {
	startResult contract.MCPPostgresServerStartResult
	startErr    error
	startCalls  int
}

func (s *recordingMCPServerService) StartPostgresServer(context.Context, contract.MCPPostgresServerStartRequest) (contract.MCPPostgresServerStartResult, error) {
	s.startCalls++
	if s.startErr != nil {
		return contract.MCPPostgresServerStartResult{}, s.startErr
	}
	return s.startResult, nil
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
