package rpc

import (
	"context"
	"testing"
	"time"

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

func TestStartApprovalCleanupLoopTimesOutPendingApprovals(t *testing.T) {
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
	go startApprovalCleanupLoop(ctx, manager, 10*time.Millisecond, time.Second, nil)

	ev := awaitResolvedEvent(t, resolved)
	if ev.Decision != ErrApprovalTimeout("approval timed out").Error() {
		t.Fatalf("resolved decision = %q, want timeout", ev.Decision)
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("cleanup loop left pending approvals behind")
	}
}

func TestBindApprovalLifecycleRegistersRestorePendingOnConnect(t *testing.T) {
	lc := &lifecycleRecorder{}
	approvals := NewApprovalManager(nil, nil)
	server := &Server{active: make(map[*jrpc2.Server]struct{})}

	bindApprovalLifecycle(lc, approvals, nil, server, nil)

	if got := len(server.snapshotOnConnects()); got != 1 {
		t.Fatalf("snapshotOnConnects() = %d, want 1 restore hook", got)
	}
	if got := len(lc.hooks); got != 1 {
		t.Fatalf("lifecycle hooks = %d, want 1 approval lifecycle hook", got)
	}
}
