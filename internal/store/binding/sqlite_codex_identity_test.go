package binding_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	binding "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"

	_ "modernc.org/sqlite"
)

func TestSQLiteUpsertAllowsCodexHomeAliasRepairForSameTuple(t *testing.T) {
	t.Parallel()

	store := newBaselineSQLiteBindingStore(t)
	canonicalHome, aliasHome := createSQLiteCodexHomeCleanAlias(t)
	ctx := context.Background()
	seed := sqliteCodexIdentityUpsertParams("agent-alias-repair")
	seed.CodexHome = aliasHome

	if err := store.Upsert(ctx, seed); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	repair := seed
	repair.CodexHome = canonicalHome
	repair.UpdatedAt = 2
	if err := store.Upsert(ctx, repair); err != nil {
		t.Fatalf("canonical repair Upsert() error = %v", err)
	}
	binding, err := store.GetByAgentID(ctx, seed.AgentID)
	if err != nil {
		t.Fatalf("GetByAgentID() error = %v", err)
	}
	if binding.CodexHome != canonicalHome {
		t.Fatalf("CodexHome = %q, want canonical %q", binding.CodexHome, canonicalHome)
	}
	if binding.CodexInstanceKey != "default" || binding.CodexModelProvider != "openai" {
		t.Fatalf("codex tuple = %q/%q, want default/openai",
			binding.CodexInstanceKey,
			binding.CodexModelProvider)
	}
}

func TestSQLiteRunMigrationsRepairsOldCodexHomeTrigger(t *testing.T) {
	t.Parallel()

	db, store := newOldBaselineSQLiteBindingStore(t)
	ctx := context.Background()
	canonicalHome, aliasHome := createSQLiteCodexHomeCleanAlias(t)
	seed := sqliteCodexIdentityUpsertParams("agent-migration-alias-repair")
	seed.CodexHome = aliasHome

	mustHaveSQLiteMigrationMarker(t, ctx, db, "001_baseline.sql")
	mustSQLiteUpsert(t, ctx, store, "seed", seed)
	repair := seed
	repair.CodexHome = canonicalHome
	repair.UpdatedAt = 2
	assertSQLiteImmutableRejection(t, ctx, store, repair)

	if err := platformsqlite.RunMigrations(ctx, db, sqliteMigrationsDir()); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	mustHaveSQLiteMigrationMarker(t, ctx, db, "106_agent_provider_binding_codex_home_alias_repair.sql")
	mustSQLiteUpsert(t, ctx, store, "post-migration repair", repair)
	assertSQLiteCodexIdentityStored(t, ctx, store, seed.AgentID, canonicalHome)
}

func mustSQLiteUpsert(t *testing.T, ctx context.Context, store binding.Store, label string, params binding.UpsertParams) {
	t.Helper()

	if err := store.Upsert(ctx, params); err != nil {
		t.Fatalf("%s Upsert() error = %v", label, err)
	}
}

func assertSQLiteImmutableRejection(t *testing.T, ctx context.Context, store binding.Store, params binding.UpsertParams) {
	t.Helper()

	err := store.Upsert(ctx, params)
	if err == nil || !strings.Contains(err.Error(), "identity is immutable") {
		t.Fatalf("pre-migration repair Upsert() error = %v, want immutable rejection", err)
	}
}

func assertSQLiteCodexIdentityStored(t *testing.T, ctx context.Context, store binding.Store, agentID, canonicalHome string) {
	t.Helper()

	got, err := store.GetByAgentID(ctx, agentID)
	if err != nil {
		t.Fatalf("GetByAgentID() error = %v", err)
	}
	if got.CodexHome != canonicalHome {
		t.Fatalf("CodexHome = %q, want canonical %q", got.CodexHome, canonicalHome)
	}
	if got.CodexInstanceKey != "default" || got.CodexModelProvider != "openai" {
		t.Fatalf("codex tuple = %q/%q, want default/openai",
			got.CodexInstanceKey,
			got.CodexModelProvider)
	}
}

