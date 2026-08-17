package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"go.uber.org/fx"
)

type lifecycleRecorder struct {
	hooks []fx.Hook
}

func (r *lifecycleRecorder) Append(h fx.Hook) {
	r.hooks = append(r.hooks, h)
}

func TestRequestApprovalAutoDeclinesWithoutFrontendWhenNoCallbackPath(t *testing.T) {
	manager := NewApprovalManager(nil, nil)

	decision, err := manager.RequestApproval(context.Background(), nil, nil, testApprovalRequest("call-1"))
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("RequestApproval() approved = %v, want false", decision.Approved)
	}
	if decision.Reason != "decline" {
		t.Fatalf("RequestApproval() reason = %q, want %q", decision.Reason, "decline")
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind after auto-decline")
	}
}

func TestApprovalRequesterDoesNotFallbackToToolPeer(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	server := &Server{active: make(map[*jrpc2.Server]string)}
	toolPeer := new(jrpc2.Server)
	server.addActive(toolPeer, dto.PeerKindTool)

	requester := approvalRequester{
		manager: manager,
		bridge:  NewPushBridge(nil, nil),
		server:  server,
	}
	if got := requester.activeServer(); got != nil {
		t.Fatalf("activeServer() = %p, want nil when only tool peers are active", got)
	}
	decision, err := requester.RequestApproval(context.Background(), contract.ApprovalRequest{CallID: "call-1"})
	if !errors.Is(err, ErrNoUIPeer) {
		t.Fatalf("RequestApproval() error = %v, want ErrNoUIPeer", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("RequestApproval() approved = %v, want fail-closed decline", decision.Approved)
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind without a UI peer")
	}
}

func TestApprovalRequestFromContractKeepsSourceButUsesGenericUICallback(t *testing.T) {
	t.Parallel()
	request := approvalRequestFromContract(contract.ApprovalRequest{
		CallID:       "call-acl",
		ApprovalID:   "approval-acl",
		ToolName:     "file",
		AgentID:      "agent-1",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Kind:         "windows_acl",
		SourceMethod: "tools/call",
		Payload:      map[string]any{"windows_error_code": 5},
	})
	if request.SourceMethod != "tools/call" {
		t.Fatalf("SourceMethod = %q, want provenance tools/call", request.SourceMethod)
	}
	if request.CallbackMethod != DefaultApprovalCallbackMethod {
		t.Fatalf("CallbackMethod = %q, want %q", request.CallbackMethod, DefaultApprovalCallbackMethod)
	}
	if got := callbackMethod(request); got != DefaultApprovalCallbackMethod {
		t.Fatalf("callbackMethod() = %q, want %q", got, DefaultApprovalCallbackMethod)
	}
}

func TestApprovalRequesterPublishesWindowsACLPromptAndReceivesUIDecision(t *testing.T) {
	dispatcher := event.NewDispatcher()
	manager := NewApprovalManager(nil, dispatcher)
	bridge := NewPushBridge(dispatcher, nil)
	requested := make(chan tooldto.ToolApprovalRequested, 1)
	cancelSubscription := event.Subscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSubscription()
	callbackParams := make(chan map[string]any, 1)
	local := jrpcserver.NewLocal(handler.Map{}, &jrpcserver.LocalOptions{
		Client: &jrpc2.ClientOptions{OnCallback: StrictHandler(func(_ context.Context, params map[string]any) (map[string]any, error) {
			callbackParams <- params
			return map[string]any{"approved": true, "reason": "approved"}, nil
		})},
		Server: &jrpc2.ServerOptions{AllowPush: true},
	})
	defer local.Close()
	server := &Server{active: make(map[*jrpc2.Server]string)}
	server.addActive(local.Server, dto.PeerKindUI)
	requester := approvalRequester{manager: manager, bridge: bridge, server: server}
	decision, err := requester.RequestApproval(context.Background(), contract.ApprovalRequest{
		CallID:       "call-acl-ui",
		ApprovalID:   "call-acl-ui",
		ToolName:     "file",
		AgentID:      "agent-trusted",
		ThreadID:     "thread-trusted",
		TurnID:       "turn-trusted",
		Reason:       "Windows 权限不足；批准后仅重试一次。",
		Kind:         "windows_acl",
		SourceMethod: "tools/call",
		Payload: map[string]any{
			"authorization_required":  true,
			"windows_error_code":      5,
			"windows_permission_kind": "access_denied",
		},
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if decision.Approved == nil || !*decision.Approved {
		t.Fatalf("RequestApproval() decision = %+v, want UI approval", decision)
	}
	select {
	case ev := <-requested:
		if ev.CallID != "call-acl-ui" || ev.AgentID != "agent-trusted" || ev.ThreadID != "thread-trusted" || ev.Kind != "windows_acl" {
			t.Fatalf("ToolApprovalRequested identity = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	select {
	case params := <-callbackParams:
		if params["sourceMethod"] != "tools/call" || params["kind"] != "windows_acl" || params["windows_error_code"] != float64(5) && params["windows_error_code"] != 5 {
			t.Fatalf("approval callback params = %#v", params)
		}
		if _, leaked := params["file_path"]; leaked {
			t.Fatalf("approval callback leaked file_path: %#v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for UI approval callback")
	}
}

func TestApprovalCleanupRunnerTimesOutPendingApprovals(t *testing.T) {
	// ApprovalCleanupRunner 取代旧的全局清理循环后，测试必须注入实例级短周期。
	// 这样能在 10ms 量级触发超时，又不修改包级默认值影响其它审批用例。
	dispatcher := event.NewDispatcher()
	manager := NewApprovalManager(nil, dispatcher)
	resolved := make(chan tooldto.ToolApprovalResolved, 1)
	cancelSub := event.Subscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
		resolved <- ev
	})
	defer cancelSub()

	req := testApprovalRequest("call-1")
	req.AgentID = "agent-1"
	req.TurnID = "turn-1"
	req.Kind = "request_user_input"
	pending, owner := manager.registerPending(req, nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	pending.createdAt = time.Now().Add(-time.Minute)

	runner := newApprovalCleanupRunnerWithConfig(manager, nil, 10*time.Millisecond, time.Second)
	cancel, _ := startRPCRunnerForTest(t, runner.Run)
	defer cancel()

	ev := awaitResolvedEvent(t, resolved)
	if ev.Decision != ErrApprovalTimeout("approval timed out").Error() {
		t.Fatalf("resolved decision = %q, want timeout", ev.Decision)
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("cleanup runner left pending approvals behind")
	}
}

func TestApprovalCleanupRunnerExpiresOnlyCompletedIdentityPastTTL(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	decision := contract.ApprovalDecision{Approved: boolPtr(true), Reason: "approved"}
	expired := contract.ApprovalIdentity{SessionScope: "scope-a", CallID: "call-a", RequestID: 41}
	registerAndResolveApproval(t, manager, expired, decision)
	if err := manager.Respond(expired, decision); err != nil {
		t.Fatalf("Respond(expired within TTL) error = %v, want idempotent success", err)
	}

	timeout := 100 * time.Millisecond
	time.Sleep(timeout + 50*time.Millisecond)
	retained := []contract.ApprovalIdentity{
		{SessionScope: "scope-b", CallID: "call-a", RequestID: 41},
		{SessionScope: "scope-a", CallID: "call-b", RequestID: 41},
		{SessionScope: "scope-a", CallID: "call-a", RequestID: 42},
	}
	for _, identity := range retained {
		registerAndResolveApproval(t, manager, identity, decision)
	}

	newApprovalCleanupRunnerWithConfig(manager, nil, time.Hour, timeout).tick()

	wantNotFound := ErrNotFound("approval is not pending").Error()
	if err := manager.Respond(expired, decision); err == nil || err.Error() != wantNotFound {
		t.Fatalf("Respond(expired after TTL) error = %v, want %q", err, wantNotFound)
	}
	for _, identity := range retained {
		if err := manager.Respond(identity, decision); err != nil {
			t.Fatalf("Respond(retained %+v) error = %v, want idempotent success", identity, err)
		}
	}
}

func registerAndResolveApproval(t *testing.T, manager *ApprovalManager, identity contract.ApprovalIdentity, decision contract.ApprovalDecision) {
	t.Helper()
	requestID := identity.RequestID
	if _, owner := manager.registerPending(ApprovalRequest{
		SessionScope: identity.SessionScope,
		CallID:       identity.CallID,
		RequestID:    &requestID,
	}, nil); !owner {
		t.Fatalf("registerPending(%+v) owner = false, want true", identity)
	}
	if err := manager.Respond(identity, decision); err != nil {
		t.Fatalf("Respond(%+v) error = %v", identity, err)
	}
}

func TestBindApprovalLifecycleRegistersRestorePendingOnConnect(t *testing.T) {
	lc := &lifecycleRecorder{}
	approvals := NewApprovalManager(nil, nil)
	server := &Server{active: make(map[*jrpc2.Server]string)}

	bindApprovalLifecycle(lc, approvals, nil, server, nil)

	if got := len(server.snapshotOnConnects()); got != 1 {
		t.Fatalf("snapshotOnConnects() = %d, want 1 restore hook", got)
	}
	if got := len(lc.hooks); got != 1 {
		t.Fatalf("lifecycle hooks = %d, want 1 approval lifecycle hook", got)
	}
}

func TestBindApprovalLifecycleRestoresPendingOnlyForUIConnections(t *testing.T) {
	for _, tc := range []struct {
		name         string
		peerKind     string
		wantDispatch bool
	}{
		{name: "ui", peerKind: dto.PeerKindUI, wantDispatch: true},
		{name: "tool", peerKind: dto.PeerKindTool, wantDispatch: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := &lifecycleRecorder{}
			approvals := NewApprovalManager(nil, nil)
			pending, owner := approvals.registerPending(testApprovalRequest("call-1"), nil)
			if !owner {
				t.Fatal("registerPending owner = false, want true")
			}
			baseline := time.Now().Add(-time.Minute)
			pending.createdAt = baseline

			local, bridge := newBlockingApprovalLocal(t)
			server := &Server{active: make(map[*jrpc2.Server]string)}
			server.addActive(local.Server, tc.peerKind)

			bindApprovalLifecycle(lc, approvals, bridge, server, nil)

			createdAt, dispatching := pendingState(approvals, pending)
			if dispatching != tc.wantDispatch {
				t.Fatalf("dispatching = %v, want %v", dispatching, tc.wantDispatch)
			}
			if tc.wantDispatch && !createdAt.After(baseline) {
				t.Fatalf("createdAt = %s, want after %s", createdAt, baseline)
			}
			if !tc.wantDispatch && !createdAt.Equal(baseline) {
				t.Fatalf("createdAt = %s, want %s", createdAt, baseline)
			}
		})
	}
}

func TestRestorePendingRefreshesTTLAfterReplayDispatch(t *testing.T) {
	approvals := NewApprovalManager(nil, nil)
	pending, owner := approvals.registerPending(testApprovalRequest("call-1"), nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	baseline := time.Now().Add(-4 * time.Minute)
	pending.createdAt = baseline

	local, bridge := newBlockingApprovalLocal(t)
	if err := approvals.RestorePending(context.Background(), bridge, local.Server); err != nil {
		t.Fatalf("RestorePending() error = %v", err)
	}

	createdAt, dispatching := pendingState(approvals, pending)
	if !dispatching {
		t.Fatal("RestorePending() did not restart dispatch")
	}
	if !createdAt.After(baseline) {
		t.Fatalf("createdAt = %s, want after %s", createdAt, baseline)
	}
}

func pendingState(approvals *ApprovalManager, pending *pendingApproval) (time.Time, bool) {
	approvals.mu.Lock()
	defer approvals.mu.Unlock()
	return pending.createdAt, pending.dispatching
}
