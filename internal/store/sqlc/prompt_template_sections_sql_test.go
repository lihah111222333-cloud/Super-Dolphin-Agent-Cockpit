package sqlc

import (
	"strings"
	"testing"
)

func TestPromptTemplateSectionsSQLIncludesTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()

	assertSQLContainsAll(t, "listPromptTemplateSectionsByTemplate", listPromptTemplateSectionsByTemplate, []string{
		"created_at, updated_at, trigger_type, recall_topic",
	})
	assertSQLNotContains(t, "listPromptTemplateSectionsByTemplate", listPromptTemplateSectionsByTemplate, "enabled = TRUE")

	assertSQLContainsAll(t, "listPromptTemplateSectionsByTemplates", listPromptTemplateSectionsByTemplates, []string{
		"WHERE template_id IN (/*SLICE:template_ids*/?)",
		"ORDER BY template_id, region, ordinal, id",
		"created_at, updated_at, trigger_type, recall_topic",
	})
	assertSQLNotContains(t, "listPromptTemplateSectionsByTemplates", listPromptTemplateSectionsByTemplates, "enabled = TRUE")

	assertSQLContainsAll(t, "upsertPromptTemplateSection", upsertPromptTemplateSection, []string{
		"template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic, created_at, updated_at",
		"VALUES\n    (?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))",
		"trigger_type = EXCLUDED.trigger_type",
		"recall_topic = EXCLUDED.recall_topic",
		"updated_at   = (CAST(strftime('%s','now') AS INTEGER) * 1000)",
		"created_at, updated_at, trigger_type, recall_topic",
	})

	assertSQLContainsAll(t, "lockRecallTopicInCWD", lockRecallTopicInCWD, []string{
		"INSERT INTO prompt_recall_topics (cwd, topic, template_id, section_key)",
		"VALUES (?, ?, 0, '')",
		"ON CONFLICT (cwd, topic) DO UPDATE SET",
	})

	assertSQLContainsAll(t, "upsertPromptRecallTopicTargetInCWD", upsertPromptRecallTopicTargetInCWD, []string{
		"INSERT INTO prompt_recall_topics (cwd, topic, template_id, section_key)",
		"VALUES (?, ?, ?, ?)",
		"template_id = EXCLUDED.template_id",
		"section_key = EXCLUDED.section_key",
	})
}

func assertSQLContainsAll(t *testing.T, name, sql string, checks []string) {
	t.Helper()
	for _, check := range checks {
		if !strings.Contains(sql, check) {
			t.Fatalf("%s SQL missing %q:\n%s", name, check, sql)
		}
	}
}

func assertSQLNotContains(t *testing.T, name, sql, check string) {
	t.Helper()
	if strings.Contains(sql, check) {
		t.Fatalf("%s must return disabled rows for the UI editor:\n%s", name, sql)
	}
}
