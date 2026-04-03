package mcpcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	jrpcserver "github.com/creachadair/jrpc2/server"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type stubHookManager struct {
	resolveResp dto.HookResolveResponse
	resolveErr  error
	pendingResp []dto.PendingHookReview
	pendingErr  error

	resolveLeases []dto.LeaseKey
	resolveReqs   []dto.HookResolveRequest
	pendingAgents []string
}

func (s *stubHookManager) Subscribe(context.Context, dto.LeaseKey, dto.HookSubscribeRequest) (dto.HookSubscribeResponse, error) {
	return dto.HookSubscribeResponse{}, nil
}

func (s *stubHookManager) DispatchBefore(context.Context, string, dto.HookPayload) (dto.BeforeDecision, error) {
	return dto.BeforeDecision{}, nil
}

func (s *stubHookManager) DispatchCheck(context.Context, string, dto.HookPayload) (dto.CheckDecision, error) {
	return dto.CheckDecision{}, nil
}

func (s *stubHookManager) DispatchAfter(context.Context, string, dto.HookPayload) (dto.AfterDecision, error) {
	return dto.AfterDecision{}, nil
}

func (s *stubHookManager) Resolve(_ context.Context, callerLease dto.LeaseKey, req dto.HookResolveRequest) (dto.HookResolveResponse, error) {
	s.resolveLeases = append(s.resolveLeases, callerLease)
	s.resolveReqs = append(s.resolveReqs, req)
	return s.resolveResp, s.resolveErr
}

func (s *stubHookManager) GetPendingReviews(_ context.Context, agentID string) ([]dto.PendingHookReview, error) {
	s.pendingAgents = append(s.pendingAgents, agentID)
	return s.pendingResp, s.pendingErr
}

func TestHookPendingHandler_UsesCurrentInstanceAgentID(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	hookManager := &stubHookManager{
		pendingResp: []dto.PendingHookReview{{
			HookCallID:      "call-pending",
			Topic:           "agent.tool.after",
			AgentID:         "agent-hook",
			SubscriberLease: "instance-hook/1",
			CreatedAt:       time.Unix(1, 0).UTC(),
			DeadlineAt:      time.Unix(2, 0).UTC(),
			DefaultAction:   dto.HookDecisionReject,
		}},
	}
	local := newHookHandlerLocal(t, registry, hookManager)
	defer local.Close()

	registerHookTestClient(t, local, dto.RegisterRequest{
		InstanceID: "instance-hook",
		BinaryName: "mcp-orch",
		AgentID:    "agent-hook",
		PID:        42,
		ClientKind: dto.ClientKindOrch,
	})

	var resp dto.HookPendingResponse
	if err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{}, &resp); err != nil {
		t.Fatalf("CallResult(%s) error = %v", dto.MethodHookPending, err)
	}
	if len(hookManager.pendingAgents) != 1 || hookManager.pendingAgents[0] != "agent-hook" {
		t.Fatalf("GetPendingReviews() agentID calls = %#v, want [agent-hook]", hookManager.pendingAgents)
	}
	if len(resp.Reviews) != 1 || resp.Reviews[0].HookCallID != "call-pending" {
		t.Fatalf("pending response = %#v, want call-pending", resp.Reviews)
	}
}

func TestHookPendingHandler_SharedServiceUsesRequestedAgentID(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	hookManager := &stubHookManager{
		pendingResp: []dto.PendingHookReview{{
			HookCallID: "call-shared",
			AgentID:    "agent-shared",
		}},
	}
	local := newHookHandlerLocal(t, registry, hookManager)
	defer local.Close()

	registerHookTestClient(t, local, dto.RegisterRequest{
		InstanceID: "instance-shared",
		BinaryName: "mcp-orch",
		PID:        42,
		ClientKind: dto.ClientKindOrch,
	})

	var resp dto.HookPendingResponse
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{AgentID: "agent-shared"}, &resp)
	if err != nil {
		t.Fatalf("CallResult(hook/pending) error = %v", err)
	}
	if len(hookManager.pendingAgents) != 1 || hookManager.pendingAgents[0] != "agent-shared" {
		t.Fatalf("GetPendingReviews() agentID calls = %#v, want [agent-shared]", hookManager.pendingAgents)
	}
	if len(resp.Reviews) != 1 || resp.Reviews[0].HookCallID != "call-shared" {
		t.Fatalf("pending response = %#v, want call-shared", resp.Reviews)
	}
}

