package hooks

import (
	"context"
	"slices"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestIntegration_ScopedSubscriptionFiltering(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	leaseA := mcp.LeaseKey{InstanceID: "lease-scope-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-scope-b", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, leaseA, "sub-scope-a", &mcp.SelectorScope{AgentID: "agent-1"}, TopicToolBefore)
	mustSubscribeIntegrationHook(t, registry, leaseB, "sub-scope-b", &mcp.SelectorScope{}, TopicToolBefore)

	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if payload.Topic != TopicToolBefore {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolBefore)
			}
			if payload.Depth != 1 {
				t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
			}
			calledLeases = append(calledLeases, gotLease)

			switch gotLease {
			case leaseA:
				if payload.AgentID != "agent-1" {
					t.Fatalf("payload.AgentID for scoped subscriber = %q, want %q", payload.AgentID, "agent-1")
				}
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionDeny,
					AllowedTools: []string{"read"},
					DeniedTools:  []string{"exec"},
					Reason:       "agent-1 scoped block",
				}, nil
			case leaseB:
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionAllow,
					AllowedTools: []string{"read", "write"},
					DeniedTools:  []string{"network"},
				}, nil
			default:
				t.Fatalf("unexpected lease = %#v", gotLease)
				return mcp.BeforeDecision{}, nil
			}
		},
	}, WithDispatcherParallelism(1))

	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &stubHookReviewStore{}))

	got, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		HookCallID: "call-scope-agent-1",
		AgentID:    "agent-1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore(agent-1) error = %v", err)
	}
	if !slices.Equal(calledLeases, []mcp.LeaseKey{leaseA, leaseB}) {
		t.Fatalf("DispatchBefore(agent-1) called leases = %#v, want %#v", calledLeases, []mcp.LeaseKey{leaseA, leaseB})
	}
	if got.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore(agent-1) decision = %q, want %q", got.Decision, mcp.HookDecisionDeny)
	}
	if got.Reason != "agent-1 scoped block" {
		t.Fatalf("DispatchBefore(agent-1) reason = %q, want %q", got.Reason, "agent-1 scoped block")
	}
	if !slices.Equal(got.AllowedTools, []string{"read"}) {
		t.Fatalf("DispatchBefore(agent-1) allowed tools = %#v, want %#v", got.AllowedTools, []string{"read"})
	}
	if !slices.Equal(got.DeniedTools, []string{"exec", "network"}) {
		t.Fatalf("DispatchBefore(agent-1) denied tools = %#v, want %#v", got.DeniedTools, []string{"exec", "network"})
	}

	calledLeases = calledLeases[:0]
	got, err = manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		HookCallID: "call-scope-agent-2",
		AgentID:    "agent-2",
	})
	if err != nil {
		t.Fatalf("DispatchBefore(agent-2) error = %v", err)
	}
	if !slices.Equal(calledLeases, []mcp.LeaseKey{leaseB}) {
		t.Fatalf("DispatchBefore(agent-2) called leases = %#v, want %#v", calledLeases, []mcp.LeaseKey{leaseB})
	}
	if got.Decision != mcp.HookDecisionAllow {
		t.Fatalf("DispatchBefore(agent-2) decision = %q, want %q", got.Decision, mcp.HookDecisionAllow)
	}
	if !slices.Equal(got.AllowedTools, []string{"read", "write"}) {
		t.Fatalf("DispatchBefore(agent-2) allowed tools = %#v, want %#v", got.AllowedTools, []string{"read", "write"})
	}
	if !slices.Equal(got.DeniedTools, []string{"network"}) {
		t.Fatalf("DispatchBefore(agent-2) denied tools = %#v, want %#v", got.DeniedTools, []string{"network"})
	}
}

