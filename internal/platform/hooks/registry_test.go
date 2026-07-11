package hooks

import (
	"slices"
	"testing"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestHookRegistrySubscribeRejectsEmptySubscriptionID(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}

	_, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "   ",
		Topics:         []string{"hook.before"},
	})
	if err == nil {
		t.Fatal("Subscribe() error = nil, want non-nil")
	}
}

func TestHookRegistrySubscribeIsIdempotentForEquivalentRequest(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	firstReq := mcp.HookSubscribeRequest{
		SubscriptionID: "sub-a",
		Topics:         []string{" topic/b ", "topic/a", "topic/b"},
		Scope:          mcp.Selector{Subscription: "runtime"},
		Filters:        []byte("{\"b\":2,\"a\":1}"),
		Mode:           "sync",
	}

	firstResp, err := registry.Subscribe(lease, firstReq)
	if err != nil {
		t.Fatalf("Subscribe(first) error = %v", err)
	}
	secondResp, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: " sub-a ",
		Topics:         []string{"topic/b", "topic/a"},
		Scope:          mcp.Selector{Subscription: "runtime"},
		Filters:        []byte("{\"a\":1,\"b\":2}"),
		Mode:           " sync ",
	})
	if err != nil {
		t.Fatalf("Subscribe(second) error = %v", err)
	}

	if firstResp.SubscriptionVersion != 1 {
		t.Fatalf("first version = %d, want 1", firstResp.SubscriptionVersion)
	}
	if secondResp.SubscriptionVersion != firstResp.SubscriptionVersion {
		t.Fatalf("second version = %d, want %d", secondResp.SubscriptionVersion, firstResp.SubscriptionVersion)
	}

	assertLeaseKeys(t, registry.GetSubscribers("topic/a"), lease)
	subscription, ok := registry.GetSubscription(lease)
	if !ok {
		t.Fatal("GetSubscription() ok = false, want true")
	}
	assertHookSubscriptionTopics(t, subscription.Topics, "topic/a", "topic/b")
}

func TestHookRegistrySubscribeIncrementsVersionOnRequestChange(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}

	_, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-a",
		Topics:         []string{"topic/a"},
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("Subscribe(first) error = %v", err)
	}

	resp, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-a",
		Topics:         []string{"topic/a", "topic/b"},
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("Subscribe(second) error = %v", err)
	}
	if resp.SubscriptionVersion != 2 {
		t.Fatalf("second version = %d, want 2", resp.SubscriptionVersion)
	}
	assertLeaseKeys(t, registry.GetSubscribers("topic/b"), lease)
}

func TestHookRegistryGetSubscribersBySelectorFiltersScope(t *testing.T) {
	t.Parallel()

	registry := NewHookRegistry()
	fullScopeLease := mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}
	agentOnlyLease := mcp.LeaseKey{InstanceID: "lease-b", Generation: 1}
	otherThreadLease := mcp.LeaseKey{InstanceID: "lease-c", Generation: 1}
	otherLease := mcp.LeaseKey{InstanceID: "lease-d", Generation: 1}

	subscribe := func(lease mcp.LeaseKey, scope *mcp.SelectorScope) {
		t.Helper()
		_, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
			SubscriptionID: lease.InstanceID,
			Topics:         []string{"topic/a"},
			Scope:          mcp.Selector{Scope: scope},
		})
		if err != nil {
			t.Fatalf("Subscribe(%s) error = %v", lease.InstanceID, err)
		}
	}

	subscribe(fullScopeLease, &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-1"})
	subscribe(agentOnlyLease, &mcp.SelectorScope{AgentID: "agent-1"})
	subscribe(otherThreadLease, &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-2"})
	subscribe(otherLease, &mcp.SelectorScope{AgentID: "agent-2", ThreadID: "thread-1"})

	got := registry.GetSubscribersBySelector(mcp.Selector{
		Subscription: "topic/a",
		Scope:        &mcp.SelectorScope{AgentID: "agent-1"},
	})
	assertLeaseKeys(t, got, agentOnlyLease)

	got = registry.GetSubscribersBySelector(mcp.Selector{
		Subscription: "topic/a",
		Scope:        &mcp.SelectorScope{AgentID: "agent-1", ThreadID: "thread-1"},
	})
	assertLeaseKeys(t, got, fullScopeLease, agentOnlyLease)

	got = registry.GetSubscribers("topic/a")
	assertLeaseKeys(t, got, fullScopeLease, agentOnlyLease, otherThreadLease, otherLease)
}

func assertLeaseKeys(t *testing.T, got []mcp.LeaseKey, want ...mcp.LeaseKey) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("lease keys = %#v, want %#v", got, want)
	}
}

func assertHookSubscriptionTopics(t *testing.T, got []string, want ...string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("subscription topics = %#v, want %#v", got, want)
	}
}
