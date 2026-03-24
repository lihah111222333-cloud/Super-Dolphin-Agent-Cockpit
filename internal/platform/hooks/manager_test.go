package hooks

import (
	"context"
	"errors"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestManagerDispatchAfterEscalatePersistsSubscriberLease(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	approveLease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	escalateLease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}

	if _, err := registry.Subscribe(approveLease, mcp.HookSubscribeRequest{
		SubscriptionID: "approve-sub",
		Topics:         []string{TopicToolAfter},
	}); err != nil {
		t.Fatalf("Subscribe(approve) error = %v", err)
	}
	if _, err := registry.Subscribe(escalateLease, mcp.HookSubscribeRequest{
		SubscriptionID: "escalate-sub",
		Topics:         []string{TopicToolAfter},
	}); err != nil {
		t.Fatalf("Subscribe(escalate) error = %v", err)
	}

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		after: func(_ context.Context, lease mcp.LeaseKey, _ mcp.HookPayload) (mcp.AfterDecision, error) {
			if lease == escalateLease {
				return mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, Reason: "needs review"}, nil
			}
			return mcp.AfterDecision{Decision: mcp.HookDecisionApprove}, nil
		},
	})
	store := &managerReviewStoreStub{}
	manager := NewManager(registry, dispatcher, NewHookResolver(store))

	got, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if got.Decision != mcp.HookDecisionEscalate {
		t.Fatalf("DispatchAfter() decision = %q, want %q", got.Decision, mcp.HookDecisionEscalate)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved reviews = %d, want 1", len(store.saved))
	}
	if store.saved[0].SubscriberLease != "lease-b/1" {
		t.Fatalf("saved subscriber lease = %q, want %q", store.saved[0].SubscriberLease, "lease-b/1")
	}
	if store.saved[0].HookCallID == "" {
		t.Fatal("saved HookCallID = empty, want generated value")
	}
}

type managerReviewStoreStub struct {
	saved                           []mcp.PendingHookReview
	byID                            map[string]mcp.PendingHookReview
	cancelPendingReviewsByLeaseFunc func(context.Context, string) (int, error)
}

func (s *managerReviewStoreStub) SavePendingReview(_ context.Context, review mcp.PendingHookReview) error {
	if s.byID == nil {
		s.byID = make(map[string]mcp.PendingHookReview)
	}
	s.saved = append(s.saved, review)
	s.byID[review.HookCallID] = review
	return nil
}

func (s *managerReviewStoreStub) GetPendingReview(_ context.Context, hookCallID string) (mcp.PendingHookReview, error) {
	return s.byID[hookCallID], nil
}

func (s *managerReviewStoreStub) ListPendingReviews(_ context.Context, _ string) ([]mcp.PendingHookReview, error) {
	return nil, nil
}

func (s *managerReviewStoreStub) ResolvePendingReview(_ context.Context, _, _, _, _ string) error {
	return nil
}

func (s *managerReviewStoreStub) CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error) {
	if s.cancelPendingReviewsByLeaseFunc != nil {
		return s.cancelPendingReviewsByLeaseFunc(ctx, subscriberLease)
	}
	return 0, nil
}

func (s *managerReviewStoreStub) CancelPendingReviewsByAgent(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *managerReviewStoreStub) CancelExpiredReviews(_ context.Context) (int, error) {
	return 0, nil
}

func (s *managerReviewStoreStub) RecoverOnStartup(_ context.Context) ([]mcp.PendingHookReview, error) {
	return nil, nil
}

func TestDepthExceedsMax_Before(t *testing.T) {
	t.Parallel()

	called := false
	manager, registry := newDepthTestManager(stubPeerCallback{
		before: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			called = true
			return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
		},
	})
	subscribeDepthTestTopics(t, registry, TopicToolBefore)

	decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, depthTestPayload(3))
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore() decision = %q, want %q", decision.Decision, mcp.HookDecisionDeny)
	}
	if called {
		t.Fatal("DispatchBefore() invoked callback at max depth")
	}
}

