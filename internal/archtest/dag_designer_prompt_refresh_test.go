package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDAGDesignerPromptRefresh_RunScopedUpdateNodeSignature(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0090_refresh_dag_designer_prompt_run_id_signature.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0090: %v", err)
	}
	content := string(data)
	for _, must := range []string{
		"main/dag_designer_zh",
		"main/dag_designer_en",
		"`task_update_node(dag_key, node_key, run_id, status, result?)`",
		"manually_edited = FALSE",
		"updated_by = 'migration:0090'",
	} {
		if !strings.Contains(content, must) {
			t.Errorf("migration 0090 missing prompt refresh marker %q", must)
		}
	}
}

func TestDAGDesignerPromptRefresh_FinalNodeKeySignature(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0108_refresh_dag_designer_prompt_final_node_key.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0108: %v", err)
	}
	content := string(data)
	for _, must := range []string{
		"main/dag_designer_zh",
		"main/dag_designer_en",
		"`task_create_dag(agent_id, dag_key, title, description?, schedule, final_node_key?, nodes?)`",
		"final_node_key",
		"run-level `metadata.final_output`",
		"Shared Files",
		"最终交付节点 final_node_key",
		"Final deliverable",
		"`final_node_key=\"review\"`",
		"manually_edited = FALSE",
		"updated_by = 'migration:0108'",
	} {
		if !strings.Contains(content, must) {
			t.Errorf("migration 0108 missing prompt refresh marker %q", must)
		}
	}
}
