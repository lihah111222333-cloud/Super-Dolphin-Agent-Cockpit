package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptRecallSeedMigration_CoversCatalogAndExpertGuidance(t *testing.T) {
	content := readPromptMigration(t, "0100_seed_recall_packs_and_when_to_use.sql")
	for _, topic := range []string{
		"lsp-basics",
		"lsp-advanced",
		"sqlc-workflow",
		"prompt-template-editing",
		"frontend-vue3",
		"migration-rules",
		"guard-rules",
	} {
		if strings.Count(content, "'"+topic+"'") != 1 {
			t.Fatalf("migration 0100 recall topic %q count = %d, want exactly 1", topic, strings.Count(content, "'"+topic+"'"))
		}
	}
	for _, marker := range []string{
		"prompt_key = 'main/general-zh'",
		"NULL::jsonb, TRUE, 'recall'",
		"ON CONFLICT (recall_topic) WHERE trigger_type = 'recall' AND recall_topic <> '' DO NOTHING",
		"BTRIM(p.when_to_use) = ''",
		"p.manually_edited = FALSE",
		"p.created_by IN ('system.seed', 'seed')",
		"'coder/prompt', '代码任务",
		"'main/sql', 'SQL 查询",
		"'main/pr_summarizer', 'PR 变更摘要",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("migration 0100 missing marker %q", marker)
		}
	}
}

func TestPromptMethodologyRuntimeMetadataMigration0106(t *testing.T) {
	content := readPromptMigration(t, "0106_prompt_template_runtime_metadata.sql")
	block := promptMethodologyRuntimeMetadataBlock(t, content)
	for _, marker := range []string{
		"WITH methodology_metadata",
		"main/planning",
		"main/code-review",
		"main/code-debug",
		"scope.global",
		"intent:expert",
		"created_by IN ('system.seed', 'seed')",
		"updated_by IN ('system.seed', 'seed', 'migration')",
		"updated_by LIKE 'migration:%'",
		"manually_edited = FALSE",
	} {
		if !strings.Contains(block, marker) {
			t.Fatalf("0106 methodology metadata block missing marker %q", marker)
		}
	}
	if strings.Contains(block, "prompt_text") {
		t.Fatal("0106 methodology metadata must not rewrite prompt_text")
	}
	for key, tokens := range promptMethodologyTokens() {
		for _, token := range append([]string{key}, tokens...) {
			if !strings.Contains(block, token) {
				t.Fatalf("0106 methodology metadata for %s missing %q", key, token)
			}
		}
	}
}

func TestPromptRecallSeedMigration_FailsFastWhenRequiredSeedRowsMissing(t *testing.T) {
	content := readPromptMigration(t, "0100_seed_recall_packs_and_when_to_use.sql")
	for _, forbidden := range []string{
		"intentionally insert zero rows",
		"intentionally inserts zero rows",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("migration 0100 must fail fast instead of documenting zero-row success: found %q", forbidden)
		}
	}
	for _, marker := range []string{
		"DO $$",
		"RAISE EXCEPTION '0100 requires prompt_template main/general-zh; apply 0095 first'",
		"RAISE EXCEPTION '0100 requires LSP basics source section before seeding recall_lsp_basics'",
		"RAISE EXCEPTION '0100 requires LSP advanced source section before seeding recall_lsp_advanced'",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("migration 0100 missing fail-fast marker %q", marker)
		}
	}
}

func TestPromptDefaultSeeds_DoNotMentionRetiredClassifier(t *testing.T) {
	for _, name := range []string{
		"0091_seed_main_default_prompt.sql",
		"0095_rename_claude_style_templates.sql",
	} {
		content := readPromptMigration(t, name)
		for _, forbidden := range []string{"分类器", "classifier", "use_classifier"} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s must not mention retired classifier marker %q", name, forbidden)
			}
		}
	}
}

func promptMethodologyRuntimeMetadataBlock(t *testing.T, content string) string {
	t.Helper()
	start := strings.Index(content, "Planning/review/debug methodology discovery metadata")
	if start < 0 {
		t.Fatal("0106 missing methodology metadata block")
	}
	end := strings.Index(content[start:], "\n-- Default developer expert roster repair")
	if end < 0 {
		t.Fatal("0106 methodology metadata block missing roster repair boundary")
	}
	return content[start : start+end]
}

func promptMethodologyTokens() map[string][]string {
	return map[string][]string{
		"main/planning":    {"method:planning", "phase:requirements", "phase:design", "phase:task_breakdown", "needs:user_confirmation", "output:handoff_plan", "evidence:acceptance_link"},
		"main/code-review": {"method:code_review", "review:findings_first", "review:severity", "review:file_line", "review:evidence_type", "review:test_gap"},
		"main/code-debug":  {"method:debug", "debug:error_evidence", "debug:minimal_repro", "debug:root_cause", "debug:verification", "debug:unverified_boundary"},
	}
}

func readPromptMigration(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(data)
}