func TestHookPendingHandler_SharedServiceRequiresAgentID(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	hookManager := &stubHookManager{}
	local := newHookHandlerLocal(t, registry, hookManager)
	defer local.Close()

	registerHookTestClient(t, local, dto.RegisterRequest{
		InstanceID: "instance-shared",
		BinaryName: "mcp-orch",
		PID:        42,
		ClientKind: dto.ClientKindOrch,
	})

	var resp dto.HookPendingResponse
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{}, &resp)
	if err == nil {
		t.Fatal("CallResult(hook/pending) error = nil, want invalid params")
	}
	rpcErr, ok := err.(*jrpc2.Error)
	if !ok {
		t.Fatalf("hook pending error type = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(dto.ErrCodeInvalidParams) {
		t.Fatalf("hook pending error code = %v, want %v", rpcErr.Code, dto.ErrCodeInvalidParams)
	}
	if len(hookManager.pendingAgents) != 0 {
		t.Fatalf("GetPendingReviews() calls = %#v, want none", hookManager.pendingAgents)
	}
}

func TestHookPendingHandler_RejectsMismatchedRequestedAgentID(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	hookManager := &stubHookManager{}
	local := newHookHandlerLocal(t, registry, hookManager)
	defer local.Close()

	registerHookTestClient(t, local, dto.RegisterRequest{
		InstanceID: "instance-main",
		BinaryName: "mcp-orch",
		AgentID:    "agent-main",
		PID:        42,
		ClientKind: dto.ClientKindOrch,
	})

	var resp dto.HookPendingResponse
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{AgentID: "agent-other"}, &resp)
	if err == nil {
		t.Fatal("CallResult(hook/pending) error = nil, want auth failed")
	}
	rpcErr, ok := err.(*jrpc2.Error)
	if !ok {
		t.Fatalf("hook pending error type = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(dto.ErrCodeAuthFailed) {
		t.Fatalf("hook pending error code = %v, want %v", rpcErr.Code, dto.ErrCodeAuthFailed)
	}
	if len(hookManager.pendingAgents) != 0 {
		t.Fatalf("GetPendingReviews() calls = %#v, want none", hookManager.pendingAgents)
	}
}

func TestHookResolveHandler_MapsPermissionDeniedToAuthFailed(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	hookManager := &stubHookManager{
		resolveErr: contract.ErrHookReviewPermissionDenied,
	}
	local := newHookHandlerLocal(t, registry, hookManager)
	defer local.Close()

	registerHookTestClient(t, local, dto.RegisterRequest{
		InstanceID: "instance-resolve",
		BinaryName: "mcp-orch",
		AgentID:    "agent-resolve",
		PID:        7,
		ClientKind: dto.ClientKindOrch,
	})

	err := local.Client.CallResult(context.Background(), dto.MethodHookResolve, dto.HookResolveRequest{
		HookCallID:     "call-1",
		Decision:       dto.HookDecisionApprove,
		IdempotencyKey: "idem-1",
	}, &dto.HookResolveResponse{})
	if err == nil {
		t.Fatal("CallResult(resolve) error = nil, want auth failed")
	}
	var rpcErr *jrpc2.Error
	if _, ok := err.(*jrpc2.Error); !ok {
		t.Fatalf("resolve error type = %T, want *jrpc2.Error", err)
	}
	rpcErr = err.(*jrpc2.Error)
	if rpcErr.Code != jrpc2.Code(dto.ErrCodeAuthFailed) {
		t.Fatalf("resolve error code = %v, want %v", rpcErr.Code, dto.ErrCodeAuthFailed)
	}
	if len(hookManager.resolveLeases) != 1 || hookManager.resolveLeases[0] != (dto.LeaseKey{InstanceID: "instance-resolve", Generation: 1}) {
		t.Fatalf("Resolve() caller leases = %#v, want registered lease", hookManager.resolveLeases)
	}
}

func newHookHandlerLocal(t *testing.T, registry *ToolRegistry, hookManager contract.HookManager) jrpcserver.Local {
	t.Helper()

	result := NewHandlers(HandlerDeps{
		Registry:    registry,
		HookManager: hookManager,
	})
	local := jrpcserver.NewLocal(result.Handlers, &jrpcserver.LocalOptions{
		Server: &jrpc2.ServerOptions{},
	})
	return local
}

func registerHookTestClient(t *testing.T, local jrpcserver.Local, req dto.RegisterRequest) dto.RegisterResponse {
	t.Helper()

	var resp dto.RegisterResponse
	if err := local.Client.CallResult(context.Background(), dto.MethodRegister, req, &resp); err != nil {
		t.Fatalf("CallResult(%s) error = %v", dto.MethodRegister, err)
	}
	if resp.Lease.InstanceID != req.InstanceID || resp.Lease.Generation != 1 {
		t.Fatalf("register response lease = %#v, want %s/1", resp.Lease, req.InstanceID)
	}
	return resp
}
