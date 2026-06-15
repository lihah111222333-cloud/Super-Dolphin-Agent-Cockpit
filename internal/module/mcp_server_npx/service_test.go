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

func TestDefaultPostgresServerConfigUsesRequestedNPXCommand(t *testing.T) {
	got := DefaultPostgresServerConfig()

	if got.Transport != "stdio" {
		t.Fatalf("Transport = %q, want stdio", got.Transport)
	}
	if got.Command != "npx" {
		t.Fatalf("Command = %q, want npx", got.Command)
	}
	wantArgs := []string{
		"-y",
		"@modelcontextprotocol/server-postgres",
		"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", got.Args, wantArgs)
	}
}

func TestStartPostgresServerAddsDefaultServerOnExplicitCall(t *testing.T) {
	base := &recordingMCPServerService{
		listResult: mcpserver.ListServersResult{ConfigPath: "/repo/.agent/mcp_server/config.json"},
		addResult:  mcpserver.AddServersResult{ConfigPath: "/repo/.agent/mcp_server/config.json", ServerNames: []string{DefaultPostgresServerName}},
	}
	svc := NewService(base)

	got, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err != nil {
		t.Fatalf("StartPostgresServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultPostgresServerName {
		t.Fatalf("StartPostgresServer() = %#v, want added postgres", got)
	}
	if base.addCalls != 1 {
		t.Fatalf("AddServers calls = %d, want 1", base.addCalls)
	}
	want := DefaultPostgresServerConfig()
	if !reflect.DeepEqual(base.addReq.MCPServers[DefaultPostgresServerName], want) {
		t.Fatalf("AddServers postgres config = %#v, want %#v", base.addReq.MCPServers[DefaultPostgresServerName], want)
	}
}

func TestStartPostgresServerDoesNotAutoOverrideExistingServer(t *testing.T) {
	existing := contract.MCPServerConfig{Transport: "stdio", Command: "custom-postgres-mcp"}
	base := &recordingMCPServerService{
		listResult: mcpserver.ListServersResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			MCPServers: map[string]mcpserver.ServerConfig{
				DefaultPostgresServerName: existing,
			},
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
	if !reflect.DeepEqual(got.Config, existing) {
		t.Fatalf("Config = %#v, want existing %#v", got.Config, existing)
	}
}

func TestStartPostgresServerRPCAddsDefaultServer(t *testing.T) {
	base := &recordingMCPServerService{
		listResult: mcpserver.ListServersResult{ConfigPath: "/repo/.agent/mcp_server/config.json"},
		addResult:  mcpserver.AddServersResult{ConfigPath: "/repo/.agent/mcp_server/config.json", ServerNames: []string{DefaultPostgresServerName}},
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
	if !got.Added || got.Config.Command != "npx" {
		t.Fatalf("StartPostgresServerResult = %#v, want added npx config", got)
	}
}

type recordingMCPServerService struct {
	listResult mcpserver.ListServersResult
	listErr    error
	addResult  mcpserver.AddServersResult
	addErr     error
	addReq     mcpserver.AddServersRequest
	addCalls   int
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
	return mcpserver.StartPostgresServerResult{}, errors.New("StartPostgresServer should not be called")
}

func (s *recordingMCPServerService) DeleteServer(context.Context, mcpserver.DeleteServerRequest) (mcpserver.DeleteServerResult, error) {
	return mcpserver.DeleteServerResult{}, errors.New("DeleteServer should not be called")
}