func TestSQLiteUpsertRejectsCodexTupleConflicts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		instanceKey   string
		modelProvider string
	}{
		{name: "instance key", instanceKey: "other", modelProvider: "openai"},
		{name: "model provider", instanceKey: "default", modelProvider: "other-provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newBaselineSQLiteBindingStore(t)
			ctx := context.Background()
			seed := sqliteCodexIdentityUpsertParams("agent-" + strings.ReplaceAll(tc.name, " ", "-"))
			if err := store.Upsert(ctx, seed); err != nil {
				t.Fatalf("seed Upsert() error = %v", err)
			}
			conflict := seed
			conflict.CodexInstanceKey = tc.instanceKey
			conflict.CodexModelProvider = tc.modelProvider
			conflict.UpdatedAt = 2
			err := store.Upsert(ctx, conflict)
			if err == nil || !strings.Contains(err.Error(), "identity is immutable") {
				t.Fatalf("conflict Upsert() error = %v, want immutable rejection", err)
			}
			got, err := store.GetByAgentID(ctx, seed.AgentID)
			if err != nil {
				t.Fatalf("GetByAgentID() error = %v", err)
			}
			if got.CodexInstanceKey != "default" || got.CodexModelProvider != "openai" {
				t.Fatalf("stored tuple = %q/%q, want original default/openai",
					got.CodexInstanceKey,
					got.CodexModelProvider)
			}
		})
	}
}

func newBaselineSQLiteBindingStore(t *testing.T) binding.Store {
	t.Helper()

	return binding.NewStore(sqlc.New(newBaselineSQLiteDB(t)))
}

func newOldBaselineSQLiteBindingStore(t *testing.T) (*sql.DB, binding.Store) {
	t.Helper()

	db := newBaselineSQLiteDB(t)
	if _, err := db.Exec("DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind"); err != nil {
		t.Fatalf("drop current provider binding trigger: %v", err)
	}
	if _, err := db.Exec(oldAgentProviderBindingRebindTriggerSQL); err != nil {
		t.Fatalf("install old provider binding trigger: %v", err)
	}
	return db, binding.NewStore(sqlc.New(db))
}

func newBaselineSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("db.Close() error = %v", err)
		}
	})
	db.SetMaxOpenConns(1)
	body, err := os.ReadFile(filepath.Join(sqliteMigrationsDir(), "001_baseline.sql"))
	if err != nil {
		t.Fatalf("read baseline migration: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("exec baseline migration: %v", err)
	}
	return db
}

func mustHaveSQLiteMigrationMarker(t *testing.T, ctx context.Context, db *sql.DB, filename string) {
	t.Helper()

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", filename).Scan(&count); err != nil {
		t.Fatalf("query SQLite migration marker %s: %v", filename, err)
	}
	if count != 1 {
		t.Fatalf("SQLite migration marker %s count = %d, want 1", filename, count)
	}
}

func sqliteMigrationsDir() string {
	return filepath.Join("..", "..", "platform", "db", "sqlite", "migrations")
}

func sqliteCodexIdentityUpsertParams(agentID string) binding.UpsertParams {
	return binding.UpsertParams{
		AgentID:            agentID,
		Provider:           "codex",
		ProviderThreadID:   agentID + "-provider-thread",
		CodexThreadID:      agentID + "-public-thread",
		Cwd:                "/repo",
		CreatedAt:          1,
		UpdatedAt:          1,
		CodexHome:          "/real/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}
}

func createSQLiteCodexHomeCleanAlias(t *testing.T) (string, string) {
	t.Helper()

	realHome := t.TempDir()
	return realHome, realHome + string(os.PathSeparator) + "."
}

const oldAgentProviderBindingRebindTriggerSQL = `
CREATE TRIGGER IF NOT EXISTS trg_prevent_agent_provider_binding_rebind
BEFORE UPDATE ON agent_provider_binding
FOR EACH ROW
WHEN NEW.agent_id <> OLD.agent_id
  OR NEW.provider <> OLD.provider
  OR (OLD.provider_thread_id <> '' AND NEW.provider_thread_id <> OLD.provider_thread_id)
  OR (OLD.codex_home <> '' AND NEW.codex_home <> OLD.codex_home)
  OR (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
  OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
BEGIN
    SELECT RAISE(ABORT, 'agent_provider_binding identity is immutable');
END;
`
