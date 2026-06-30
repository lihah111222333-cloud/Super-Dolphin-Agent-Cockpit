package hooks

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestManagerDispatchAfterEscalatePersistsSubscriberLease(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	approveLease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	escalateLease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}

	subscribeHookTopic(t, registry, approveLease, TopicToolAfter, &mcp.SelectorScope{AgentID: "agent-2", ThreadID: "thread-2"})
	subscribeHookTopic(t, registry, escalateLease, TopicToolAfter, &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-1"})

	calledLeases := make([]mcp.LeaseKey, 0, 1)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		after: recordingAfterEscalation(&calledLeases, escalateLease),
	}, WithDispatcherParallelism(1))
	store := &managerReviewStoreStub{}
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

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
	if got.TTLMs != 30_000 {
		t.Fatalf("DispatchAfter() ttl_ms = %d, want %d", got.TTLMs, 30_000)
	}
	requireSavedReview(t, store, "lease-b/1", 30_000)
	requireCalledLeases(t, "after callback", calledLeases, []mcp.LeaseKey{escalateLease})
}

func subscribeHookTopic(t *testing.T, registry *HookRegistry, lease mcp.LeaseKey, topic string, scope *mcp.SelectorScope) {
	t.Helper()
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: lease.InstanceID,
		Topics:         []string{topic},
		Scope:          mcp.Selector{Scope: scope},
	}); err != nil {
		t.Fatalf("Subscribe(%s) error = %v", lease.InstanceID, err)
	}
}

func recordingAfterEscalation(called *[]mcp.LeaseKey, escalateLease mcp.LeaseKey) func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.AfterDecision, error) {
	return func(_ context.Context, lease mcp.LeaseKey, _ mcp.HookPayload) (mcp.AfterDecision, error) {
		*called = append(*called, lease)
		if lease == escalateLease {
			return mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, Reason: "needs review", TTLMs: 30_000}, nil
		}
		return mcp.AfterDecision{Decision: mcp.HookDecisionApprove}, nil
	}
}

func requireSavedReview(t *testing.T, store *managerReviewStoreStub, wantLease string, wantTTL int64) {
	t.Helper()
	if len(store.saved) != 1 {
		t.Fatalf("saved reviews = %d, want 1", len(store.saved))
	}
	if store.saved[0].SubscriberLease != wantLease {
		t.Fatalf("saved subscriber lease = %q, want %q", store.saved[0].SubscriberLease, wantLease)
	}
	if store.saved[0].HookCallID == "" {
		t.Fatal("saved HookCallID = empty, want generated value")
	}
	if gotTTL := store.saved[0].DeadlineAt.Sub(store.saved[0].CreatedAt).Milliseconds(); gotTTL != wantTTL {
		t.Fatalf("saved review ttl = %dms, want %dms", gotTTL, wantTTL)
	}
}

func TestManagerDispatchBeforeUsesPayloadScopeSelector(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	matchingLease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	otherLease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}

	for _, tc := range []struct {
		lease mcp.LeaseKey
		scope *mcp.SelectorScope
	}{
		{lease: matchingLease, scope: &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-1"}},
		{lease: otherLease, scope: &mcp.SelectorScope{AgentID: "agent-2", ThreadID: "thread-1"}},
	} {
		if _, err := registry.Subscribe(tc.lease, mcp.HookSubscribeRequest{
			SubscriptionID: tc.lease.InstanceID,
			Topics:         []string{TopicToolBefore},
			Scope:          mcp.Selector{Scope: tc.scope},
		}); err != nil {
			t.Fatalf("Subscribe(%s) error = %v", tc.lease.InstanceID, err)
		}
	}

	calledLeases := make([]mcp.LeaseKey, 0, 1)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, lease mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			calledLeases = append(calledLeases, lease)
			if lease == matchingLease {
				return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
			}
			return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, nil
		},
	}, WithDispatcherParallelism(1))
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &managerReviewStoreStub{}))

	decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionAllow {
		t.Fatalf("DispatchBefore() decision = %q, want %q", decision.Decision, mcp.HookDecisionAllow)
	}
	if len(calledLeases) != 1 || calledLeases[0] != matchingLease {
		t.Fatalf("DispatchBefore() called leases = %#v, want [%#v]", calledLeases, matchingLease)
	}
}

