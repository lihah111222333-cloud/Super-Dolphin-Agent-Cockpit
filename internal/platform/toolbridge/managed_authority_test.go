package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestManagedToolCallRejectsOldSurfaceAfterReplacement(t *testing.T) {
	registry := mcpcontrol.NewToolRegistry(mcpcontrol.RegistryOptions{
		GenerationStore:    mcpcontrol.NewMemoryGenerationStore(),
		StrictManagedKinds: []string{mcpdto.ClientKindOrch},
	})
	firstBootstrap, err := registry.IssueManagedAuthority(context.Background(), mcpdto.ManagedAuthorityIssueRequest{
		BinaryName: "mcp-orch",
	})
	if err != nil {
		t.Fatalf("IssueManagedAuthority(first) error = %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstLocal := newManagedToolPeer(t, registry, firstBootstrap, "request-1", func() {
		close(entered)
		<-release
	})
	defer firstLocal.Close()

	instance := registry.FindActiveByKind(mcpdto.ClientKindOrch)[0]
	h := &Handler{registry: registry, proxyAuthToken: newProxyAuthToken()}
	callDone := make(chan error, 1)
	go func() {
		_, callErr := h.callPeerTool(context.Background(), instance, ToolCallRequest{
			Name:      "inspect",
			Arguments: json.RawMessage(`{}`),
		})
		callDone <- callErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("managed tools/call did not enter peer callback")
	}

	replacement, err := registry.IssueManagedAuthority(context.Background(), mcpdto.ManagedAuthorityIssueRequest{
		BinaryName: "mcp-orch",
	})
	if err != nil {
		t.Fatalf("IssueManagedAuthority(replacement) error = %v", err)
	}
	secondLocal := newManagedToolPeer(t, registry, replacement, "request-2", nil)
	defer secondLocal.Close()
	close(release)

	select {
	case err := <-callDone:
		if !errors.Is(err, mcpcontrol.ErrManagedLeaseStale) {
			t.Fatalf("callPeerTool(old surface) error = %v, want ErrManagedLeaseStale", err)
		}
	case <-time.After(time.Second):
		t.Fatal("managed tools/call did not finish after replacement")
	}
}

func newManagedToolPeer(
	t *testing.T,
	registry *mcpcontrol.ToolRegistry,
	bootstrap mcpdto.ManagedAuthorityBootstrap,
	requestID string,
	block func(),
) jrpcserver.Local {
	t.Helper()
	callback := platformrpc.StrictHandler(func(_ context.Context, _ map[string]any) (peerToolCallResponse, error) {
		if block != nil {
			block()
		}
		return peerToolCallResponse{
			Content: []peerToolCallContent{{Type: "text", Text: "old surface result"}},
		}, nil
	})
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, request mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			return registry.Register(ctx, request)
		}),
	}, &jrpcserver.LocalOptions{
		Client: &jrpc2.ClientOptions{OnCallback: callback},
		Server: &jrpc2.ServerOptions{AllowPush: true},
	})
	var response mcpdto.RegisterResponse
	err := local.Client.CallResult(context.Background(), mcpdto.MethodRegister, mcpdto.RegisterRequest{
		InstanceID: bootstrap.InstanceID,
		BootID:     bootstrap.BootID,
		BinaryName: "mcp-orch",
		ClientKind: mcpdto.ClientKindOrch,
		PeerKind:   mcpdto.PeerKindSharedService,
		Shared:     true,
		PID:        100,
		ManagedAuthority: &mcpdto.ManagedAuthorityProof{
			ProtocolVersion: bootstrap.ProtocolVersion,
			RequestID:       requestID,
			Token:           bootstrap.Token,
		},
	}, &response)
	if err != nil {
		_ = local.Close()
		t.Fatalf("CallResult(register managed peer) error = %v", err)
	}
	return local
}
