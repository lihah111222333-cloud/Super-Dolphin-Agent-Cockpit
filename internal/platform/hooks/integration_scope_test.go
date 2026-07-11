package hooks

import (
	"context"
	"slices"
	"testing"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestIntegration_ScopedSubscriptionFiltering(t *testing.T) {
	t.Parallel()

	leaseA := mcp.LeaseKey{InstanceID: "lease-scope-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-scope-b", Generation: 1}
	runScopedIntegrationCase(t, scopedIntegrationCase{
		topic:         TopicToolBefore,
		leaseA:        leaseA,
		leaseB:        leaseB,
		subA:          "sub-scope-a",
		subB:          "sub-scope-b",
		scopeA:        &mcp.SelectorScope{AgentID: "agent-1"},
		scopeB:        &mcp.SelectorScope{},
		denyDecision:  scopedDenyDecision("agent-1 scoped block"),
		allowDecision: scopedAllowDecision(""),
		assertScopedPayload: func(t *testing.T, payload mcp.HookPayload) {
			if payload.AgentID != "agent-1" {
				t.Fatalf("payload.AgentID for scoped subscriber = %q, want %q", payload.AgentID, "agent-1")
			}
		},
		dispatches: []scopedDispatchExpectation{
			{
				label:        "agent-1",
				payload:      mcp.HookPayload{HookCallID: "call-scope-agent-1", AgentID: "agent-1"},
				wantLeases:   []mcp.LeaseKey{leaseA, leaseB},
				wantDecision: scopedDenyResultDecision("agent-1 scoped block"),
			},
			{
				label:        "agent-2",
				payload:      mcp.HookPayload{HookCallID: "call-scope-agent-2", AgentID: "agent-2"},
				wantLeases:   []mcp.LeaseKey{leaseB},
				wantDecision: scopedAllowDecision(""),
			},
		},
	})
}

func TestIntegration_ThreadScopedSubscription(t *testing.T) {
	t.Parallel()

	const topic = "agent.turn.before"
	leaseA := mcp.LeaseKey{InstanceID: "lease-thread-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-thread-b", Generation: 1}
	runScopedIntegrationCase(t, scopedIntegrationCase{
		topic:         topic,
		leaseA:        leaseA,
		leaseB:        leaseB,
		subA:          "sub-thread-a",
		subB:          "sub-thread-b",
		scopeA:        &mcp.SelectorScope{ThreadID: "thread-1"},
		scopeB:        &mcp.SelectorScope{},
		denyDecision:  scopedDenyDecision("thread-1 scoped block"),
		allowDecision: scopedAllowDecision(""),
		assertScopedPayload: func(t *testing.T, payload mcp.HookPayload) {
			if payload.ThreadID != "thread-1" {
				t.Fatalf("payload.ThreadID for scoped subscriber = %q, want %q", payload.ThreadID, "thread-1")
			}
		},
		dispatches: []scopedDispatchExpectation{
			{
				label:        "thread-1",
				payload:      mcp.HookPayload{HookCallID: "call-scope-thread-1", AgentID: "agent-thread", ThreadID: "thread-1"},
				wantLeases:   []mcp.LeaseKey{leaseA, leaseB},
				wantDecision: scopedDenyResultDecision("thread-1 scoped block"),
			},
			{
				label:        "thread-2",
				payload:      mcp.HookPayload{HookCallID: "call-scope-thread-2", AgentID: "agent-thread", ThreadID: "thread-2"},
				wantLeases:   []mcp.LeaseKey{leaseB},
				wantDecision: scopedAllowDecision(""),
			},
		},
	})
}

func TestIntegration_MultiDimensionScope(t *testing.T) {
	t.Parallel()

	const topic = "agent.turn.before"
	leaseA := mcp.LeaseKey{InstanceID: "lease-multi-a", Generation: 1}
	leaseB := mcp.LeaseKey{InstanceID: "lease-multi-b", Generation: 1}
	runScopedIntegrationCase(t, scopedIntegrationCase{
		topic:         topic,
		leaseA:        leaseA,
		leaseB:        leaseB,
		subA:          "sub-multi-a",
		subB:          "sub-multi-b",
		scopeA:        &mcp.SelectorScope{AgentID: "a1", ThreadID: "t1"},
		scopeB:        &mcp.SelectorScope{AgentID: "a1"},
		denyDecision:  scopedDenyDecision("a1/t1 scoped block"),
		allowDecision: scopedAllowDecision("agent wildcard allow"),
		assertScopedPayload: func(t *testing.T, payload mcp.HookPayload) {
			if payload.AgentID != "a1" || payload.ThreadID != "t1" {
				t.Fatalf("payload scope for multi subscriber = (%q,%q), want (%q,%q)", payload.AgentID, payload.ThreadID, "a1", "t1")
			}
		},
		dispatches: []scopedDispatchExpectation{
			{
				label:        "a1/t1",
				payload:      mcp.HookPayload{HookCallID: "call-scope-a1-t1", AgentID: "a1", ThreadID: "t1"},
				wantLeases:   []mcp.LeaseKey{leaseA, leaseB},
				wantDecision: scopedDenyResultDecision("a1/t1 scoped block"),
			},
			{
				label:        "a1/t2",
				payload:      mcp.HookPayload{HookCallID: "call-scope-a1-t2", AgentID: "a1", ThreadID: "t2"},
				wantLeases:   []mcp.LeaseKey{leaseB},
				wantDecision: scopedAllowDecision("agent wildcard allow"),
			},
		},
	})
}

type scopedIntegrationCase struct {
	topic               string
	leaseA              mcp.LeaseKey
	leaseB              mcp.LeaseKey
	subA                string
	subB                string
	scopeA              *mcp.SelectorScope
	scopeB              *mcp.SelectorScope
	denyDecision        mcp.BeforeDecision
	allowDecision       mcp.BeforeDecision
	assertScopedPayload func(*testing.T, mcp.HookPayload)
	dispatches          []scopedDispatchExpectation
}

type scopedDispatchExpectation struct {
	label        string
	payload      mcp.HookPayload
	wantLeases   []mcp.LeaseKey
	wantDecision mcp.BeforeDecision
}

func runScopedIntegrationCase(t *testing.T, tc scopedIntegrationCase) {
	t.Helper()

	registry := NewHookRegistry()
	mustSubscribeIntegrationHook(t, registry, tc.leaseA, tc.subA, tc.scopeA, tc.topic)
	mustSubscribeIntegrationHook(t, registry, tc.leaseB, tc.subB, tc.scopeB, tc.topic)

	calledLeases := make([]mcp.LeaseKey, 0, 2)
	dispatcher := mustNewHookDispatcher(
		t,
		registry,
		scopedIntegrationCallback(t, tc, &calledLeases),
		WithDispatcherParallelism(1),
	)
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, &stubHookReviewStore{}))
	for _, dispatch := range tc.dispatches {
		calledLeases = calledLeases[:0]
		got, err := manager.DispatchBefore(context.Background(), tc.topic, dispatch.payload)
		assertScopedDispatchResult(t, dispatch, calledLeases, got, err)
	}
}

