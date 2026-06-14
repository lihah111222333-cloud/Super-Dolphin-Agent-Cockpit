package sqlc

import (
	"strings"
	"testing"
)

func TestUpsertPromptTemplateSQLWhenToUseOrder(t *testing.T) {
	t.Parallel()

	checks := []string{
		"INSERT INTO prompt_templates (",
		"variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority",
		"created_by, updated_by, created_at, updated_at",
		") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))",
		"when_to_use = EXCLUDED.when_to_use",
		"match_when = EXCLUDED.match_when",
		"updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)",
		"RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text,",
		"CAST(variables AS BLOB) AS variables, CAST(tags AS BLOB) AS tags",
		"CAST(match_when AS BLOB) AS match_when, priority",
	}
	for _, check := range checks {
		if !strings.Contains(upsertPromptTemplate, check) {
			t.Fatalf("upsertPromptTemplate SQL missing %q:\n%s", check, upsertPromptTemplate)
		}
	}

	if got := strings.Count(upsertPromptTemplate, "CAST(strftime('%s','now') AS INTEGER) * 1000"); got != 3 {
		t.Fatalf("upsertPromptTemplate SQL should use SQLite millisecond timestamps for insert created_at, insert updated_at, and update updated_at, got %d occurrences:\n%s", got, upsertPromptTemplate)
	}
}
