package hooks

import (
	"context"
	"errors"
	"testing"
	"time"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
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

func subscribeHookForTest(t *testing.T, registry *HookRegistry, lease mcp.LeaseKey, subID, topic string) {
	t.Helper()
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: subID,
		Topics:         []string{topic},
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
}

func subscribeSelectorForTest(t *testing.T, registry *HookRegistry, lease mcp.LeaseKey, scope *mcp.SelectorScope) {
	t.Helper()
	if _, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: lease.InstanceID,
		Topics:         []string{TopicToolBefore},
		Scope:          mcp.Selector{Scope: scope},
	}); err != nil {
		t.Fatalf("Subscribe(%s) error = %v", lease.InstanceID, err)
	}
}

func assertCheckCallbackInputs(t *testing.T, gotLease, wantLease mcp.LeaseKey, payload mcp.HookPayload) {
	t.Helper()
	if gotLease != wantLease {
		t.Fatalf("lease = %#v, want %#v", gotLease, wantLease)
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
}

func assertSingleCheckDecision(t *testing.T, decisions []peerDecision[mcp.CheckDecision], want mcp.CheckDecision) {
	t.Helper()
	if len(decisions) != 1 {
		t.Fatalf("DispatchCheck() len = %d, want 1", len(decisions))
	}
	if decisions[0].Err != nil {
		t.Fatalf("DispatchCheck() peer err = %v, want nil", decisions[0].Err)
	}
	if decisions[0].Decision.Decision != want.Decision {
		t.Fatalf("DispatchCheck() decision = %q, want %q", decisions[0].Decision.Decision, want.Decision)
	}
	if decisions[0].Decision.Severity != want.Severity {
		t.Fatalf("DispatchCheck() severity = %q, want %q", decisions[0].Decision.Severity, want.Severity)
	}
	if decisions[0].ConsecutiveFailures != 0 {
		t.Fatalf("DispatchCheck() failures = %d, want 0", decisions[0].ConsecutiveFailures)
	}
}

func assertSelectorFilteredCheck(t *testing.T, decisions []peerDecision[mcp.CheckDecision], called []mcp.LeaseKey, want mcp.LeaseKey) {
	t.Helper()
	if len(decisions) != 1 {
		t.Fatalf("dispatchCheckBySelector() len = %d, want 1", len(decisions))
	}
	if decisions[0].Lease != want {
		t.Fatalf("dispatchCheckBySelector() lease = %#v, want %#v", decisions[0].Lease, want)
	}
	if len(called) != 1 {
		t.Fatalf("dispatchCheckBySelector() called leases = %#v, want [%#v]", called, want)
	}
	if called[0] != want {
		t.Fatalf("dispatchCheckBySelector() called leases = %#v, want [%#v]", called, want)
	}
}

func assertAfterCallbackInputs(t *testing.T, gotLease, wantLease mcp.LeaseKey, payload mcp.HookPayload) {
	t.Helper()
	if gotLease != wantLease {
		t.Fatalf("lease = %#v, want %#v", gotLease, wantLease)
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
}

func assertSingleAfterDecision(t *testing.T, decisions []peerDecision[mcp.AfterDecision]) {
	t.Helper()
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

func assertBeforeFailure(t *testing.T, label string, got []peerDecision[mcp.BeforeDecision], wantErr error, wantFailures int) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("DispatchBefore(%s) len = %d, want 1", label, len(got))
	}
	if !errors.Is(got[0].Err, wantErr) {
		t.Fatalf("DispatchBefore(%s) peer err = %v, want boom", label, got[0].Err)
	}
	if got[0].ConsecutiveFailures != wantFailures {
		t.Fatalf("DispatchBefore(%s) failures = %d, want %d", label, got[0].ConsecutiveFailures, wantFailures)
	}
}

func dispatchBeforeForTest(t *testing.T, dispatcher *HookDispatcher, label string, payload mcp.HookPayload) []peerDecision[mcp.BeforeDecision] {
	t.Helper()
	got, err := dispatcher.DispatchBefore(context.Background(), TopicToolBefore, payload)
	if err != nil {
		t.Fatalf("DispatchBefore(%s) error = %v", label, err)
	}
	return got
}

func TestHookDispatcherDispatchBeforeReturnsEmptyWithoutSubscribers(t *testing.T) {
	t.Parallel()

	dispatcher := mustNewHookDispatcher(t, NewHookRegistry(), stubPeerCallback{})
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

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
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

	dispatcher := mustNewHookDispatcher(
		t,
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

func TestMarkDispatchWorkerPanicResultMarksFailure(t *testing.T) {
	t.Parallel()

	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	dispatcher := &HookDispatcher{failCounts: map[mcp.LeaseKey]int{lease: 2}}
	results := make([]peerDecision[mcp.BeforeDecision], 1)

	markDispatchWorkerPanicResult(dispatcher, results, dispatchJob{index: 0, lease: lease}, true, "boom")

	if results[0].Lease != lease {
		t.Fatalf("marked lease = %#v, want %#v", results[0].Lease, lease)
	}
	if !errors.Is(results[0].Err, errDispatchWorkerPanic) {
		t.Fatalf("marked err = %v, want sentinel", results[0].Err)
	}
	if results[0].ConsecutiveFailures != 3 {
		t.Fatalf("marked failures = %d, want 3", results[0].ConsecutiveFailures)
	}

	merged := MergeBefore(results)
	if len(merged.FailedLeases) != 1 || merged.FailedLeases[0] != lease {
		t.Fatalf("MergeBefore() failed leases = %#v, want [%#v]", merged.FailedLeases, lease)
	}
	if len(merged.LostLeases) != 1 || merged.LostLeases[0] != lease {
		t.Fatalf("MergeBefore() lost leases = %#v, want [%#v]", merged.LostLeases, lease)
	}
}

func TestDispatchCheck_Success(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	subscribeHookForTest(t, registry, lease, "sub-check", TopicToolBefore)

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		check: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error) {
			assertCheckCallbackInputs(t, gotLease, lease, payload)
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
	assertSingleCheckDecision(t, decisions, mcp.CheckDecision{Decision: mcp.HookDecisionWarn, Severity: "high"})
}

func TestHookDispatcherDispatchCheckBySelectorFiltersScope(t *testing.T) {
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
		subscribeSelectorForTest(t, registry, tc.lease, tc.scope)
	}

	calledLeases := make([]mcp.LeaseKey, 0, 1)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		check: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error) {
			calledLeases = append(calledLeases, gotLease)
			assertCheckCallbackInputs(t, gotLease, matchingLease, payload)
			return mcp.CheckDecision{Decision: mcp.HookDecisionWarn}, nil
		},
	}, WithDispatcherParallelism(1))

	decisions, err := dispatcher.dispatchCheckBySelector(context.Background(), mcp.Selector{
		Subscription: TopicToolBefore,
		Scope:        &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-1"},
	}, mcp.HookPayload{})
	if err != nil {
		t.Fatalf("dispatchCheckBySelector() error = %v", err)
	}
	assertSelectorFilteredCheck(t, decisions, calledLeases, matchingLease)
}

func TestDispatchAfter_Success(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	subscribeHookForTest(t, registry, lease, "sub-after", TopicToolAfter)

	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		after: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error) {
			assertAfterCallbackInputs(t, gotLease, lease, payload)
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
	assertSingleAfterDecision(t, decisions)
}

func TestForgetLease(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	subscribeHookForTest(t, registry, lease, "sub-a", TopicToolBefore)

	someErr := errors.New("boom")
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, _ mcp.HookPayload) (mcp.BeforeDecision, error) {
			if gotLease != lease {
				t.Fatalf("lease = %#v, want %#v", gotLease, lease)
			}
			return mcp.BeforeDecision{}, someErr
		},
	}, WithDispatcherParallelism(1))

	payload := mcp.HookPayload{AgentID: "agent-1", ThreadID: "thread-1"}
	assertBeforeFailure(t, "first", dispatchBeforeForTest(t, dispatcher, "first", payload), someErr, 1)
	assertBeforeFailure(t, "second", dispatchBeforeForTest(t, dispatcher, "second", payload), someErr, 2)

	dispatcher.ForgetLease(lease)

	assertBeforeFailure(t, "third", dispatchBeforeForTest(t, dispatcher, "third", payload), someErr, 1)
}
