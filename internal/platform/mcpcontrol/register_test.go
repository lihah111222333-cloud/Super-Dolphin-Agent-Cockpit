package mcpcontrol

import (
	"context"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
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

func TestRegister_CanonicalServicePeerResolvableForAnyAgentScope(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		binaryName string
		clientKind string
		capability string
	}{
		{
			name:       "orch",
			instanceID: "mcp-orch-1",
			binaryName: "mcp-orch",
			clientKind: dto.ClientKindOrch,
			capability: "tools/task",
		},
		{
			name:       "lsp",
			instanceID: "mcp-lsp-1",
			binaryName: "mcp-lsp",
			clientKind: dto.ClientKindLSP,
			capability: "tools/lsp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			local := jrpcserver.NewLocal(handler.Map{
				dto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
					return registry.Register(ctx, req)
				}),
			}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
			defer local.Close()

			// Canonical service peers register exactly the way bootstrap.registerConn does:
			// host-boot singletons with empty AgentID and PeerKind "tool".
			req := dto.RegisterRequest{
				InstanceID:          tt.instanceID,
				BinaryName:          tt.binaryName,
				ClientKind:          tt.clientKind,
				PeerKind:            dto.PeerKindTool,
				PID:                 4321,
				CapabilitiesOffered: []string{tt.capability},
			}
			var resp dto.RegisterResponse
			if err := local.Client.CallResult(context.Background(), dto.MethodRegister, req, &resp); err != nil {
				t.Fatalf("register error = %v", err)
			}

			// A tool call from an arbitrary agent must still resolve to the shared peer.
			peers := registry.FindActiveForScope(ToolScope{
				Family:   tt.clientKind,
				AgentID:  "agent-xyz",
				ThreadID: "thread-xyz",
			})
			if len(peers) != 1 {
				t.Fatalf("FindActiveForScope() resolved %d peers, want 1 shared %s peer", len(peers), tt.clientKind)
			}
			if !peers[0].Shared || peers[0].PeerKind != dto.PeerKindSharedService {
				t.Fatalf("resolved peer shared=%v peerKind=%q, want shared-service", peers[0].Shared, peers[0].PeerKind)
			}
		})
	}
}
