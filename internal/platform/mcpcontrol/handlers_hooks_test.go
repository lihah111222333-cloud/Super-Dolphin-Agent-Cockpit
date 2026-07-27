package mcpcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	jrpcserver "github.com/creachadair/jrpc2/server"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

// stubHookManager 记录 hook 控制面调用，便于断言 handler 是否使用分页接口。
type stubHookManager struct {
	resolveResp dto.HookResolveResponse
	resolveErr  error
	pendingResp []dto.PendingHookReview
	pendingPage contract.HookPendingReviewPage
	pendingErr  error

	resolveLeases     []dto.LeaseKey
	resolveReqs       []dto.HookResolveRequest
	pendingAgents     []string
	pendingPageParams []contract.HookPendingReviewPageParams
}

// Subscribe 只满足 HookManager 接口；pending/resolve 测试不会触发订阅路径。
func (s *stubHookManager) Subscribe(context.Context, dto.LeaseKey, dto.HookSubscribeRequest) (dto.HookSubscribeResponse, error) {
	return dto.HookSubscribeResponse{}, nil
}

// DispatchBefore 只满足 HookManager 接口；本文件聚焦控制面 handler 参数映射。
func (s *stubHookManager) DispatchBefore(context.Context, string, dto.HookPayload) (dto.BeforeDecision, error) {
	return dto.BeforeDecision{}, nil
}

// DispatchCheck 只满足 HookManager 接口；测试不执行 hook fanout。
func (s *stubHookManager) DispatchCheck(context.Context, string, dto.HookPayload) (dto.CheckDecision, error) {
	return dto.CheckDecision{}, nil
}

// DispatchAfter 只满足 HookManager 接口；测试不执行 hook fanout。
func (s *stubHookManager) DispatchAfter(context.Context, string, dto.HookPayload) (dto.AfterDecision, error) {
	return dto.AfterDecision{}, nil
}

// Resolve 记录 resolve 调用的租约和请求，供权限映射测试断言。
func (s *stubHookManager) Resolve(_ context.Context, callerLease dto.LeaseKey, req dto.HookResolveRequest) (dto.HookResolveResponse, error) {
	s.resolveLeases = append(s.resolveLeases, callerLease)
	s.resolveReqs = append(s.resolveReqs, req)
	return s.resolveResp, s.resolveErr
}

// GetPendingReviews 记录 legacy 调用；新 pending RPC 不应依赖该无分页入口。
func (s *stubHookManager) GetPendingReviews(_ context.Context, agentID string) ([]dto.PendingHookReview, error) {
	s.pendingAgents = append(s.pendingAgents, agentID)
	return s.pendingResp, s.pendingErr
}

// GetPendingReviewsPage 记录分页参数，证明 ctl/hook/pending 下推 limit 和 cursor。
func (s *stubHookManager) GetPendingReviewsPage(_ context.Context, params contract.HookPendingReviewPageParams) (contract.HookPendingReviewPage, error) {
	s.pendingAgents = append(s.pendingAgents, params.AgentID)
	s.pendingPageParams = append(s.pendingPageParams, params)
	if s.pendingPage.Reviews != nil || s.pendingPage.EffectiveLimit != 0 {
		return s.pendingPage, s.pendingErr
	}
	return contract.HookPendingReviewPage{
		Reviews:        s.pendingResp,
		EffectiveLimit: params.Limit,
	}, s.pendingErr
}

// TestHookPendingHandler_UsesCurrentInstanceAgentID 验证实例 agent 自动绑定到分页 pending 查询。
func TestHookPendingHandler_UsesCurrentInstanceAgentID(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	hookManager := &stubHookManager{
		pendingPage: contract.HookPendingReviewPage{
			Reviews: []dto.PendingHookReview{{
				HookCallID:      "call-pending",
				Topic:           "agent.tool.after",
				AgentID:         "agent-hook",
				SubscriberLease: "instance-hook/1",
				CreatedAt:       time.Unix(1, 0).UTC(),
				DeadlineAt:      time.Unix(2, 0).UTC(),
				DefaultAction:   dto.HookDecisionReject,
			}},
			HasMore:              true,
			NextCursorCreatedAt:  time.Unix(1, 0).UTC(),
			NextCursorHookCallID: "call-pending",
			EffectiveLimit:       1,
		},
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
	if err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{Limit: 1}, &resp); err != nil {
		t.Fatalf("CallResult(%s) error = %v", dto.MethodHookPending, err)
	}
	requirePendingPageCall(t, hookManager, "agent-hook", 1)
	requirePendingResponse(t, resp, "call-pending", 1, true)
}

// TestHookPendingHandler_SharedServiceUsesRequestedAgentID 验证共享服务必须显式下推 agent 和 cursor。
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
	cursorCreatedAt := time.Unix(9, 0).UTC()
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{
		AgentID: "agent-shared",
		Limit:   25,
		Cursor: &dto.HookPendingCursor{
			CreatedAt:  cursorCreatedAt,
			HookCallID: "call-cursor",
		},
	}, &resp)
	if err != nil {
		t.Fatalf("CallResult(hook/pending) error = %v", err)
	}
	requirePendingPageCall(t, hookManager, "agent-shared", 25)
	requirePendingCursor(t, hookManager.pendingPageParams[0], cursorCreatedAt, "call-cursor")
	requirePendingResponse(t, resp, "call-shared", 25, false)
}

