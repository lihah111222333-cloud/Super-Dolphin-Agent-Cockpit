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
