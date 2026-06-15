package commandcard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestSQLiteCommandListSearchesTemplateAndKeepsListAndDetailShape(t *testing.T) {
	ctx := context.Background()
	db := openCommandSQLiteDB(t)
	store := NewStore(sqlc.New(db))

	created, err := store.Upsert(ctx, CommandCard{
		CardKey:         "cmd/template-search",
		Title:           "Template Search",
		Description:     "description",
		CommandTemplate: "go test ./cmd/mcp-orch/... -run task11-command-needle",
		ArgsSchema:      json.RawMessage(`{"type":"object","properties":{"pkg":{"type":"string"}}}`),
		RiskLevel:       "normal",
		Enabled:         true,
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("Upsert() ID = 0, want persisted row")
	}

	listed, err := store.List(ctx, ListFilter{Keyword: "task11-command-needle", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List() len = %d, want 1", len(listed))
	}
	assertCommandCardPayload(t, "list", listed[0])

	detail, err := store.Get(ctx, "cmd/template-search")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertCommandCardPayload(t, "detail", *detail)
}

func assertCommandCardPayload(t *testing.T, label string, got CommandCard) {
	t.Helper()
	if !strings.Contains(got.CommandTemplate, "task11-command-needle") {
		t.Fatalf("%s CommandTemplate = %q, want template needle", label, got.CommandTemplate)
	}
	if !strings.Contains(string(got.ArgsSchema), `"pkg"`) {
		t.Fatalf("%s ArgsSchema = %s, want pkg property", label, got.ArgsSchema)
	}
	if got.RiskLevel != "normal" || !got.Enabled {
		t.Fatalf("%s risk=%q enabled=%v, want normal/true", label, got.RiskLevel, got.Enabled)
	}
}

func openCommandSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "command.sqlite")
	db, err := sql.Open("sqlite", commandSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(ctx, db, commandSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func commandSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func commandSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
