package rpc

import (
	"context"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"
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

	decision, err := manager.RequestApproval(context.Background(), nil, nil, ApprovalRequest{CallID: "call-1"})
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

	pending, owner := manager.registerPending(ApprovalRequest{
		CallID:  "call-1",
		AgentID: "agent-1",
		TurnID:  "turn-1",
		Kind:    "request_user_input",
	}, nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	pending.createdAt = time.Now().Add(-time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := newApprovalCleanupRunnerWithConfig(manager, nil, 10*time.Millisecond, time.Second)
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()
	defer func() {
		cancel()
		<-runDone
	}()

	ev := awaitResolvedEvent(t, resolved)
	if ev.Decision != ErrApprovalTimeout("approval timed out").Error() {
		t.Fatalf("resolved decision = %q, want timeout", ev.Decision)
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("cleanup runner left pending approvals behind")
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
			pending, owner := approvals.registerPending(ApprovalRequest{CallID: "call-1"}, nil)
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
	pending, owner := approvals.registerPending(ApprovalRequest{CallID: "call-1"}, nil)
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