func TestIntegration_ThreadScopedSubscription(t *testing.T) {
	t.Parallel()

	const topic = "agent.turn.before"

	registry := NewHookRegistry()
	leaseA := mcp.LeaseKey{InstanceID: "lease-thread-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-thread-b", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, leaseA, "sub-thread-a", &mcp.SelectorScope{ThreadID: "thread-1"}, topic)
	mustSubscribeIntegrationHook(t, registry, leaseB, "sub-thread-b", &mcp.SelectorScope{}, topic)

	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if payload.Topic != topic {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, topic)
			}
			if payload.Depth != 1 {
				t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
			}
			calledLeases = append(calledLeases, gotLease)

			switch gotLease {
			case leaseA:
				if payload.ThreadID != "thread-1" {
					t.Fatalf("payload.ThreadID for scoped subscriber = %q, want %q", payload.ThreadID, "thread-1")
				}
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionDeny,
					AllowedTools: []string{"read"},
					DeniedTools:  []string{"exec"},
					Reason:       "thread-1 scoped block",
				}, nil
			case leaseB:
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionAllow,
					AllowedTools: []string{"read", "write"},
					DeniedTools:  []string{"network"},
				}, nil
			default:
				t.Fatalf("unexpected lease = %#v", gotLease)
				return mcp.BeforeDecision{}, nil
			}
		},
	}, WithDispatcherParallelism(1))

	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &stubHookReviewStore{}))

	got, err := manager.DispatchBefore(context.Background(), topic, mcp.HookPayload{
		HookCallID: "call-scope-thread-1",
		AgentID:    "agent-thread",
		ThreadID:   "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore(thread-1) error = %v", err)
	}
	if !slices.Equal(calledLeases, []mcp.LeaseKey{leaseA, leaseB}) {
		t.Fatalf("DispatchBefore(thread-1) called leases = %#v, want %#v", calledLeases, []mcp.LeaseKey{leaseA, leaseB})
	}
	if got.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore(thread-1) decision = %q, want %q", got.Decision, mcp.HookDecisionDeny)
	}
	if got.Reason != "thread-1 scoped block" {
		t.Fatalf("DispatchBefore(thread-1) reason = %q, want %q", got.Reason, "thread-1 scoped block")
	}
	if !slices.Equal(got.AllowedTools, []string{"read"}) {
		t.Fatalf("DispatchBefore(thread-1) allowed tools = %#v, want %#v", got.AllowedTools, []string{"read"})
	}
	if !slices.Equal(got.DeniedTools, []string{"exec", "network"}) {
		t.Fatalf("DispatchBefore(thread-1) denied tools = %#v, want %#v", got.DeniedTools, []string{"exec", "network"})
	}

	calledLeases = calledLeases[:0]
	got, err = manager.DispatchBefore(context.Background(), topic, mcp.HookPayload{
		HookCallID: "call-scope-thread-2",
		AgentID:    "agent-thread",
		ThreadID:   "thread-2",
	})
	if err != nil {
		t.Fatalf("DispatchBefore(thread-2) error = %v", err)
	}
	if !slices.Equal(calledLeases, []mcp.LeaseKey{leaseB}) {
		t.Fatalf("DispatchBefore(thread-2) called leases = %#v, want %#v", calledLeases, []mcp.LeaseKey{leaseB})
	}
	if got.Decision != mcp.HookDecisionAllow {
		t.Fatalf("DispatchBefore(thread-2) decision = %q, want %q", got.Decision, mcp.HookDecisionAllow)
	}
	if !slices.Equal(got.AllowedTools, []string{"read", "write"}) {
		t.Fatalf("DispatchBefore(thread-2) allowed tools = %#v, want %#v", got.AllowedTools, []string{"read", "write"})
	}
	if !slices.Equal(got.DeniedTools, []string{"network"}) {
		t.Fatalf("DispatchBefore(thread-2) denied tools = %#v, want %#v", got.DeniedTools, []string{"network"})
	}
}

