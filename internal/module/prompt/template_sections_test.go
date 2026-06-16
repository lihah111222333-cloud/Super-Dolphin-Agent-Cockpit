package prompt

import (
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestPromptAssetTemplatesForCWDPreferProjectAssets verifies project-scoped assets shadow global duplicates.
func TestPromptAssetTemplatesForCWDPreferProjectAssets(t *testing.T) {
	t.Parallel()

	templates := []contract.PromptTemplate{
		{
			ID:        1,
			Title:     "Memory",
			AgentKey:  "explore",
			Tags:      promptTestTags(t, "intent:recall", "scope.global"),
			UpdatedBy: promptUpdatedBy,
			Priority:  100,
		},
		{
			ID:        2,
			Title:     "Memory",
			AgentKey:  "explore",
			Tags:      promptTestTags(t, "intent:recall", "scope.cwd:/repo"),
			UpdatedBy: promptUpdatedBy,
			Priority:  1,
		},
		{
			ID:        3,
			Title:     "Default Rules",
			AgentKey:  "default_rule",
			Tags:      promptTestTags(t, "intent:default_rule", "scope.cwd:/repo"),
			UpdatedBy: promptUpdatedBy,
		},
		{
			ID:        4,
			Title:     "Other Project",
			AgentKey:  "explore",
			Tags:      promptTestTags(t, "intent:recall", "scope.cwd:/elsewhere"),
			UpdatedBy: promptUpdatedBy,
		},
		{
			ID:        5,
			Title:     "System Managed",
			AgentKey:  "explore",
			Tags:      promptTestTags(t, "intent:recall", "scope.cwd:/repo", "builtin:system"),
			UpdatedBy: promptUpdatedBy,
		},
	}

	got := promptAssetTemplatesForCWD(templates, "/repo")
	if len(got) != 2 {
		t.Fatalf("promptAssetTemplatesForCWD() len = %d, want 2: %#v", len(got), got)
	}
	if got[0].ID != 2 || got[1].ID != 3 {
		t.Fatalf("promptAssetTemplatesForCWD() IDs = [%d %d], want [2 3]", got[0].ID, got[1].ID)
	}
}

func promptTestTags(t *testing.T, tags ...string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(tags)
	if err != nil {
		t.Fatalf("json.Marshal(tags) error = %v", err)
	}
	return raw
}
