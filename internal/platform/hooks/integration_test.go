package hooks

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestIntegration_MultiSubscriberConflictMerge(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	leaseA := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}
	leaseC := mcp.LeaseKey{InstanceID: "lease-c", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, leaseA, "sub-a", nil, TopicToolBefore)
	mustSubscribeIntegrationHook(t, registry, leaseB, "sub-b", nil, TopicToolBefore)
	mustSubscribeIntegrationHook(t, registry, leaseC, "sub-c", nil, TopicToolBefore)

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if payload.Topic != TopicToolBefore {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolBefore)
			}
			if payload.Depth != 1 {
				t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
			}
			if payload.HookCallID != "call-merge" {
				t.Fatalf("payload.HookCallID = %q, want %q", payload.HookCallID, "call-merge")
			}

			switch gotLease {
			case leaseA:
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionAllow,
					AllowedTools: []string{"read", "shell"},
					DeniedTools:  []string{"network"},
				}, nil
			case leaseB:
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionDeny,
					AllowedTools: []string{"read", "write"},
					DeniedTools:  []string{"exec"},
					Reason:       "policy block",
				}, nil
			case leaseC:
				return mcp.BeforeDecision{
					Decision:     mcp.HookDecisionModify,
					AllowedTools: []string{"read", "shell", "write"},
					DeniedTools:  []string{"rm", "exec"},
				}, nil
			default:
				t.Fatalf("unexpected lease = %#v", gotLease)
				return mcp.BeforeDecision{}, nil
			}
		},
	}, WithDispatcherParallelism(1))

	manager := NewManager(registry, dispatcher, NewHookResolver(&stubHookReviewStore{}))
	got, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		HookCallID: "call-merge",
		AgentID:    "agent-merge",
		ThreadID:   "thread-merge",
	})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if got.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore() decision = %q, want %q", got.Decision, mcp.HookDecisionDeny)
	}
	if got.Reason != "policy block" {
		t.Fatalf("DispatchBefore() reason = %q, want %q", got.Reason, "policy block")
	}
	if !slices.Equal(got.AllowedTools, []string{"read"}) {
		t.Fatalf("DispatchBefore() allowed tools = %#v, want %#v", got.AllowedTools, []string{"read"})
	}
	if !slices.Equal(got.DeniedTools, []string{"exec", "network", "rm"}) {
		t.Fatalf("DispatchBefore() denied tools = %#v, want %#v", got.DeniedTools, []string{"exec", "network", "rm"})
	}
}