func TestManagerShutdownHooks_UnsubscribesBeforeCancel(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-a",
		Topics:         []string{TopicToolAfter},
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	dispatcher := NewHookDispatcher(registry, stubPeerCallback{})
	dispatcher.failMu.Lock()
	dispatcher.failCounts[lease] = 2
	dispatcher.failMu.Unlock()

	cancelled := false
	store := &managerReviewStoreStub{
		cancelPendingReviewsByLeaseFunc: func(_ context.Context, subscriberLease string) (int, error) {
			cancelled = true
			if subscriberLease != "lease-a/1" {
				t.Fatalf("CancelPendingReviewsByLease() lease = %q, want %q", subscriberLease, "lease-a/1")
			}
			if _, ok := registry.GetSubscription(lease); ok {
				t.Fatal("subscription still present while cancelling pending reviews")
			}
			dispatcher.failMu.Lock()
			_, tracked := dispatcher.failCounts[lease]
			dispatcher.failMu.Unlock()
			if tracked {
				t.Fatal("failure count still present while cancelling pending reviews")
			}
			return 1, nil
		},
	}
	manager := NewManager(registry, dispatcher, NewHookResolver(store))

	if err := manager.ShutdownHooks(context.Background(), lease); err != nil {
		t.Fatalf("ShutdownHooks() error = %v", err)
	}
	if !cancelled {
		t.Fatal("ShutdownHooks() did not cancel pending reviews")
	}
	if _, ok := registry.GetSubscription(lease); ok {
		t.Fatal("ShutdownHooks() left subscription registered")
	}
	dispatcher.failMu.Lock()
	_, tracked := dispatcher.failCounts[lease]
	dispatcher.failMu.Unlock()
	if tracked {
		t.Fatal("ShutdownHooks() left failure count tracked")
	}
}

func TestLostSubscriberAutoUnsubscribe(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-lost", Generation: 1}
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "lost-sub",
		Topics:         []string{TopicToolBefore},
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	beforeCalls := 0
	dispatcher := NewHookDispatcher(registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			beforeCalls++
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			return mcp.BeforeDecision{}, errors.New("subscriber lost")
		},
	})

	cancelCalls := 0
	store := &managerReviewStoreStub{
		cancelPendingReviewsByLeaseFunc: func(_ context.Context, subscriberLease string) (int, error) {
			cancelCalls++
			if subscriberLease != "lease-lost/1" {
				t.Fatalf("CancelPendingReviewsByLease() lease = %q, want %q", subscriberLease, "lease-lost/1")
			}
			if _, ok := registry.GetSubscription(lease); ok {
				t.Fatal("subscription still present while cancelling lost subscriber")
			}
			dispatcher.failMu.Lock()
			_, tracked := dispatcher.failCounts[lease]
			dispatcher.failMu.Unlock()
			if tracked {
				t.Fatal("failure count still present while cancelling lost subscriber")
			}
			return 1, nil
		},
	}
	manager := NewManager(registry, dispatcher, NewHookResolver(store))
	payload := depthTestPayload(0)

	for attempt := 1; attempt <= 2; attempt++ {
		decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, payload)
		if err != nil {
			t.Fatalf("DispatchBefore() attempt %d error = %v", attempt, err)
		}
		if decision.Decision != mcp.HookDecisionDeny {
			t.Fatalf("DispatchBefore() attempt %d decision = %q, want %q", attempt, decision.Decision, mcp.HookDecisionDeny)
		}
		if _, ok := registry.GetSubscription(lease); !ok {
			t.Fatalf("DispatchBefore() attempt %d unsubscribed too early", attempt)
		}
		dispatcher.failMu.Lock()
		failures := dispatcher.failCounts[lease]
		dispatcher.failMu.Unlock()
		if failures != attempt {
			t.Fatalf("DispatchBefore() attempt %d failures = %d, want %d", attempt, failures, attempt)
		}
		if cancelCalls != 0 {
			t.Fatalf("DispatchBefore() attempt %d cancel calls = %d, want 0", attempt, cancelCalls)
		}
	}

	decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, payload)
	if err != nil {
		t.Fatalf("DispatchBefore() third attempt error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore() third attempt decision = %q, want %q", decision.Decision, mcp.HookDecisionDeny)
	}
	if _, ok := registry.GetSubscription(lease); ok {
		t.Fatal("DispatchBefore() left lost subscriber registered")
	}
	dispatcher.failMu.Lock()
	_, tracked := dispatcher.failCounts[lease]
	dispatcher.failMu.Unlock()
	if tracked {
		t.Fatal("DispatchBefore() left lost subscriber failure count tracked")
	}
	if cancelCalls != 1 {
		t.Fatalf("DispatchBefore() cancel calls = %d, want 1", cancelCalls)
	}

	if _, err := manager.DispatchBefore(context.Background(), TopicToolBefore, payload); err != nil {
		t.Fatalf("DispatchBefore() fourth attempt error = %v", err)
	}
	if beforeCalls != 3 {
		t.Fatalf("DispatchBefore() callback calls = %d, want 3 after auto-unsubscribe", beforeCalls)
	}
}

func TestDepthExceedsMax_Check(t *testing.T) {
	t.Parallel()

	called := false
	manager, registry := newDepthTestManager(stubPeerCallback{
		check: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.CheckDecision, error) {
			called = true
			return mcp.CheckDecision{Decision: mcp.HookDecisionWarn}, nil
		},
	})
	subscribeDepthTestTopics(t, registry, TopicToolBefore)

	decision, err := manager.DispatchCheck(context.Background(), TopicToolBefore, depthTestPayload(3))
	if err != nil {
		t.Fatalf("DispatchCheck() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionContinue {
		t.Fatalf("DispatchCheck() decision = %q, want %q", decision.Decision, mcp.HookDecisionContinue)
	}
	if called {
		t.Fatal("DispatchCheck() invoked callback at max depth")
	}
}

