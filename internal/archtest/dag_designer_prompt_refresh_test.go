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
