package hooks

import (
	"context"
	"slices"
	"testing"
	"time"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
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

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			assertConflictMergePayload(t, payload)
			return conflictMergeDecisionForLease(t, gotLease, leaseA, leaseB, leaseC), nil
		},
	}, WithDispatcherParallelism(1))

	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &stubHookReviewStore{}))
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

func assertConflictMergePayload(t *testing.T, payload mcp.HookPayload) {
	t.Helper()
	if payload.Topic != TopicToolBefore {
		t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolBefore)
	}
	if payload.Depth != 1 {
		t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
	}
	if payload.HookCallID != "call-merge" {
		t.Fatalf("payload.HookCallID = %q, want %q", payload.HookCallID, "call-merge")
	}
}

func conflictMergeDecisionForLease(t *testing.T, gotLease, leaseA, leaseB, leaseC mcp.LeaseKey) mcp.BeforeDecision {
	t.Helper()
	switch gotLease {
	case leaseA:
		return mcp.BeforeDecision{
			Decision:     mcp.HookDecisionAllow,
			AllowedTools: []string{"read", "shell"},
			DeniedTools:  []string{"network"},
		}
	case leaseB:
		return mcp.BeforeDecision{
			Decision:     mcp.HookDecisionDeny,
			AllowedTools: []string{"read", "write"},
			DeniedTools:  []string{"exec"},
			Reason:       "policy block",
		}
	case leaseC:
		return mcp.BeforeDecision{
			Decision:     mcp.HookDecisionModify,
			AllowedTools: []string{"read", "shell", "write"},
			DeniedTools:  []string{"rm", "exec"},
		}
	default:
		t.Fatalf("unexpected lease = %#v", gotLease)
		return mcp.BeforeDecision{}
	}
}

func TestIntegration_EscalateResolveFlow(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-escalate", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-escalate", nil, TopicToolAfter)

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			assertEscalateCallback(t, gotLease, lease, payload)
			return mcp.AfterDecision{
				Decision: mcp.HookDecisionEscalate,
				Reason:   "needs review",
			}, nil
		},
	}, WithDispatcherParallelism(1))

	resolvedAt := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	baseStore := &stubHookReviewStore{}
	baseStore.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
		if resolvedBy != "reviewer-integration" {
			t.Fatalf("resolvedBy = %q, want %q", resolvedBy, "reviewer-integration")
		}
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
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

	got, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
		HookCallID: "call-escalate",
		AgentID:    "agent-escalate",
		ThreadID:   "thread-escalate",
	})
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	assertEscalateDispatchDecision(t, got)
	pending := mustSavedEscalateReview(t, baseStore)

	resolved, err := manager.Resolve(context.Background(), mcp.LeaseKey{InstanceID: "lease-escalate", Generation: 1}, mcp.HookResolveRequest{
		HookCallID:     pending.HookCallID,
		Decision:       mcp.HookDecisionApprove,
		Reason:         "approved by reviewer",
		IdempotencyKey: "idem-escalate",
		ResolvedBy:     "reviewer-integration",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	assertEscalateResolveResult(t, resolved, resolvedAt)
	assertEscalateResolveCall(t, baseStore, pending)
}

func assertEscalateCallback(t *testing.T, gotLease, wantLease mcp.LeaseKey, payload mcp.HookPayload) {
	t.Helper()
	if gotLease != wantLease {
		t.Fatalf("lease = %#v, want %#v", gotLease, wantLease)
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
}

func assertEscalateDispatchDecision(t *testing.T, got mcp.AfterDecision) {
	t.Helper()
	if got.Decision != mcp.HookDecisionEscalate {
		t.Fatalf("DispatchAfter() decision = %q, want %q", got.Decision, mcp.HookDecisionEscalate)
	}
	if got.Reason != "needs review" {
		t.Fatalf("DispatchAfter() reason = %q, want %q", got.Reason, "needs review")
	}
}

func mustSavedEscalateReview(t *testing.T, baseStore *stubHookReviewStore) mcp.PendingHookReview {
	t.Helper()
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
	return pending
}

func assertEscalateResolveResult(t *testing.T, resolved mcp.HookResolveResponse, resolvedAt time.Time) {
	t.Helper()
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
}

func assertEscalateResolveCall(t *testing.T, baseStore *stubHookReviewStore, pending mcp.PendingHookReview) {
	t.Helper()
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
	if baseStore.resolveCalls[0].resolvedBy != "reviewer-integration" {
		t.Fatalf("ResolvePendingReview() resolvedBy = %q, want %q", baseStore.resolveCalls[0].resolvedBy, "reviewer-integration")
	}
}

func TestIntegration_DepthLimitPreventsRecursion(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-depth", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-depth", nil, TopicToolBefore)

	called := false
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			called = true
			return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
		},
	}, WithDispatcherParallelism(1))

	manager := mustNewManager(
		t,
		registry,
		dispatcher,
		mustNewHookResolver(t, &stubHookReviewStore{}),
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
