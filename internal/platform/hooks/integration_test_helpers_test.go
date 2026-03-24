package hooks

import (
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

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