func TestIntegration_EscalateResolveFlow(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-escalate", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-escalate", nil, TopicToolAfter)

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.Topic != TopicToolAfter {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolAfter)
			}
			if payload.Depth != 1 {
				t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
			}
			if payload.HookCallID != "call-escalate" {
				t.Fatalf("payload.HookCallID = %q, want %q", payload.HookCallID, "call-escalate")
			}
			return mcp.AfterDecision{
				Decision: mcp.HookDecisionEscalate,
				Reason:   "needs review",
			}, nil
		},
	}, WithDispatcherParallelism(1))

	resolvedAt := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	baseStore := &stubHookReviewStore{}
	baseStore.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey string) error {
		if baseStore.resolved == nil {
			baseStore.resolved = make(map[string]stubResolvedReview)
		}
		baseStore.resolved[hookCallID] = stubResolvedReview{
			decision:   decision,
			resolvedAt: resolvedAt,
		}
		return nil
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: baseStore}
	manager := NewManager(registry, dispatcher, NewHookResolver(store))

	got, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
		HookCallID: "call-escalate",
		AgentID:    "agent-escalate",
		ThreadID:   "thread-escalate",
	})
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if got.Decision != mcp.HookDecisionEscalate {
		t.Fatalf("DispatchAfter() decision = %q, want %q", got.Decision, mcp.HookDecisionEscalate)
	}
	if got.Reason != "needs review" {
		t.Fatalf("DispatchAfter() reason = %q, want %q", got.Reason, "needs review")
	}
	if len(baseStore.saved) != 1 {
		t.Fatalf("saved reviews = %d, want 1", len(baseStore.saved))
	}

	pending := baseStore.saved[0]
	if pending.HookCallID != "call-escalate" {
		t.Fatalf("pending HookCallID = %q, want %q", pending.HookCallID, "call-escalate")
	}
	if pending.Topic != TopicToolAfter {
		t.Fatalf("pending Topic = %q, want %q", pending.Topic, TopicToolAfter)
	}
	if pending.AgentID != "agent-escalate" {
		t.Fatalf("pending AgentID = %q, want %q", pending.AgentID, "agent-escalate")
	}
	if pending.SubscriberLease != "lease-escalate/1" {
		t.Fatalf("pending SubscriberLease = %q, want %q", pending.SubscriberLease, "lease-escalate/1")
	}

	resolved, err := manager.Resolve(context.Background(), mcp.LeaseKey{InstanceID: "reviewer", Generation: 1}, mcp.HookResolveRequest{
		HookCallID:     pending.HookCallID,
		Decision:       mcp.HookDecisionApprove,
		Reason:         "approved by reviewer",
		IdempotencyKey: "idem-escalate",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !resolved.Accepted {
		t.Fatal("Resolve() Accepted = false, want true")
	}
	if resolved.CanonicalDecision != mcp.HookDecisionApprove {
		t.Fatalf("Resolve() canonical decision = %q, want %q", resolved.CanonicalDecision, mcp.HookDecisionApprove)
	}
	if resolved.PendingState != pendingStateResolved {
		t.Fatalf("Resolve() pending state = %q, want %q", resolved.PendingState, pendingStateResolved)
	}
	if resolved.ResolvedAt != resolvedAt.Format(time.RFC3339Nano) {
		t.Fatalf("Resolve() resolved at = %q, want %q", resolved.ResolvedAt, resolvedAt.Format(time.RFC3339Nano))
	}
	if len(baseStore.resolveCalls) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(baseStore.resolveCalls))
	}
	if baseStore.resolveCalls[0].hookCallID != pending.HookCallID {
		t.Fatalf("ResolvePendingReview() hookCallID = %q, want %q", baseStore.resolveCalls[0].hookCallID, pending.HookCallID)
	}
	if baseStore.resolveCalls[0].decision != mcp.HookDecisionApprove {
		t.Fatalf("ResolvePendingReview() decision = %q, want %q", baseStore.resolveCalls[0].decision, mcp.HookDecisionApprove)
	}
	if baseStore.resolveCalls[0].reason != "approved by reviewer" {
		t.Fatalf("ResolvePendingReview() reason = %q, want %q", baseStore.resolveCalls[0].reason, "approved by reviewer")
	}
	if baseStore.resolveCalls[0].idempotencyKey != "idem-escalate" {
		t.Fatalf("ResolvePendingReview() idempotencyKey = %q, want %q", baseStore.resolveCalls[0].idempotencyKey, "idem-escalate")
	}
}

func TestIntegration_DepthLimitPreventsRecursion(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-depth", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-depth", nil, TopicToolBefore)

	called := false
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		before: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			called = true
			return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
		},
	}, WithDispatcherParallelism(1))

	manager := NewManager(
		registry,
		dispatcher,
		NewHookResolver(&stubHookReviewStore{}),
		WithMaxHookDepth(defaultMaxHookDepth),
	)

	got, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		HookCallID: "call-depth",
		AgentID:    "agent-depth",
		ThreadID:   "thread-depth",
		Depth:      defaultMaxHookDepth,
	})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if got.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore() decision = %q, want %q", got.Decision, mcp.HookDecisionDeny)
	}
	if called {
		t.Fatal("DispatchBefore() invoked callback at max depth")
	}
}