func TestValidateHookPendingInputAllowsZeroTimestampCursorWithID(t *testing.T) {
	t.Parallel()

	req := dto.HookPendingRequest{
		Limit: 1,
		Cursor: &dto.HookPendingCursor{
			CreatedAt:  time.Time{},
			HookCallID: "call-zero",
		},
	}
	if err := validateHookPendingInput(req); err != nil {
		t.Fatalf("validateHookPendingInput() error = %v, want valid zero timestamp cursor", err)
	}
}

// TestHookPendingHandler_SharedServiceRequiresAgentID 验证共享服务缺少 agent 时 fail-fast。
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
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{Limit: 25}, &resp)
	if err == nil {
		t.Fatal("CallResult(hook/pending) error = nil, want invalid params")
	}
	rpcErr, ok := err.(*jrpc2.Error)
	if !ok {
		t.Fatalf("hook pending error type = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("hook pending error code = %v, want %v", rpcErr.Code, platformrpc.CodeInvalidParams)
	}

	if len(hookManager.pendingAgents) != 0 {
		t.Fatalf("GetPendingReviewsPage() calls = %#v, want none", hookManager.pendingAgents)
	}
}

// TestHookPendingHandler_RejectsMismatchedRequestedAgentID 验证实例 agent 与请求 agent 不一致会拒绝。
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
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{AgentID: "agent-other", Limit: 25}, &resp)
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
		t.Fatalf("GetPendingReviewsPage() calls = %#v, want none", hookManager.pendingAgents)
	}
}

// TestHookPendingHandler_RequiresLimit 验证 pending 请求必须携带显式 limit。
func TestHookPendingHandler_RequiresLimit(t *testing.T) {
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
	err := local.Client.CallResult(context.Background(), dto.MethodHookPending, dto.HookPendingRequest{}, &resp)
	if err == nil {
		t.Fatal("CallResult(hook/pending) error = nil, want invalid params")
	}
	rpcErr, ok := err.(*jrpc2.Error)
	if !ok {
		t.Fatalf("hook pending error type = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("hook pending error code = %v, want %v", rpcErr.Code, platformrpc.CodeInvalidParams)
	}
	if len(hookManager.pendingAgents) != 0 {
		t.Fatalf("GetPendingReviewsPage() calls = %#v, want none", hookManager.pendingAgents)
	}
}

// requirePendingPageCall 断言 pending handler 只调用一次分页查询且下推正确 limit。
func requirePendingPageCall(t *testing.T, hookManager *stubHookManager, wantAgentID string, wantLimit int) {
	t.Helper()

	if got := len(hookManager.pendingAgents); got != 1 {
		t.Fatalf("GetPendingReviewsPage() agent calls = %#v, want one", hookManager.pendingAgents)
	}
	if got := hookManager.pendingAgents[0]; got != wantAgentID {
		t.Fatalf("GetPendingReviewsPage() agentID = %q, want %q", got, wantAgentID)
	}
	if got := len(hookManager.pendingPageParams); got != 1 {
		t.Fatalf("GetPendingReviewsPage() params = %#v, want one", hookManager.pendingPageParams)
	}
	if got := hookManager.pendingPageParams[0].Limit; got != wantLimit {
		t.Fatalf("GetPendingReviewsPage() limit = %d, want %d", got, wantLimit)
	}
}

// requirePendingCursor 断言 pending handler 将 cursor 原样交给分页 API。
func requirePendingCursor(t *testing.T, got contract.HookPendingReviewPageParams, wantCreatedAt time.Time, wantHookCallID string) {
	t.Helper()

	if !got.CursorCreatedAt.Equal(wantCreatedAt) {
		t.Fatalf("GetPendingReviewsPage() cursor created_at = %s, want %s", got.CursorCreatedAt, wantCreatedAt)
	}
	if got.CursorHookCallID != wantHookCallID {
		t.Fatalf("GetPendingReviewsPage() cursor hook_call_id = %q, want %q", got.CursorHookCallID, wantHookCallID)
	}
}

// requirePendingResponse 断言 pending RPC 返回分页元数据和一条预期 review。
func requirePendingResponse(t *testing.T, got dto.HookPendingResponse, wantHookCallID string, wantLimit int, wantHasMore bool) {
	t.Helper()

	if count := len(got.Reviews); count != 1 {
		t.Fatalf("pending response count = %d, want 1: %#v", count, got.Reviews)
	}
	if gotID := got.Reviews[0].HookCallID; gotID != wantHookCallID {
		t.Fatalf("pending response hook_call_id = %q, want %q", gotID, wantHookCallID)
	}
	if got.Limit != wantLimit {
		t.Fatalf("pending response limit = %d, want %d", got.Limit, wantLimit)
	}
	if got.HasMore != wantHasMore {
		t.Fatalf("pending response has_more = %v, want %v", got.HasMore, wantHasMore)
	}
	if !wantHasMore {
		return
	}
	if got.NextCursor == nil {
		t.Fatal("pending response next_cursor = nil, want cursor")
	}
	if got.NextCursor.HookCallID != wantHookCallID {
		t.Fatalf("pending response next_cursor hook_call_id = %q, want %q", got.NextCursor.HookCallID, wantHookCallID)
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
	if resp.InstanceID != req.InstanceID || resp.Generation != 1 {
		t.Fatalf("register response lease = %s/%d, want %s/1", resp.InstanceID, resp.Generation, req.InstanceID)
	}
	return resp
}
