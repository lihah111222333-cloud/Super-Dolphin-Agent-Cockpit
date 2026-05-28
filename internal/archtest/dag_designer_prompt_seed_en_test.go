package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDAGDesignerPromptSeed_ENCoversCoreSurface guards the F7.2 English prompt
// seed migration from being hollowed out by later refactors or field refreshes.
//
// Strategy: read the migration SQL file directly instead of querying a database.
// Once the seed is merged, the file is the source of truth; deleting any core
// tool surface, section anchor, or node_type schema keyword should make this test
// fail and force maintainers to update the designer prompt deliberately.
//
// Anchors: docs/plans/dag改造实施计划.md §3 F7.2; blueprint v2 §AI Designer.
func TestDAGDesignerPromptSeed_ENCoversCoreSurface(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0085_seed_dag_designer_prompt_en.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0085: %v", err)
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		t.Fatalf("migration 0085 is empty; F7.2 seed must contain a prompt body")
	}

	assertDAGDesignerPromptIdentity(t, content)
	assertDAGDesignerPromptToolSurface(t, content)
	assertDAGDesignerPromptSections(t, content)
	assertDAGDesignerPromptTypedSchemas(t, content)
	assertDAGDesignerPromptBlueprintRules(t, content)
	assertDAGDesignerPromptRoutingTags(t, content)
	assertDAGDesignerPromptSize(t, content)
}

func assertDAGDesignerPromptIdentity(t *testing.T, content string) {
	t.Helper()
	// Identity markers: prompt_key / agent_key / English title / idempotency guard.
	assertContainsAll(t, content, "missing identity marker", []string{
		"main/dag_designer_en",
		"'dag_designer'",
		"AI Flow Designer (English)",
		"ON CONFLICT (prompt_key) DO NOTHING",
	})
}

func assertDAGDesignerPromptToolSurface(t *testing.T, content string) {
	t.Helper()
	// MCP tool surface: reviewers should immediately see which tools the designer
	// is allowed to use. Quiet deletion of any one is prompt degradation.
	assertContainsAll(t, content, "must reference MCP tool", []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_dag_apply_ops",
		"task_update_node",
		"task_get_dag",
		"task_get_run",
		"task_list_runs",
	})
}

func assertDAGDesignerPromptSections(t *testing.T, content string) {
	t.Helper()
	// English section anchors: keep the usable prompt structure intact.
	assertContainsAll(t, content, "must keep English section anchor", []string{
		"# Your Work Loop",
		"# Available MCP Tools (mcp-orch)",
		"## Resource Discovery",
		"## DAG Writes",
		"## DAG Reads",
		"# Node Typed Schema",
		"# Blueprint v2 Guardrails",
		"# Example Conversation",
		"# Style",
	})
}

func assertDAGDesignerPromptTypedSchemas(t *testing.T, content string) {
	t.Helper()
	// The three node_type typed schemas are the S5.1 contract entry points.
	assertContainsAll(t, content, "must describe typed schema", []string{
		`node_type = "agent"`,
		`node_type = "automation"`,
		`node_type = "hybrid"`,
	})
}

func assertDAGDesignerPromptBlueprintRules(t *testing.T, content string) {
	t.Helper()
	// Blueprint v2 constraints that the English prompt must not lose.
	assertContainsAll(t, content, "must keep blueprint rule keyword", []string{
		"base_version", // OCC optimistic lock
		"running",      // dynamic rewrite constrained state
		"FailureClass", // intelligent retry classification
		"ErrVersionConflict",
		"4KB",       // size_cap / sharedfile decision
		"scheduled", // trigger mode
		"cron",      // cron expression context
		"inputs.summarization",
		"final_node_key",
		"final_output",
	})
}

func assertDAGDesignerPromptRoutingTags(t *testing.T, content string) {
	t.Helper()
	// Routing tags are intentionally kept aligned with the F7.1 seed fields.
	assertContainsAll(t, content, "tags missing routing keyword", []string{
		"设计 DAG",
		"流程编排",
		"定时任务",
	})

	// English routing tags must keep the English seed discoverable directly.
	assertContainsAll(t, content, "tags missing English routing keyword", []string{
		"AI design flow",
		"schedule task",
		"cron expression",
	})
}

func assertDAGDesignerPromptSize(t *testing.T, content string) {
	t.Helper()
	// Size guard: a useful prompt should not be suspiciously tiny; over 600 lines
	// is only logged to avoid blocking deliberate prompt growth.
	lines := strings.Count(content, "\n")
	if lines < 60 {
		t.Errorf("migration 0085 only %d lines; prompt body looks suspiciously short", lines)
	}
	if lines > 600 {
		t.Logf("migration 0085 has %d lines; consider whether prompt body grew bloat", lines)
	}
}

func assertContainsAll(t *testing.T, content, failure string, values []string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(content, value) {
			t.Errorf("migration 0085 %s %q", failure, value)
		}
	}
}