func TestDepthExceedsMax_After(t *testing.T) {
	t.Parallel()

	called := false
	manager, registry := newDepthTestManager(stubPeerCallback{
		after: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.AfterDecision, error) {
			called = true
			return mcp.AfterDecision{Decision: mcp.HookDecisionApprove}, nil
		},
	})
	subscribeDepthTestTopics(t, registry, TopicToolAfter)

	decision, err := manager.DispatchAfter(context.Background(), TopicToolAfter, depthTestPayload(3))
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionReject {
		t.Fatalf("DispatchAfter() decision = %q, want %q", decision.Decision, mcp.HookDecisionReject)
	}
	if called {
		t.Fatal("DispatchAfter() invoked callback at max depth")
	}
}

func TestDepthBelowMax(t *testing.T) {
	t.Parallel()

	beforeCalled := false
	checkCalled := false
	afterCalled := false
	manager, registry := newDepthTestManager(stubPeerCallback{
		before: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			beforeCalled = true
			return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
		},
		check: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.CheckDecision, error) {
			checkCalled = true
			return mcp.CheckDecision{Decision: mcp.HookDecisionWarn}, nil
		},
		after: func(_ context.Context, _ mcp.LeaseKey, _ mcp.HookPayload) (mcp.AfterDecision, error) {
			afterCalled = true
			return mcp.AfterDecision{Decision: mcp.HookDecisionApprove}, nil
		},
	})
	subscribeDepthTestTopics(t, registry, TopicToolBefore, TopicToolAfter)

	beforeDecision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, depthTestPayload(2))
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if beforeDecision.Decision != mcp.HookDecisionAllow {
		t.Fatalf("DispatchBefore() decision = %q, want %q", beforeDecision.Decision, mcp.HookDecisionAllow)
	}

	checkDecision, err := manager.DispatchCheck(context.Background(), TopicToolBefore, depthTestPayload(2))
	if err != nil {
		t.Fatalf("DispatchCheck() error = %v", err)
	}
	if checkDecision.Decision != mcp.HookDecisionWarn {
		t.Fatalf("DispatchCheck() decision = %q, want %q", checkDecision.Decision, mcp.HookDecisionWarn)
	}

	afterDecision, err := manager.DispatchAfter(context.Background(), TopicToolAfter, depthTestPayload(2))
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if afterDecision.Decision != mcp.HookDecisionApprove {
		t.Fatalf("DispatchAfter() decision = %q, want %q", afterDecision.Decision, mcp.HookDecisionApprove)
	}

	if !beforeCalled {
		t.Fatal("DispatchBefore() did not invoke callback below max depth")
	}
	if !checkCalled {
		t.Fatal("DispatchCheck() did not invoke callback below max depth")
	}
	if !afterCalled {
		t.Fatal("DispatchAfter() did not invoke callback below max depth")
	}
}

func TestDepthIncrement(t *testing.T) {
	t.Parallel()

	gotDepth := 0
	manager, registry := newDepthTestManager(stubPeerCallback{
		after: func(_ context.Context, _ mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			gotDepth = payload.Depth
			return mcp.AfterDecision{Decision: mcp.HookDecisionApprove}, nil
		},
	})
	subscribeDepthTestTopics(t, registry, TopicToolAfter)

	decision, err := manager.DispatchAfter(context.Background(), TopicToolAfter, depthTestPayload(1))
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionApprove {
		t.Fatalf("DispatchAfter() decision = %q, want %q", decision.Decision, mcp.HookDecisionApprove)
	}
	if gotDepth != 2 {
		t.Fatalf("callback payload.Depth = %d, want 2", gotDepth)
	}
}

func newDepthTestManager(cb PeerCallback, opts ...ManagerOption) (*Manager, *HookRegistry) {
	registry := NewHookRegistry()
	dispatcher := NewHookDispatcher(registry, cb)
	resolver := NewHookResolver(&managerReviewStoreStub{})
	return NewManager(registry, dispatcher, resolver, opts...), registry
}

func subscribeDepthTestTopics(t *testing.T, registry *HookRegistry, topics ...string) {
	t.Helper()

	_, err := registry.Subscribe(mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}, mcp.HookSubscribeRequest{
		SubscriptionID: "depth-sub",
		Topics:         topics,
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
}

func depthTestPayload(depth int) mcp.HookPayload {
	return mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Depth:    depth,
	}
}
