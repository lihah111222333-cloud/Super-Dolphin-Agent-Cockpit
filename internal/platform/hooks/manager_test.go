package hooks

import (
	"context"
	"errors"
	"testing"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestDepthExceedsMax_Before(t *testing.T) {
	t.Parallel()

	called := false
	manager, registry := newDepthTestManager(t, stubPeerCallback{
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

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{})
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
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

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
	subscribeHookTopic(t, registry, lease, TopicToolBefore, &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-1"})

	beforeCalls := 0
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: lostSubscriberBeforeCallback(t, lease, &beforeCalls),
	})

	cancelCalls := 0
	store := &managerReviewStoreStub{
		cancelPendingReviewsByLeaseFunc: lostSubscriberCancelCallback(t, registry, dispatcher, lease, &cancelCalls),
	}
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))
	payload := depthTestPayload(0)

	for attempt := 1; attempt <= 2; attempt++ {
		requireLostSubscriberAttempt(t, manager, registry, dispatcher, payload, lease, attempt, cancelCalls)
	}

	requireLostSubscriberAutoUnsubscribed(t, manager, registry, dispatcher, payload, lease)
	if cancelCalls != 1 {
		t.Fatalf("DispatchBefore() cancel calls = %d, want 1", cancelCalls)
	}

	requireLostSubscriberFourthDispatch(t, manager, payload)
	if beforeCalls != 3 {
		t.Fatalf("DispatchBefore() callback calls = %d, want 3 after auto-unsubscribe", beforeCalls)
	}
}

func lostSubscriberBeforeCallback(t *testing.T, wantLease mcp.LeaseKey, beforeCalls *int) func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.BeforeDecision, error) {
	t.Helper()
	return func(_ context.Context, gotLease mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
		*beforeCalls = *beforeCalls + 1
		if gotLease != wantLease {
			t.Fatalf("lease = %#v, want %#v", gotLease, wantLease)
		}
		return mcp.BeforeDecision{}, errors.New("subscriber lost")
	}
}

func lostSubscriberCancelCallback(t *testing.T, registry *HookRegistry, dispatcher *HookDispatcher, lease mcp.LeaseKey, cancelCalls *int) func(context.Context, string) (int, error) {
	t.Helper()
	return func(_ context.Context, subscriberLease string) (int, error) {
		*cancelCalls = *cancelCalls + 1
		if subscriberLease != "lease-lost/1" {
			t.Fatalf("CancelPendingReviewsByLease() lease = %q, want %q", subscriberLease, "lease-lost/1")
		}
		if _, ok := registry.GetSubscription(lease); ok {
			t.Fatal("subscription still present while cancelling lost subscriber")
		}
		requireHookFailureUntracked(t, dispatcher, lease)
		return 1, nil
	}
}

func requireLostSubscriberAttempt(t *testing.T, manager *Manager, registry *HookRegistry, dispatcher *HookDispatcher, payload mcp.HookPayload, lease mcp.LeaseKey, attempt, cancelCalls int) {
	t.Helper()
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
	requireHookFailureCount(t, dispatcher, lease, attempt)
	if cancelCalls != 0 {
		t.Fatalf("DispatchBefore() attempt %d cancel calls = %d, want 0", attempt, cancelCalls)
	}
}

func requireLostSubscriberAutoUnsubscribed(t *testing.T, manager *Manager, registry *HookRegistry, dispatcher *HookDispatcher, payload mcp.HookPayload, lease mcp.LeaseKey) {
	t.Helper()
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
	requireHookFailureUntracked(t, dispatcher, lease)
}

func requireLostSubscriberFourthDispatch(t *testing.T, manager *Manager, payload mcp.HookPayload) {
	t.Helper()
	if _, err := manager.DispatchBefore(context.Background(), TopicToolBefore, payload); err != nil {
		t.Fatalf("DispatchBefore() fourth attempt error = %v", err)
	}
}

func requireHookFailureUntracked(t *testing.T, dispatcher *HookDispatcher, lease mcp.LeaseKey) {
	t.Helper()
	dispatcher.failMu.Lock()
	_, tracked := dispatcher.failCounts[lease]
	dispatcher.failMu.Unlock()
	if tracked {
		t.Fatal("failure count still present for hook lease")
	}
}

func TestDepthExceedsMax_Check(t *testing.T) {
	t.Parallel()

	called := false
	manager, registry := newDepthTestManager(t, stubPeerCallback{
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
	manager, registry := newDepthTestManager(t, stubPeerCallback{
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
	manager, registry := newDepthTestManager(t, stubPeerCallback{
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
	manager, registry := newDepthTestManager(t, stubPeerCallback{
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
