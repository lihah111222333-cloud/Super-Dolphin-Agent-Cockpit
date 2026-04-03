package hooks

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestIntegration_LostSubscriberAutoCleanup(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-lost", Generation: 1}
	mustSubscribeIntegrationHook(t, registry, lease, "sub-lost", nil, TopicToolAfter)

	afterCalls := 0
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
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
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

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

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
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
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

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

func TestIntegration_ManagerWiringAndThinWrappers(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{}, WithDispatcherParallelism(1))
	store := &stubHookReviewStore{
		listPendingReviewsResult: []mcp.PendingHookReview{{HookCallID: "call-pending", AgentID: "agent-wire"}},
	}
	logger := pkglogger.New(pkglogger.NewTextHandler(io.Discard, nil))
	resolver := mustNewHookResolver(t, store)
	manager, err := provideManager(managerIn{
		Registry:   registry,
		Dispatcher: dispatcher,
		Resolver:   resolver,
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("provideManager() error = %v", err)
	}

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