func TestIntegration_MultiDimensionScope(t *testing.T) {
	t.Parallel()

	const topic = "agent.turn.before"

	registry := NewHookRegistry()
	leaseA := mcp.LeaseKey{InstanceID: "lease-multi-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-multi-b", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, leaseA, "sub-multi-a", &mcp.SelectorScope{AgentID: "a1", ThreadID: "t1"}, topic)
	mustSubscribeIntegrationHook(t, registry, leaseB, "sub-multi-b", &mcp.SelectorScope{AgentID: "a1"}, topic)

	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if payload.Topic != topic {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, topic)
			}
			if payload.Depth != 1 {
				t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
			}
			calledLeases = append(calledLeases, gotLease)

			switch gotLease {
			case leaseA:
				if payload.AgentID != "a1" || payload.ThreadID != "t1" {
					t.Fatalf("payload scope for multi subscriber = (%q,%q), want (%q,%q)", payload.AgentID, payload.ThreadID, "a1", "t1")
				}
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionDeny,
					AllowedTools: []string{"read"},
					DeniedTools:  []string{"exec"},
					Reason:       "a1/t1 scoped block",
				}, nil
			case leaseB:
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionAllow,
					AllowedTools: []string{"read", "write"},
					DeniedTools:  []string{"network"},
					Reason:       "agent wildcard allow",
				}, nil
			default:
				t.Fatalf("unexpected lease = %#v", gotLease)
				return mcp.BeforeDecision{}, nil
			}
		},
	}, WithDispatcherParallelism(1))

	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &stubHookReviewStore{}))

	got, err := manager.DispatchBefore(context.Background(), topic, mcp.HookPayload{
		HookCallID: "call-scope-a1-t1",
		AgentID:    "a1",
		ThreadID:   "t1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore(a1/t1) error = %v", err)
	}
	if !slices.Equal(calledLeases, []mcp.LeaseKey{leaseA, leaseB}) {
		t.Fatalf("DispatchBefore(a1/t1) called leases = %#v, want %#v", calledLeases, []mcp.LeaseKey{leaseA, leaseB})
	}
	if got.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore(a1/t1) decision = %q, want %q", got.Decision, mcp.HookDecisionDeny)
	}
	if got.Reason != "a1/t1 scoped block" {
		t.Fatalf("DispatchBefore(a1/t1) reason = %q, want %q", got.Reason, "a1/t1 scoped block")
	}
	if !slices.Equal(got.AllowedTools, []string{"read"}) {
		t.Fatalf("DispatchBefore(a1/t1) allowed tools = %#v, want %#v", got.AllowedTools, []string{"read"})
	}
	if !slices.Equal(got.DeniedTools, []string{"exec", "network"}) {
		t.Fatalf("DispatchBefore(a1/t1) denied tools = %#v, want %#v", got.DeniedTools, []string{"exec", "network"})
	}

	calledLeases = calledLeases[:0]
	got, err = manager.DispatchBefore(context.Background(), topic, mcp.HookPayload{
		HookCallID: "call-scope-a1-t2",
		AgentID:    "a1",
		ThreadID:   "t2",
	})
	if err != nil {
		t.Fatalf("DispatchBefore(a1/t2) error = %v", err)
	}
	if !slices.Equal(calledLeases, []mcp.LeaseKey{leaseB}) {
		t.Fatalf("DispatchBefore(a1/t2) called leases = %#v, want %#v", calledLeases, []mcp.LeaseKey{leaseB})
	}
	if got.Decision != mcp.HookDecisionAllow {
		t.Fatalf("DispatchBefore(a1/t2) decision = %q, want %q", got.Decision, mcp.HookDecisionAllow)
	}
	if got.Reason != "agent wildcard allow" {
		t.Fatalf("DispatchBefore(a1/t2) reason = %q, want %q", got.Reason, "agent wildcard allow")
	}
	if !slices.Equal(got.AllowedTools, []string{"read", "write"}) {
		t.Fatalf("DispatchBefore(a1/t2) allowed tools = %#v, want %#v", got.AllowedTools, []string{"read", "write"})
	}
	if !slices.Equal(got.DeniedTools, []string{"network"}) {
		t.Fatalf("DispatchBefore(a1/t2) denied tools = %#v, want %#v", got.DeniedTools, []string{"network"})
	}
}
