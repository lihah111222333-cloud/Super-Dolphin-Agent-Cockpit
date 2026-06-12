package sqlc

import (
	"strings"
	"testing"
)

func TestPromptTemplateSectionsSQLIncludesTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()

	listChecks := []string{
		"created_at, updated_at, trigger_type, recall_topic",
	}
	for _, check := range listChecks {
		if !strings.Contains(listPromptTemplateSectionsByTemplate, check) {
			t.Fatalf("listPromptTemplateSectionsByTemplate SQL missing %q:\n%s", check, listPromptTemplateSectionsByTemplate)
		}
	}
	if strings.Contains(listPromptTemplateSectionsByTemplate, "enabled = TRUE") {
		t.Fatalf("listPromptTemplateSectionsByTemplate must return disabled rows for the UI editor:\n%s", listPromptTemplateSectionsByTemplate)
	}

	batchChecks := []string{
		"WHERE template_id IN (/*SLICE:template_ids*/?)",
		"ORDER BY template_id, region, ordinal, id",
		"created_at, updated_at, trigger_type, recall_topic",
	}
	for _, check := range batchChecks {
		if !strings.Contains(listPromptTemplateSectionsByTemplates, check) {
			t.Fatalf("listPromptTemplateSectionsByTemplates SQL missing %q:\n%s", check, listPromptTemplateSectionsByTemplates)
		}
	}
	if strings.Contains(listPromptTemplateSectionsByTemplates, "enabled = TRUE") {
		t.Fatalf("listPromptTemplateSectionsByTemplates must return disabled rows for the UI editor:\n%s", listPromptTemplateSectionsByTemplates)
	}

	upsertChecks := []string{
		"template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic",
		"VALUES\n    (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"trigger_type = EXCLUDED.trigger_type",
		"recall_topic = EXCLUDED.recall_topic",
		"updated_at   = (CAST(strftime('%s','now') AS INTEGER) * 1000)",
		"created_at, updated_at, trigger_type, recall_topic",
	}
	for _, check := range upsertChecks {
		if !strings.Contains(upsertPromptTemplateSection, check) {
			t.Fatalf("upsertPromptTemplateSection SQL missing %q:\n%s", check, upsertPromptTemplateSection)
		}
	}
}
