package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestManagedAuthorityReceiptRealBootstrapMapperConsumesEveryField(t *testing.T) {
	baseline := mcpdto.ManagedAuthorityReceipt{
		ProtocolVersion: mcpdto.ManagedAuthorityProtocolVersion,
		RequestID:       "request-1",
		NextToken:       "token-2",
	}
	archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(t, baseline, func(input mcpdto.ManagedAuthorityReceipt) map[string]any {
		response := &mcpdto.RegisterResponse{
			InstanceID:            "managed:mcp-orch",
			Generation:            1,
			ServerProtocolVersion: mcpdto.ProtocolVersion,
			ManagedAuthority:      &input,
		}
		normalized, err := normalizeRegisterResponse(response, response.InstanceID)
		output := map[string]any{"error": bootstrapErrorString(err)}
		if normalized != nil && normalized.ManagedAuthority != nil {
			output["protocol_version"] = normalized.ManagedAuthority.ProtocolVersion
			output["request_id"] = normalized.ManagedAuthority.RequestID
			output["next_token"] = normalized.ManagedAuthority.NextToken
		}
		return output
	}, nil)
}

func bootstrapErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type lostAckRegisterPeer struct {
	registry *mcpcontrol.ToolRegistry
	calls    int
	captured []mcpdto.ManagedAuthorityProof
}

func (p *lostAckRegisterPeer) register(ctx context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
	p.calls++
	p.captured = append(p.captured, *req.ManagedAuthority)
	resp, err := p.registry.Register(ctx, req)
	if err != nil {
		return mcpdto.RegisterResponse{}, err
	}
	if p.calls == 1 {
		return mcpdto.RegisterResponse{}, errors.New("simulated lost register ack")
	}
	return resp, nil
}

func TestManagedRegisterLostAckRetriesSameRequestAndToken(t *testing.T) {
	registry := mcpcontrol.NewToolRegistry(mcpcontrol.RegistryOptions{
		GenerationStore:    mcpcontrol.NewMemoryGenerationStore(),
		StrictManagedKinds: []string{mcpdto.ClientKindOrch},
	})
	issued, err := registry.IssueManagedAuthority(context.Background(), mcpdto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	peer := &lostAckRegisterPeer{registry: registry}
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodRegister: platformrpc.StrictHandler(peer.register),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		cfg: Config{
			InstanceID:             issued.InstanceID,
			BootID:                 issued.BootID,
			BinaryName:             "mcp-orch",
			ClientKind:             mcpdto.ClientKindOrch,
			ManagedToken:           issued.Token,
			ManagedProtocolVersion: issued.ProtocolVersion,
		},
		instanceID:   issued.InstanceID,
		managedToken: issued.Token,
	}
	if _, err := client.registerConn(context.Background(), local.Client); err == nil {
		t.Fatal("first registerConn() error = nil, want simulated lost ack")
	}
	resp, err := client.registerConn(context.Background(), local.Client)
	if err != nil {
		t.Fatalf("retry registerConn() error = %v", err)
	}
	assertManagedRetryProof(t, peer.captured)
	assertManagedTokenRotated(t, resp, issued.Token)
	client.mu.Lock()
	client.applyRegisterLocked(resp)
	client.mu.Unlock()
	next := client.currentManagedProof()
	if next == nil || next.Token != resp.ManagedAuthority.NextToken || next.RequestID == peer.captured[0].RequestID {
		t.Fatalf("next proof = %#v, want rotated token and request id", next)
	}
}

func assertManagedRetryProof(t *testing.T, captured []mcpdto.ManagedAuthorityProof) {
	t.Helper()
	if len(captured) != 2 || captured[0] != captured[1] {
		t.Fatalf("captured proofs = %#v, want exact retry", captured)
	}
}

func assertManagedTokenRotated(t *testing.T, resp *mcpdto.RegisterResponse, issuedToken string) {
	t.Helper()
	if resp.ManagedAuthority == nil || resp.ManagedAuthority.NextToken == issuedToken {
		t.Fatalf("managed receipt = %#v, want rotated token", resp.ManagedAuthority)
	}
}
