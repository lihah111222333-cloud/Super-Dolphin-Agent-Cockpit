package hooks

import (
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
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

	subscribers := registry.GetSubscribers("topic/a")
	if len(subscribers) != 1 || subscribers[0] != lease {
		t.Fatalf("GetSubscribers(topic/a) = %#v, want [%#v]", subscribers, lease)
	}
	subscription, ok := registry.GetSubscription(lease)
	if !ok {
		t.Fatal("GetSubscription() ok = false, want true")
	}
	if got, want := subscription.Topics, []string{"topic/a", "topic/b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("subscription topics = %#v, want %#v", got, want)
	}
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
	if got := registry.GetSubscribers("topic/b"); len(got) != 1 || got[0] != lease {
		t.Fatalf("GetSubscribers(topic/b) = %#v, want [%#v]", got, lease)
	}
}
