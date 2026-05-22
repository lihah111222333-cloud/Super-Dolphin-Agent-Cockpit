package sqlc

import (
	"strings"
	"testing"
)

func TestUpsertPromptTemplateSQLWhenToUseOrder(t *testing.T) {
	t.Parallel()

	checks := []string{
		"variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority",
		") VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12::jsonb, $13, $14, $15, NOW())",
		"when_to_use = EXCLUDED.when_to_use",
		"RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority",
	}
	for _, check := range checks {
		if !strings.Contains(upsertPromptTemplate, check) {
			t.Fatalf("upsertPromptTemplate SQL missing %q:\n%s", check, upsertPromptTemplate)
		}
	}
}