func TestDispatchAfter_EscalatePersistenceFailure_FailsClosed(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-escalate", Generation: 1}
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "escalate-sub",
		Topics:         []string{TopicToolAfter},
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			if payload.Topic != TopicToolAfter {
				t.Fatalf("payload.Topic = %q, want %q", payload.Topic, TopicToolAfter)
			}
			return mcp.AfterDecision{Decision: mcp.HookDecisionEscalate, Reason: "persist me"}, nil
		},
	}, WithDispatcherParallelism(1))

	persistErr := errors.New("hook store unavailable")
	store := &managerReviewStoreStub{
		savePendingReviewFunc: func(_ context.Context, review mcp.PendingHookReview) error {
			if review.SubscriberLease != "lease-escalate/1" {
				t.Fatalf("SubscriberLease = %q, want %q", review.SubscriberLease, "lease-escalate/1")
			}
			return persistErr
		},
	}
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

	decision, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{
		HookCallID: "call-escalate-fail",
		AgentID:    "agent-1",
		ThreadID:   "thread-1",
	})
	if err == nil {
		t.Fatal("DispatchAfter() error = nil, want persistence failure")
	}
	if !errors.Is(err, persistErr) {
		t.Fatalf("DispatchAfter() error = %v, want wrapped %v", err, persistErr)
	}
	if decision.Decision != mcp.HookDecisionReject {
		t.Fatalf("DispatchAfter() decision = %q, want %q", decision.Decision, mcp.HookDecisionReject)
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved reviews = %d, want 0 after persistence failure", len(store.saved))
	}
}

func TestDispatchBefore_PartialFailure_DeniesRequest(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	allowLease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	flakyLease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}
	subscribeHookTopic(t, registry, allowLease, TopicToolBefore, nil)
	subscribeHookTopic(t, registry, flakyLease, TopicToolBefore, nil)

	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: partialFailureBeforeCallback(&calledLeases, flakyLease),
	}, WithDispatcherParallelism(1))
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &managerReviewStoreStub{}))

	decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{})
	if err != nil {
		t.Fatalf("DispatchBefore() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore() decision = %q, want %q", decision.Decision, mcp.HookDecisionDeny)
	}
	requireCalledLeases(t, "DispatchBefore()", calledLeases, []mcp.LeaseKey{allowLease, flakyLease})

	requireHookFailureCount(t, dispatcher, flakyLease, 1)
	if _, ok := registry.GetSubscription(flakyLease); !ok {
		t.Fatal("DispatchBefore() unsubscribed flaky lease after single partial failure")
	}
}

func TestDispatchAfter_PartialFailure_PreservesSuccessfulDecision(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	approveLease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	flakyLease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}
	subscribeHookTopic(t, registry, approveLease, TopicToolAfter, nil)
	subscribeHookTopic(t, registry, flakyLease, TopicToolAfter, nil)

	var logs bytes.Buffer
	logger := pkglogger.New(pkglogger.NewTextHandler(&logs, nil))
	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		after: partialFailureAfterCallback(&calledLeases, flakyLease),
	}, WithDispatcherParallelism(1))
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &managerReviewStoreStub{}), WithManagerLogger(logger))

	decision, err := manager.DispatchAfter(context.Background(), TopicToolAfter, mcp.HookPayload{})
	if err != nil {
		t.Fatalf("DispatchAfter() error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionApprove {
		t.Fatalf("DispatchAfter() decision = %q, want %q", decision.Decision, mcp.HookDecisionApprove)
	}
	if decision.Reason != "approved" {
		t.Fatalf("DispatchAfter() reason = %q, want %q", decision.Reason, "approved")
	}
	requireCalledLeases(t, "DispatchAfter()", calledLeases, []mcp.LeaseKey{approveLease, flakyLease})

	logText := logs.String()
	requireLogContains(t, logText, "level=WARN")
	requireLogContains(t, logText, "partial after hook failure, keeping successful decision")
}

func partialFailureBeforeCallback(called *[]mcp.LeaseKey, flakyLease mcp.LeaseKey) func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.BeforeDecision, error) {
	return func(_ context.Context, gotLease mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
		*called = append(*called, gotLease)
		if gotLease == flakyLease {
			return mcp.BeforeDecision{}, errors.New("temporary peer failure")
		}
		return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
	}
}

func partialFailureAfterCallback(called *[]mcp.LeaseKey, flakyLease mcp.LeaseKey) func(context.Context, mcp.LeaseKey, mcp.HookPayload) (mcp.AfterDecision, error) {
	return func(_ context.Context, gotLease mcp.LeaseKey, _ mcp.HookPayload) (mcp.AfterDecision, error) {
		*called = append(*called, gotLease)
		if gotLease == flakyLease {
			return mcp.AfterDecision{}, errors.New("temporary peer failure")
		}
		return mcp.AfterDecision{Decision: mcp.HookDecisionApprove, Reason: "approved"}, nil
	}
}

func requireCalledLeases(t *testing.T, label string, got, want []mcp.LeaseKey) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s called leases = %#v, want %#v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s called leases = %#v, want %#v", label, got, want)
		}
	}
}

func requireHookFailureCount(t *testing.T, dispatcher *HookDispatcher, lease mcp.LeaseKey, want int) {
	t.Helper()
	dispatcher.failMu.Lock()
	failures := dispatcher.failCounts[lease]
	dispatcher.failMu.Unlock()
	if failures != want {
		t.Fatalf("hook lease failures = %d, want %d", failures, want)
	}
}

func requireLogContains(t *testing.T, logText, want string) {
	t.Helper()
	if !strings.Contains(logText, want) {
		t.Fatalf("logs = %q, want containing %q", logText, want)
	}
}