func TestIntegration_LostSubscriberAutoCleanup(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-lost", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-lost", nil, TopicToolAfter)

	afterCalls := 0
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			afterCalls++
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.Topic != TopicToolAfter {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolAfter)
			}
			return mcp.AfterDecision{}, errors.New("subscriber lost")
		},
	}, WithDispatcherParallelism(1))

	store := &stubHookReviewStore{}
	manager := NewManager(registry, dispatcher, NewHookResolver(store))

	for attempt := 1; attempt <= 3; attempt++ {
		got, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
			HookCallID: "call-lost",
			AgentID:    "agent-lost",
			ThreadID:   "thread-lost",
		})
		if err != nil {
			t.Fatalf("DispatchAfter() attempt %d error = %v", attempt, err)
		}
		if got.Decision != mcp.HookDecisionReject {
			t.Fatalf("DispatchAfter() attempt %d decision = %q, want %q", attempt, got.Decision, mcp.HookDecisionReject)
		}
		if attempt < 3 {
			if _, ok := registry.GetSubscription(lease); !ok {
				t.Fatalf("DispatchAfter() attempt %d unsubscribed too early", attempt)
			}
			if len(store.cancelByLeaseCalls) != 0 {
				t.Fatalf("DispatchAfter() attempt %d cancel calls = %d, want 0", attempt, len(store.cancelByLeaseCalls))
			}
		}
	}

	if _, ok := registry.GetSubscription(lease); ok {
		t.Fatal("DispatchAfter() left lost subscriber registered")
	}
	if !slices.Equal(store.cancelByLeaseCalls, []string{"lease-lost/1"}) {
		t.Fatalf("CancelPendingReviewsByLease() calls = %#v, want %#v", store.cancelByLeaseCalls, []string{"lease-lost/1"})
	}
	if afterCalls != 3 {
		t.Fatalf("DispatchAfter() callback calls = %d, want 3", afterCalls)
	}

	dispatcher.failMu.Lock()
	_, tracked := dispatcher.failCounts[lease]
	dispatcher.failMu.Unlock()
	if tracked {
		t.Fatal("DispatchAfter() left lost subscriber failure count tracked")
	}
}

func TestIntegration_ShutdownCleansUp(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-shutdown", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-shutdown", nil, TopicToolAfter)

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.HookCallID != "call-shutdown" {
				t.Fatalf("payload.HookCallID = %q, want %q", payload.HookCallID, "call-shutdown")
			}
			return mcp.AfterDecision{
				Decision: mcp.HookDecisionEscalate,
				Reason:   "needs shutdown review",
			}, nil
		},
	}, WithDispatcherParallelism(1))

	store := &stubHookReviewStore{}
	store.cancelPendingReviewsByLeaseFunc = func(_ context.Context, subscriberLease string) (int, error) {
		removed := 0
		for hookCallID, review := range store.pending {
			if review.SubscriberLease == subscriberLease {
				delete(store.pending, hookCallID)
				removed++
			}
		}
		return removed, nil
	}
	manager := NewManager(registry, dispatcher, NewHookResolver(store))

	got, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
		HookCallID: "call-shutdown",
		AgentID:    "agent-shutdown",
		ThreadID:   "thread-shutdown",
	})
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if got.Decision != mcp.HookDecisionEscalate {
		t.Fatalf("DispatchAfter() decision = %q, want %q", got.Decision, mcp.HookDecisionEscalate)
	}
	if _, ok := store.pending["call-shutdown"]; !ok {
		t.Fatal("DispatchAfter() did not create pending review")
	}

	dispatcher.failMu.Lock()
	dispatcher.failCounts[lease] = 2
	dispatcher.failMu.Unlock()

	if err := manager.ShutdownHooks(context.Background(), lease); err != nil {
		t.Fatalf("ShutdownHooks() error = %v", err)
	}
	if _, ok := registry.GetSubscription(lease); ok {
		t.Fatal("ShutdownHooks() left subscription registered")
	}
	if !slices.Equal(store.cancelByLeaseCalls, []string{"lease-shutdown/1"}) {
		t.Fatalf("CancelPendingReviewsByLease() calls = %#v, want %#v", store.cancelByLeaseCalls, []string{"lease-shutdown/1"})
	}
	if len(store.pending) != 0 {
		t.Fatalf("ShutdownHooks() pending reviews = %d, want 0", len(store.pending))
	}

	dispatcher.failMu.Lock()
	_, tracked := dispatcher.failCounts[lease]
	dispatcher.failMu.Unlock()
	if tracked {
		t.Fatal("ShutdownHooks() left failure count tracked")
	}
}

