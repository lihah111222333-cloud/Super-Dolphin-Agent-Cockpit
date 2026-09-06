package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestStandaloneSessionPersistenceAtomicity(t *testing.T) {
	ctx := context.Background()
	db := sessionTestDatabase(t)
	store := NewSessionStore(db)
	record := standalone.SessionRecord{AgentID: "agent", ThreadID: "thread", Provider: "codex", Name: "worker",
		CWD: t.TempDir(), Model: "test-model", CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	thread, err := NewThreadStore(db).GetByThreadID(ctx, "thread")
	if err != nil || thread.AgentID != "agent" || thread.Status != "idle" {
		t.Fatalf("persisted thread=%#v err=%v", thread, err)
	}
	record.ThreadID = "conflicting-thread"
	if err := store.CreateSession(ctx, record); err == nil {
		t.Fatal("duplicate agent binding was overwritten")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_threads WHERE thread_id = ?", record.ThreadID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("binding conflict left an orphan thread row")
	}
}

func TestBindingProjectionSurfacesProviderIdentityFields(t *testing.T) {
	ctx := context.Background()
	db := sessionTestDatabase(t)
	_, err := db.ExecContext(ctx, `INSERT INTO agent_provider_binding
		(agent_id, provider, provider_thread_id, codex_thread_id, cwd, archived,
		 created_at, updated_at, session_uuid, codex_home, provider_recovery_home,
		 codex_instance_key, codex_model_provider)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"agent-projection", "codex", "provider-thread", "public-thread", "/repo", false,
		1, 2, "session-uuid", "/codex/home", "/codex/home", "instance", "model-provider")
	if err != nil {
		t.Fatalf("insert provider binding: %v", err)
	}

	binding, err := NewBindingStore(db).GetByAgentID(ctx, "agent-projection")
	if err != nil {
		t.Fatalf("GetByAgentID() error = %v", err)
	}
	assertBindingIdentityProjection(t, binding)
}

func assertBindingIdentityProjection(t *testing.T, binding *orchestration.PersistedBinding) {
	t.Helper()
	if binding.AgentID != "agent-projection" || binding.ProviderThreadID != "provider-thread" || binding.CodexThreadID != "public-thread" {
		t.Fatalf("binding identity = %#v, want provider/public projection", binding)
	}
	if binding.SessionUUID != "session-uuid" || binding.CodexHome != "/codex/home" || binding.ProviderRecoveryHome != "/codex/home" || binding.CodexInstanceKey != "instance" || binding.CodexModelProvider != "model-provider" {
		t.Fatalf("binding provider identity fields = %#v, want all persisted fields", binding)
	}
}

func sessionTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := sqliteruntime.Open(ctx, sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "orch.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := sqliteruntime.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
