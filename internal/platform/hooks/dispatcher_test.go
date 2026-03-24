package hooks

import (
	"context"
	"errors"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type stubPeerCallback struct {
	before func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.BeforeDecision, error)
	check  func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.CheckDecision, error)
	after  func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.AfterDecision, error)
}

func (s stubPeerCallback) CallbackBefore(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
	if s.before == nil {
		return mcp.BeforeDecision{}, nil
	}
	return s.before(ctx, lease, payload)
}

func (s stubPeerCallback) CallbackCheck(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error) {
	if s.check == nil {
		return mcp.CheckDecision{}, nil
	}
	return s.check(ctx, lease, payload)
}

func (s stubPeerCallback) CallbackAfter(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
	if s.after == nil {
		return mcp.AfterDecision{}, nil
	}
	return s.after(ctx, lease, payload)
}

func TestHookDispatcherDispatchBeforeReturnsEmptyWithoutSubscribers(t *testing.T) {
	t.Parallel()

	dispatcher := NewHookDispatcher(NewHookRegistry(), stubPeerCallback{})
	decisions, err := dispatcher.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("DispatchBefore() len = %d, want 0", len(decisions))
	}
}

func TestHookDispatcherDispatchBeforeSinglePeerSuccess(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	_, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-a",
		Topics:         []string{TopicToolBefore},
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.Topic != TopicToolBefore {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolBefore)
			}
			if payload.HookCallID == "" {
				t.Fatal("payload.HookCallID = empty, want generated value")
			}
			return mcp.BeforeDecision{
				Decision:     mcp.HookDecisionAllow,
				AllowedTools: []string{"shell"},
			}, nil
		},
	})

	decisions, err := dispatcher.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("DispatchBefore() len = %d, want 1", len(decisions))
	}
	if decisions[0].Err != nil {
		t.Fatalf("DispatchBefore() peer err = %v, want nil", decisions[0].Err)
	}
	if decisions[0].Decision.Decision != mcp.HookDecisionAllow {
		t.Fatalf("DispatchBefore() decision = %q, want %q", decisions[0].Decision.Decision, mcp.HookDecisionAllow)
	}
}

func TestHookDispatcherDispatchBeforeSinglePeerTimeout(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	_, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-a",
		Topics:         []string{TopicToolBefore},
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	dispatcher := NewHookDispatcher(
		registry,
		stubPeerCallback{
			before: func(ctx context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
				<-ctx.Done()
				return mcp.BeforeDecision{}, ctx.Err()
			},
		},
		WithPeerTimeout(10*time.Millisecond),
	)

	decisions, err := dispatcher.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("DispatchBefore() len = %d, want 1", len(decisions))
	}
	if !errors.Is(decisions[0].Err, context.DeadlineExceeded) {
		t.Fatalf("DispatchBefore() peer err = %v, want deadline exceeded", decisions[0].Err)
	}
	if decisions[0].ConsecutiveFailures != 1 {
		t.Fatalf("DispatchBefore() failures = %d, want 1", decisions[0].ConsecutiveFailures)
	}
}

func TestDispatchCheck_Success(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-check",
		Topics:         []string{TopicToolBefore},
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		check: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.Topic != TopicToolBefore {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolBefore)
			}
			if payload.Depth != 1 {
				t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
			}
			if payload.HookCallID == "" {
				t.Fatal("payload.HookCallID = empty, want generated value")
			}
			return mcp.CheckDecision{
				Decision: mcp.HookDecisionWarn,
				Severity: "high",
				Reason:   "policy warning",
			}, nil
		},
	}, WithDispatcherParallelism(1))

	decisions, err := dispatcher.DispatchCheck(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchCheck() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("DispatchCheck() len = %d, want 1", len(decisions))
	}
	if decisions[0].Err != nil {
		t.Fatalf("DispatchCheck() peer err = %v, want nil", decisions[0].Err)
	}
	if decisions[0].Decision.Decision != mcp.HookDecisionWarn {
		t.Fatalf("DispatchCheck() decision = %q, want %q", decisions[0].Decision.Decision, mcp.HookDecisionWarn)
	}
	if decisions[0].Decision.Severity != "high" {
		t.Fatalf("DispatchCheck() severity = %q, want %q", decisions[0].Decision.Severity, "high")
	}
	if decisions[0].ConsecutiveFailures != 0 {
		t.Fatalf("DispatchCheck() failures = %d, want 0", decisions[0].ConsecutiveFailures)
	}
}

func TestDispatchAfter_Success(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-after",
		Topics:         []string{TopicToolAfter},
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.Topic != TopicToolAfter {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolAfter)
			}
			if payload.Depth != 2 {
				t.Fatalf("payload.Depth = %d, want 2", payload.Depth)
			}
			if payload.HookCallID != "call-after" {
				t.Fatalf("payload.HookCallID = %q, want %q", payload.HookCallID, "call-after")
			}
			return mcp.AfterDecision{
				Decision: mcp.HookDecisionApprove,
				Reason:   "looks good",
			}, nil
		},
	})

	decisions, err := dispatcher.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
		AgentID:    "agent-1",
		ThreadID:   "thread-1",
		HookCallID: "call-after",
		Depth:      1,
	})
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("DispatchAfter() len = %d, want 1", len(decisions))
	}
	if decisions[0].Err != nil {
		t.Fatalf("DispatchAfter() peer err = %v, want nil", decisions[0].Err)
	}
	if decisions[0].Decision.Decision != mcp.HookDecisionApprove {
		t.Fatalf("DispatchAfter() decision = %q, want %q", decisions[0].Decision.Decision, mcp.HookDecisionApprove)
	}
	if decisions[0].Decision.Reason != "looks good" {
		t.Fatalf("DispatchAfter() reason = %q, want %q", decisions[0].Decision.Reason, "looks good")
	}
}

func TestForgetLease(t *testing.T) {
	t.Parallel()

	dispatcher := NewHookDispatcher(NewHookRegistry(), stubPeerCallback{})
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}

	if failures := dispatcher.recordPeerResult(lease, errors.New("boom")); failures != 1 {
		t.Fatalf("recordPeerResult(error) = %d, want 1", failures)
	}
	if got := dispatcher.failCounts[lease]; got != 1 {
		t.Fatalf("failCounts[%#v] = %d, want 1", lease, got)
	}
	if failures := dispatcher.recordPeerResult(lease, nil); failures != 0 {
		t.Fatalf("recordPeerResult(nil) = %d, want 0", failures)
	}
	if _, ok := dispatcher.failCounts[lease]; ok {
		t.Fatalf("failCounts still contains %#v after success", lease)
	}
}
