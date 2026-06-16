package mcpservernpx

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpserver "github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestDefaultPostgresServerConfigUsesGlobalNPMCommand(t *testing.T) {
	got := DefaultPostgresServerConfig()

	if got.Transport != "stdio" {
		t.Fatalf("Transport = %q, want stdio", got.Transport)
	}
	if got.Command != "mcp-server-postgres" {
		t.Fatalf("Command = %q, want mcp-server-postgres", got.Command)
	}
	wantArgs := []string{
		"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", got.Args, wantArgs)
	}
}

func TestStartPostgresServerAddsDefaultServerOnExplicitCall(t *testing.T) {
	base := &recordingMCPServerService{
		startResult: mcpserver.StartPostgresServerResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPostgresServerName,
			Added:      true,
			Config:     DefaultPostgresServerConfig(),
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
	if base.addCalls != 0 {
		t.Fatalf("AddServers calls = %d, want 0", base.addCalls)
	}
}

func TestStartPostgresServerDoesNotAutoOverrideExistingServer(t *testing.T) {
	existing := contract.MCPServerConfig{Transport: "stdio", Command: "custom-postgres-mcp"}
	base := &recordingMCPServerService{
		startResult: mcpserver.StartPostgresServerResult{
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
	if base.addCalls != 0 {
		t.Fatalf("AddServers calls = %d, want 0", base.addCalls)
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
		startResult: mcpserver.StartPostgresServerResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			ServerName: DefaultPostgresServerName,
			Added:      true,
			Config:     DefaultPostgresServerConfig(),
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
	listResult mcpserver.ListServersResult
	listErr    error
	addResult  mcpserver.AddServersResult
	addErr     error
	addReq     mcpserver.AddServersRequest
	addCalls   int

	startResult mcpserver.StartPostgresServerResult
	startErr    error
	startCalls  int
}

func (s *recordingMCPServerService) AddServers(_ context.Context, req mcpserver.AddServersRequest) (mcpserver.AddServersResult, error) {
	s.addCalls++
	s.addReq = req
	if s.addErr != nil {
		return mcpserver.AddServersResult{}, s.addErr
	}
	return s.addResult, nil
}

func (s *recordingMCPServerService) ListServers(context.Context) (mcpserver.ListServersResult, error) {
	if s.listErr != nil {
		return mcpserver.ListServersResult{}, s.listErr
	}
	return s.listResult, nil
}

func (s *recordingMCPServerService) ListServersForCWD(context.Context, string) (mcpserver.ListServersResult, error) {
	return mcpserver.ListServersResult{}, errors.New("ListServersForCWD should not be called")
}

func (s *recordingMCPServerService) ListServerTools(context.Context, mcpserver.ListServerToolsRequest) (mcpserver.ListServerToolsResult, error) {
	return mcpserver.ListServerToolsResult{}, errors.New("ListServerTools should not be called")
}

func (s *recordingMCPServerService) StartPostgresServer(context.Context, mcpserver.StartPostgresServerRequest) (mcpserver.StartPostgresServerResult, error) {
	s.startCalls++
	if s.startErr != nil {
		return mcpserver.StartPostgresServerResult{}, s.startErr
	}
	return s.startResult, nil
}

func (s *recordingMCPServerService) DeleteServer(context.Context, mcpserver.DeleteServerRequest) (mcpserver.DeleteServerResult, error) {
	return mcpserver.DeleteServerResult{}, errors.New("DeleteServer should not be called")
}