func TestIntegration_ScopedSubscriptionFiltering(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	leaseA := mcp.LeaseKey{InstanceID: "lease-scope-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-scope-b", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, leaseA, "sub-scope-a", &mcp.SelectorScope{AgentID: "agent-1"}, TopicToolBefore)
	mustSubscribeIntegrationHook(t, registry, leaseB, "sub-scope-b", &mcp.SelectorScope{}, TopicToolBefore)

	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
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

	manager := NewManager(registry, dispatcher, NewHookResolver(&stubHookReviewStore{}))

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
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
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

	manager := NewManager(registry, dispatcher, NewHookResolver(&stubHookReviewStore{}))

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
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
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

	manager := NewManager(registry, dispatcher, NewHookResolver(&stubHookReviewStore{}))

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

func TestIntegration_ManagerWiringAndThinWrappers(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{}, WithDispatcherParallelism(1))
	store := &stubHookReviewStore{
		listPendingReviewsResult: []mcp.PendingHookReview{{HookCallID: "call-pending", AgentID: "agent-wire"}},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := provideManager(managerIn{
		Registry:   registry,
		Dispatcher: dispatcher,
		Resolver:   NewHookResolver(store),
		Logger:     logger,
	})

	if manager.logger != logger {
		t.Fatal("provideManager() did not apply custom logger")
	}

	resp, err := manager.Subscribe(context.Background(), mcp.LeaseKey{InstanceID: "lease-wire", Generation: 1}, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-wire",
		Topics:         []string{TopicToolBefore},
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if !resp.Accepted {
		t.Fatal("Subscribe() Accepted = false, want true")
	}

	leases, prepared := dispatcher.prepareDispatch(TopicToolBefore, mcp.HookPayload{AgentID: "agent-wire"})
	if !slices.Equal(leases, []mcp.LeaseKey{{InstanceID: "lease-wire", Generation: 1}}) {
		t.Fatalf("prepareDispatch() leases = %#v, want %#v", leases, []mcp.LeaseKey{{InstanceID: "lease-wire", Generation: 1}})
	}
	if prepared.Topic != TopicToolBefore {
		t.Fatalf("prepareDispatch() topic = %q, want %q", prepared.Topic, TopicToolBefore)
	}
	if prepared.Depth != 1 {
		t.Fatalf("prepareDispatch() depth = %d, want 1", prepared.Depth)
	}
	if prepared.HookCallID == "" {
		t.Fatal("prepareDispatch() HookCallID = empty, want generated value")
	}

	pending, err := manager.GetPendingReviews(context.Background(), "agent-wire")
	if err != nil {
		t.Fatalf("GetPendingReviews() error = %v", err)
	}
	if len(pending) != 1 || pending[0].HookCallID != "call-pending" {
		t.Fatalf("GetPendingReviews() = %#v, want call-pending", pending)
	}
	if !slices.Equal(store.listAgentIDs, []string{"agent-wire"}) {
		t.Fatalf("ListPendingReviews() agent IDs = %#v, want %#v", store.listAgentIDs, []string{"agent-wire"})
	}
}

func mustSubscribeIntegrationHook(t *testing.T, registry *HookRegistry, lease mcp.LeaseKey, subscriptionID string, scope *mcp.SelectorScope, topics ...string) {
	t.Helper()

	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: subscriptionID,
		Topics:         topics,
		Scope:          mcp.Selector{Scope: scope},
	}); err != nil {
		t.Fatalf("Subscribe(%q) error = %v", subscriptionID, err)
	}
}
