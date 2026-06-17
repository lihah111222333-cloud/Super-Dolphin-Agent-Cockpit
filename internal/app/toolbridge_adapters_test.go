package app

import (
	"testing"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func TestToolCallBindingFromStoreCarriesParentAgentID(t *testing.T) {
	got := toolCallBindingFromStore(&bindingstore.Binding{
		AgentID:       " agent-child ",
		ParentAgentID: " agent-root ",
	})
	if got.AgentID != "agent-child" {
		t.Fatalf("AgentID = %q, want agent-child", got.AgentID)
	}
	if got.ParentAgentID != "agent-root" {
		t.Fatalf("ParentAgentID = %q, want agent-root", got.ParentAgentID)
	}
}
