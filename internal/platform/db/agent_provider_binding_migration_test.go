package db

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readMigrationFixture(t *testing.T, name string) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(content)
}

func assertMigrationContains(t *testing.T, name string, checks []string) {
	t.Helper()

	content := readMigrationFixture(t, name)
	for _, needle := range checks {
		if !strings.Contains(content, needle) {
			t.Fatalf("%s missing %q", name, needle)
		}
	}
}

func TestBaselineAgentProviderBindingIncludesConflictTargetSupport(t *testing.T) {
	t.Parallel()

	content := readMigrationFixture(t, "001_baseline.sql")
	checks := []string{
		"CONSTRAINT pk_agent_provider_binding PRIMARY KEY (agent_id)",
		"CONSTRAINT chk_agent_provider_binding_provider_not_empty CHECK ((provider <> ''::text))",
		"session_uuid text DEFAULT ''::text NOT NULL",
		"CREATE UNIQUE INDEX uq_agent_provider_binding_provider_thread ON public.agent_provider_binding USING btree (provider, provider_thread_id) WHERE (provider_thread_id <> ''::text);",
		"IF OLD.provider_thread_id <> '' AND NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN",
	}
	for _, needle := range checks {
		if !strings.Contains(content, needle) {
			t.Fatalf("001_baseline.sql missing %q", needle)
		}
	}
}

func TestRepairMigrationRebuildsAgentProviderBindingConstraints(t *testing.T) {
	t.Parallel()

	content := readMigrationFixture(t, "0029_agent_provider_binding_schema_repair.sql")
	checks := []string{
		"ADD COLUMN IF NOT EXISTS session_uuid TEXT NOT NULL DEFAULT ''",
		"DROP CONSTRAINT IF EXISTS uq_agent_provider_binding_provider_thread",
		"PARTITION BY provider, provider_thread_id",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_provider_binding_provider_thread",
		"PARTITION BY agent_id",
		"ADD CONSTRAINT pk_agent_provider_binding PRIMARY KEY (agent_id)",
		"ADD CONSTRAINT chk_agent_provider_binding_provider_not_empty",
		"VALIDATE CONSTRAINT chk_agent_provider_binding_provider_not_empty",
		"DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind ON agent_provider_binding",
	}
	for _, needle := range checks {
		if !strings.Contains(content, needle) {
			t.Fatalf("0029_agent_provider_binding_schema_repair.sql missing %q", needle)
		}
	}
}

func TestMigration0070RestoresFirstTimeProviderThreadIDWrite(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "0070_binding_provider_thread_first_write.sql", []string{
		"CREATE OR REPLACE FUNCTION prevent_agent_provider_binding_rebind()",
		"IF OLD.provider_thread_id <> ''",
		"AND OLD.provider_thread_id <> OLD.agent_id",
		"AND NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN",
		"UPDATE agent_provider_binding",
		"SET provider_thread_id = session_uuid",
		"WHERE provider = 'claude'",
		"AND provider_thread_id = agent_id",
		"session_uuid ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'",
	})
}

func TestMigration0071EnforcesClaudeProviderThreadUUID(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "0071_claude_provider_thread_uuid_check.sql", []string{
		"SET provider_thread_id = session_uuid",
		"WHERE provider = 'claude'",
		"AND (provider_thread_id = '' OR provider_thread_id = agent_id)",
		"SET provider_thread_id = ''",
		"AND provider_thread_id = agent_id",
		"ADD CONSTRAINT chk_claude_provider_thread_uuid",
		"provider <> 'claude'",
		"OR provider_thread_id = ''",
		"provider_thread_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'",
	})
}

func TestMigration0078RepairsDependsOnBeforeArrayCheck(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "0078_dag_v2_node_depends_on_array_check.sql", []string{
		"SET depends_on = '[]'::jsonb",
		"jsonb_typeof(depends_on) IS DISTINCT FROM 'array'",
		"ADD CONSTRAINT chk_depends_on_is_array",
		"VALIDATE CONSTRAINT chk_depends_on_is_array",
	})
}

func TestMigration0079RepairsReadsWritesBeforeArrayChecks(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "0079_dag_v2_node_run_id_fk_and_jsonb_checks.sql", []string{
		"SET reads = '[]'::jsonb",
		"jsonb_typeof(reads) IS DISTINCT FROM 'array'",
		"SET writes = '[]'::jsonb",
		"jsonb_typeof(writes) IS DISTINCT FROM 'array'",
		"ADD CONSTRAINT chk_reads_is_array",
		"ADD CONSTRAINT chk_writes_is_array",
	})
}

func TestMigration0088RepairsAgentThreadsBaselineCompatColumns(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "0088_agent_threads_baseline_compat_columns.sql", []string{
		"ADD COLUMN IF NOT EXISTS workspace_run_key TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN IF NOT EXISTS owner_thread_id   TEXT NOT NULL DEFAULT ''",
		"idx_agent_threads_workspace_run_key",
		"idx_agent_threads_owner_thread_id",
	})
}
