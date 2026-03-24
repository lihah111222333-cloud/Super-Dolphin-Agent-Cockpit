package mcpcontrol

import (
	"context"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

func TestRegister_ReturnsProtocolVersionAndRejectedCapabilities(t *testing.T) {
	registry := NewRegistry()
	local := jrpcserver.NewLocal(handler.Map{
		dto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
			return registry.Register(ctx, req)
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	req := dto.RegisterRequest{
		InstanceID:          "instance-1",
		BinaryName:          "mcp-lsp",
		ClientKind:          dto.ClientKindLSP,
		PeerKind:            dto.PeerKindTool,
		PID:                 1234,
		CapabilitiesOffered: []string{"tools/lsp"},
	}
	var resp dto.RegisterResponse
	if err := local.Client.CallResult(context.Background(), dto.MethodRegister, req, &resp); err != nil {
		t.Fatalf("CallResult(register) error = %v", err)
	}
	if resp.ServerProtocolVersion != dto.ProtocolVersion {
		t.Fatalf("ServerProtocolVersion = %q, want %q", resp.ServerProtocolVersion, dto.ProtocolVersion)
	}
	if resp.CapabilitiesRejected == nil {
		t.Fatal("CapabilitiesRejected = nil, want empty slice")
	}
	if len(resp.CapabilitiesRejected) != 0 {
		t.Fatalf("CapabilitiesRejected length = %d, want 0", len(resp.CapabilitiesRejected))
	}
}