func scopedIntegrationCallback(
	t *testing.T,
	tc scopedIntegrationCase,
	calledLeases *[]mcp.LeaseKey,
) stubPeerCallback {
	t.Helper()

	return stubPeerCallback{before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
		if payload.Topic != tc.topic {
			t.Fatalf("payload.Topic = %q, want %q", payload.Topic, tc.topic)
		}
		if payload.Depth != 1 {
			t.Fatalf("payload.Depth = %d, want 1", payload.Depth)
		}
		*calledLeases = append(*calledLeases, gotLease)

		switch gotLease {
		case tc.leaseA:
			tc.assertScopedPayload(t, payload)
			return tc.denyDecision, nil
		case tc.leaseB:
			return tc.allowDecision, nil
		default:
			t.Fatalf("unexpected lease = %#v", gotLease)
			return mcp.BeforeDecision{}, nil
		}
	}}
}

func assertScopedDispatchResult(
	t *testing.T,
	want scopedDispatchExpectation,
	calledLeases []mcp.LeaseKey,
	got mcp.BeforeDecision,
	err error,
) {
	t.Helper()

	if err != nil {
		t.Fatalf("DispatchBefore(%s) error = %v", want.label, err)
	}
	if !slices.Equal(calledLeases, want.wantLeases) {
		t.Fatalf("DispatchBefore(%s) called leases = %#v, want %#v", want.label, calledLeases, want.wantLeases)
	}
	if got.Decision != want.wantDecision.Decision {
		t.Fatalf("DispatchBefore(%s) decision = %q, want %q", want.label, got.Decision, want.wantDecision.Decision)
	}
	if got.Reason != want.wantDecision.Reason {
		t.Fatalf("DispatchBefore(%s) reason = %q, want %q", want.label, got.Reason, want.wantDecision.Reason)
	}
	if !slices.Equal(got.AllowedTools, want.wantDecision.AllowedTools) {
		t.Fatalf("DispatchBefore(%s) allowed tools = %#v, want %#v", want.label, got.AllowedTools, want.wantDecision.AllowedTools)
	}
	if !slices.Equal(got.DeniedTools, want.wantDecision.DeniedTools) {
		t.Fatalf("DispatchBefore(%s) denied tools = %#v, want %#v", want.label, got.DeniedTools, want.wantDecision.DeniedTools)
	}
}

func scopedDenyDecision(reason string) mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:     mcp.HookDecisionDeny,
		AllowedTools: []string{"read"},
		DeniedTools:  []string{"exec"},
		Reason:       reason,
	}
}

func scopedDenyResultDecision(reason string) mcp.BeforeDecision {
	decision := scopedDenyDecision(reason)
	decision.DeniedTools = []string{"exec", "network"}
	return decision
}

func scopedAllowDecision(reason string) mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:     mcp.HookDecisionAllow,
		AllowedTools: []string{"read", "write"},
		DeniedTools:  []string{"network"},
		Reason:       reason,
	}
}
