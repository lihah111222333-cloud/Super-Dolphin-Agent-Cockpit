package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnterprisePromptRuntimeMetadataMigration0106(t *testing.T) {
	content := readEnterpriseRuntimeMetadataMigration(t)
	block := enterpriseRuntimeMetadataBlock(t, content)

	requireRuntimeMetadataContainsAll(t, block, "0106 enterprise metadata guard", []string{
		"Enterprise workflow preset discovery metadata",
		"WITH enterprise_metadata",
		"scope.global",
		"intent:enterprise_workflow",
		"workflow:enterprise",
		"schema:input_sources",
		"schema:output_structure",
		"schema:evidence",
		"schema:time_window",
		"schema:confidence",
		"schema:uncertainty",
		"schema:owner",
		"schema:next_step",
		"created_by IN ('system.seed', 'seed')",
		"updated_by IN ('system.seed', 'seed', 'migration')",
		"updated_by LIKE 'system.%'",
		"updated_by LIKE 'migration:%'",
		"manually_edited = FALSE",
	})
	if strings.Contains(block, "prompt_text") {
		t.Fatal("0106 enterprise metadata must not rewrite prompt_text")
	}
	for _, removed := range []string{"main/paper_summarizer", "main/topic_curator", "main/learning_card", "main/trip_briefer"} {
		if strings.Contains(block, removed) {
			t.Fatalf("0106 enterprise metadata must not revive removed preset %q", removed)
		}
	}

	for key, tokens := range enterpriseRuntimeMetadataTokens() {
		requireRuntimeMetadataContainsAll(t, block, key, append([]string{key}, tokens...))
	}
}

func TestEnterprisePromptRuntimeRollbackBlock0106(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "ROLLBACK.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ROLLBACK.md: %v", err)
	}
	block := string(data)
	for _, marker := range []string{
		"0106 data restore",
		"WITH enterprise_restore",
		"main/morning_briefer",
		"main/todo_prioritizer",
		"updated_by = 'system.seed'",
		"created_by IN ('system.seed', 'seed')",
		"manually_edited = FALSE",
	} {
		if !strings.Contains(block, marker) {
			t.Fatalf("0106 enterprise rollback block missing %q", marker)
		}
	}
}

func readEnterpriseRuntimeMetadataMigration(t *testing.T) string {
	t.Helper()

	path := filepath.Join(repoRoot(t), "migrations", "0106_prompt_template_runtime_metadata.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0106: %v", err)
	}
	return string(data)
}

func enterpriseRuntimeMetadataBlock(t *testing.T, content string) string {
	t.Helper()

	start := strings.Index(content, "Enterprise workflow preset discovery metadata")
	if start < 0 {
		t.Fatal("0106 missing enterprise metadata block")
	}
	end := strings.Index(content[start:], "\n-- Planning/review/debug methodology discovery metadata")
	if end < 0 {
		t.Fatal("0106 enterprise metadata block missing methodology boundary")
	}
	return content[start : start+end]
}

func enterpriseRuntimeMetadataTokens() map[string][]string {
	return map[string][]string{
		"main/morning_briefer":  {"brief:today_focus", "brief:risk"},
		"main/pr_summarizer":    {"pr:scope", "pr:behavior_impact", "pr:risk_area", "pr:review_focus"},
		"main/weekly_reviewer":  {"weekly:outcomes", "weekly:decisions", "weekly:blockers"},
		"main/data_inspector":   {"data:field_meaning", "data:outlier", "data:quality_issue"},
		"main/email_drafter":    {"email:recipient", "email:tone", "email:purpose", "email:action_request", "email:follow_up"},
		"main/health_reporter":  {"health:status", "health:anomaly", "health:impact"},
		"main/source_monitor":   {"source:change_summary", "source:trigger", "source:risk_level"},
		"main/note_organizer":   {"note:topic", "note:fact", "note:decision", "note:action_item"},
		"main/todo_prioritizer": {"todo:priority", "todo:dependency", "todo:blocker"},
	}
}
