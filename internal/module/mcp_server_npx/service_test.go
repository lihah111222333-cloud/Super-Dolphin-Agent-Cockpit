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
		listResult: contract.MCPServerListResult{ConfigPath: "/repo/.agent/mcp_server/config.json"},
		addResult:  contract.MCPServerAddResult{ConfigPath: "/repo/.agent/mcp_server/config.json", ServerNames: []string{DefaultPostgresServerName}},
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
		listResult: contract.MCPServerListResult{
			ConfigPath: "/repo/.agent/mcp_server/config.json",
			MCPServers: map[string]contract.MCPServerConfig{
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
		listResult: contract.MCPServerListResult{ConfigPath: "/repo/.agent/mcp_server/config.json"},
		addResult:  contract.MCPServerAddResult{ConfigPath: "/repo/.agent/mcp_server/config.json", ServerNames: []string{DefaultPostgresServerName}},
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
	listResult contract.MCPServerListResult
	listErr    error
	addResult  contract.MCPServerAddResult
	addErr     error
	addReq     contract.MCPServerAddRequest
	addCalls   int
}

func (s *recordingMCPServerService) AddServers(_ context.Context, req contract.MCPServerAddRequest) (contract.MCPServerAddResult, error) {
	s.addCalls++
	s.addReq = req
	if s.addErr != nil {
		return contract.MCPServerAddResult{}, s.addErr
	}
	return s.addResult, nil
}

func (s *recordingMCPServerService) ListServers(context.Context) (contract.MCPServerListResult, error) {
	if s.listErr != nil {
		return contract.MCPServerListResult{}, s.listErr
	}
	return s.listResult, nil
}
