//go:build windows

package toolbridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type windowsACLApprovalRequesterFunc func(context.Context, contract.ApprovalRequest) (contract.ApprovalDecision, error)

func (fn windowsACLApprovalRequesterFunc) RequestApproval(ctx context.Context, req contract.ApprovalRequest) (contract.ApprovalDecision, error) {
	return fn(ctx, req)
}

func TestRegisteredWindowsACLApprovalRetriesOriginallySelectedLSPPeerOnce(t *testing.T) {
	registry := mcpcontrol.NewRegistry()
	firstPeerCalls := 0
	firstLocal := newRegisteredWindowsACLPeer(t, registry, "lsp-acl-first", func() peerToolCallResponse {
		firstPeerCalls++
		if firstPeerCalls == 1 {
			return peerToolCallResponse{
				Content:           []peerToolCallContent{{Type: "text", Text: "typed ACL failure"}},
				IsError:           true,
				StructuredContent: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"),
			}
		}
		return peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "original peer retry success"}}}
	})
	defer firstLocal.Close()
	instance := registry.FindActiveByKind(mcpdto.ClientKindLSP)[0]
	secondPeerCalls := 0
	var secondLocal jrpcserver.Local
	approved := true
	requester := windowsACLApprovalRequesterFunc(func(context.Context, contract.ApprovalRequest) (contract.ApprovalDecision, error) {
		secondLocal = newRegisteredWindowsACLPeer(t, registry, "lsp-acl-second", func() peerToolCallResponse {
			secondPeerCalls++
			return peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "new peer must not receive retry"}}}
		})
		return contract.ApprovalDecision{Approved: &approved}, nil
	})
	h := &Handler{registry: registry, approvalRequester: requester, proxyAuthToken: newProxyAuthToken()}
	result, err := h.callPeerTool(context.Background(), instance, ToolCallRequest{
		Name:       "file",
		Arguments:  json.RawMessage(`{}`),
		AgentID:    "agent-trusted",
		ThreadID:   "thread-trusted",
		TurnID:     "turn-trusted",
		CallID:     "call-registered-acl",
		ClientKind: mcpdto.ClientKindLSP,
	})
	if secondLocal.Client != nil {
		defer secondLocal.Close()
	}
	if err != nil {
		t.Fatalf("callPeerTool() error = %v", err)
	}
	if result == nil || !result.Success || firstPeerCalls != 2 || secondPeerCalls != 0 {
		t.Fatalf("result/first/new calls = %+v/%d/%d, want success/2/0", result, firstPeerCalls, secondPeerCalls)
	}
}

// newRegisteredWindowsACLPeer 通过真实 ToolRegistry 注册一个 Windows LSP peer；
// 当前 managed authority 只属于 mcp-orch，因此此处不伪造 mcp-lsp authority。
func newRegisteredWindowsACLPeer(
	t *testing.T,
	registry *mcpcontrol.ToolRegistry,
	instanceID string,
	response func() peerToolCallResponse,
) jrpcserver.Local {
	t.Helper()
	callback := platformrpc.StrictHandler(func(_ context.Context, _ map[string]any) (peerToolCallResponse, error) {
		return response(), nil
	})
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, request mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			return registry.Register(ctx, request)
		}),
	}, &jrpcserver.LocalOptions{
		Client: &jrpc2.ClientOptions{OnCallback: callback},
		Server: &jrpc2.ServerOptions{AllowPush: true},
	})
	var registered mcpdto.RegisterResponse
	err := local.Client.CallResult(context.Background(), mcpdto.MethodRegister, mcpdto.RegisterRequest{
		InstanceID: instanceID,
		BinaryName: "mcp-lsp",
		ClientKind: mcpdto.ClientKindLSP,
		PeerKind:   mcpdto.PeerKindTool,
		PID:        200,
	}, &registered)
	if err != nil {
		_ = local.Close()
		t.Fatalf("register mcp-lsp peer %q: %v", instanceID, err)
	}
	return local
}
