package mcpserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	_ "modernc.org/sqlite"
)

func TestConfigStorePersistsHTTPAndStdioServers(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")

	insertHTTPServer(t, store, ctx, workspaceRoot)
	insertPlaywrightServer(t, store, ctx, workspaceRoot)

	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if servers["my-search"].URL != "https://your-domain.com/mcp" {
		t.Fatalf("http server = %#v", servers["my-search"])
	}
	assertPlaywrightServer(t, servers["playwright"], true)
}

// TestConfigStoreRejectsUnsafePersistedStdioCommand 锁定持久化层读取边界：
// 已落库的 stdio 配置不能绕过写入 allowlist 再进入运行时。
func TestConfigStoreRejectsUnsafePersistedStdioCommand(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	if err := store.ensureTable(ctx); err != nil {
		t.Fatalf("ensureTable() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs (workspace_root, name, transport, command, args, env, enabled)
		VALUES (?, 'shell', 'stdio', 'bash', '["-lc","env"]', '{}', 1)
	`, workspaceRoot); err != nil {
		t.Fatalf("seed unsafe stdio row: %v", err)
	}

	_, err := store.ListServers(ctx, workspaceRoot)
	if err == nil {
		t.Fatal("ListServers() error = nil, want unsafe persisted stdio command rejection")
	}
	if !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("ListServers() error = %v, want unsupported stdio command", err)
	}
}

// TestConfigStoreRejectsRemovedPostgresCommand 确认历史行不能恢复已删除的 Postgres MCP 命令。
func TestConfigStoreRejectsRemovedPostgresCommand(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	if err := store.ensureTable(ctx); err != nil {
		t.Fatalf("ensureTable() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs (workspace_root, name, transport, command, args, env, enabled)
		VALUES (?, 'postgres', 'stdio', ?, '["postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"]', '{}', 1)
	`, workspaceRoot, "mcp-server-postgres"); err != nil {
		t.Fatalf("seed removed postgres row: %v", err)
	}

	_, err := store.ListServers(ctx, workspaceRoot)
	if err == nil {
		t.Fatal("ListServers() error = nil, want path-qualified postgres rejection")
	}
	if !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("ListServers() error = %v, want unsupported stdio command", err)
	}
}

func TestConfigStoreMigratesLegacyHTTPOnlyTableBeforeWritingStdio(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")

	if _, err := db.ExecContext(ctx, legacyHTTPOnlyMCPServerConfigsTableSQL); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs (workspace_root, name, transport, url, headers)
		VALUES (?, 'my-search', 'http', 'https://legacy.example/mcp', '{}')
	`, workspaceRoot); err != nil {
		t.Fatalf("seed legacy table: %v", err)
	}

	store := &configStore{db: db}
	inserted, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "playwright",
		Config: contract.MCPServerConfig{
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"@playwright/mcp@latest"},
		},
	})
	if err != nil {
		t.Fatalf("InsertServer(stdio after legacy migration) error = %v", err)
	}
	if !inserted {
		t.Fatal("InsertServer(stdio after legacy migration) inserted = false, want true")
	}

	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if servers["my-search"].URL != "https://legacy.example/mcp" {
		t.Fatalf("legacy http server = %#v", servers["my-search"])
	}
	assertPlaywrightServer(t, servers["playwright"], false)
}

func TestConfigStoreLegacyMigrationRollbackKeepsOriginalTableOnRenameFailure(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")

	if _, err := db.ExecContext(ctx, legacyHTTPOnlyMCPServerConfigsTableSQL); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs (workspace_root, name, transport, url, headers)
		VALUES (?, 'my-search', 'http', 'https://legacy.example/mcp', '{}')
	`, workspaceRoot); err != nil {
		t.Fatalf("seed legacy table: %v", err)
	}

	store := &configStore{db: failOnMCPServerExecDB{
		db:           db,
		failContains: "ALTER TABLE mcp_server_configs_next RENAME TO mcp_server_configs",
		err:          errors.New("injected rename failure"),
	}}
	_, err = store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "playwright",
		Config: contract.MCPServerConfig{
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"@playwright/mcp@latest"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("InsertServer() error = %v, want injected rename failure", err)
	}
	assertMCPServerTableExists(t, db, "mcp_server_configs")
	assertMCPServerTableMissing(t, db, "mcp_server_configs_next")
	assertLegacyMCPServerURL(t, db, workspaceRoot, "https://legacy.example/mcp")
}

func TestConfigStoreRecoversNextTableWhenMainTableWasDropped(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")

	if _, err := db.ExecContext(ctx, createMCPServerConfigsNextTableSQL); err != nil {
		t.Fatalf("create next table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs_next (
			workspace_root, name, transport, url, headers, command, args, env, enabled
		) VALUES (?, 'my-search', 'http', 'https://legacy.example/mcp', '{}', '', '[]', '{}', 1)
	`, workspaceRoot); err != nil {
		t.Fatalf("seed next table: %v", err)
	}

	store := &configStore{db: db}
	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if servers["my-search"].URL != "https://legacy.example/mcp" {
		t.Fatalf("recovered server = %#v", servers["my-search"])
	}
	assertMCPServerTableMissing(t, db, "mcp_server_configs_next")
}

func TestConfigStoreFailsFastWhenMainAndNextTablesBothExist(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")

	if _, err := db.ExecContext(ctx, createMCPServerConfigsTableSQL); err != nil {
		t.Fatalf("create main table: %v", err)
	}
	if _, err := db.ExecContext(ctx, createMCPServerConfigsNextTableSQL); err != nil {
		t.Fatalf("create next table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs_next (
			workspace_root, name, transport, url, headers, command, args, env, enabled
		) VALUES (?, 'my-search', 'http', 'https://legacy.example/mcp', '{}', '', '[]', '{}', 1)
	`, workspaceRoot); err != nil {
		t.Fatalf("seed next table: %v", err)
	}

	store := &configStore{db: db}
	_, err = store.ListServers(ctx, workspaceRoot)
	if err == nil || !strings.Contains(err.Error(), "mcp_server_configs_next") {
		t.Fatalf("ListServers() error = %v, want leftover next-table anomaly", err)
	}
}

func TestConfigStoreDeleteRemovesServer(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	if _, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "my-search",
		Config: contract.MCPServerConfig{
			Transport: "http",
			URL:       "https://your-domain.com/mcp",
		},
	}); err != nil {
		t.Fatalf("InsertServer() error = %v", err)
	}

	deleted, err := store.DeleteServer(ctx, workspaceRoot, "my-search")
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if !deleted {
		t.Fatal("DeleteServer() deleted = false, want true")
	}
	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if _, ok := servers["my-search"]; ok {
		t.Fatalf("server still listed after delete: %#v", servers)
	}
}

func TestConfigStoreReplaceServerOnlyUpdatesExactExistingRow(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	otherWorkspace := filepath.Join(t.TempDir(), "other")
	original := atomicityOriginalMCPServerConfig()
	replacement := atomicityReplacementMCPServerConfig()
	insertMCPServerConfig(t, store, ctx, workspaceRoot, "sqlite", original)
	insertMCPServerConfig(t, store, ctx, workspaceRoot, "other", original)
	insertMCPServerConfig(t, store, ctx, otherWorkspace, "sqlite", original)

	replaced, err := store.ReplaceServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "sqlite",
		Config:        replacement,
	})
	if err != nil {
		t.Fatalf("ReplaceServer() error = %v", err)
	}
	if !replaced {
		t.Fatal("ReplaceServer() replaced = false, want true")
	}
	assertStoredMCPServerConfig(t, store, ctx, workspaceRoot, "sqlite", replacement)
	assertStoredMCPServerConfig(t, store, ctx, workspaceRoot, "other", original)
	assertStoredMCPServerConfig(t, store, ctx, otherWorkspace, "sqlite", original)

	replaced, err = store.ReplaceServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "missing",
		Config:        replacement,
	})
	if err != nil {
		t.Fatalf("ReplaceServer(missing) error = %v", err)
	}
	if replaced {
		t.Fatal("ReplaceServer(missing) replaced = true, want false")
	}
}

func TestConfigStoreReplaceServerSQLiteFailureKeepsOriginalRow(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	original := atomicityOriginalMCPServerConfig()
	insertMCPServerConfig(t, store, ctx, workspaceRoot, "sqlite", original)
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_mcp_server_replace
		BEFORE UPDATE ON mcp_server_configs
		BEGIN
			SELECT RAISE(ABORT, 'injected replace failure');
		END
	`); err != nil {
		t.Fatalf("create replace failure trigger: %v", err)
	}

	_, err := store.ReplaceServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "sqlite",
		Config:        atomicityReplacementMCPServerConfig(),
	})
	if err == nil || !strings.Contains(err.Error(), "injected replace failure") {
		t.Fatalf("ReplaceServer() error = %v, want injected SQLite failure", err)
	}
	assertStoredMCPServerConfig(t, store, ctx, workspaceRoot, "sqlite", original)
}

func TestConfigStoreReplaceServerCanceledContextKeepsOriginalRow(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	original := atomicityOriginalMCPServerConfig()
	insertMCPServerConfig(t, store, context.Background(), workspaceRoot, "sqlite", original)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.ReplaceServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "sqlite",
		Config:        atomicityReplacementMCPServerConfig(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReplaceServer() error = %v, want context.Canceled", err)
	}
	assertStoredMCPServerConfig(t, store, context.Background(), workspaceRoot, "sqlite", original)
}

func TestConfigStoreReplaceServerClosedDBKeepsOriginalRow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp-server.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store := &configStore{db: db}
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	original := atomicityOriginalMCPServerConfig()
	insertMCPServerConfig(t, store, ctx, workspaceRoot, "sqlite", original)
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite before replace: %v", err)
	}

	_, err = store.ReplaceServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "sqlite",
		Config:        atomicityReplacementMCPServerConfig(),
	})
	if err == nil {
		t.Fatal("ReplaceServer() error = nil, want closed DB failure")
	}

	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer verifyDB.Close()
	assertStoredMCPServerConfig(t, &configStore{db: verifyDB}, ctx, workspaceRoot, "sqlite", original)
}

type failOnMCPServerExecDB struct {
	db           *sql.DB
	failContains string
	err          error
}

func (d failOnMCPServerExecDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, d.failContains) {
		return driver.RowsAffected(0), d.err
	}
	return d.db.ExecContext(ctx, query, args...)
}

func (d failOnMCPServerExecDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

func (d failOnMCPServerExecDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

func (d failOnMCPServerExecDB) withMCPServerMigrationTx(ctx context.Context, fn func(platformdb.Queryable) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	wrapped := failOnMCPServerExecTx{tx: tx, failContains: d.failContains, err: d.err}
	if err := fn(wrapped); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type failOnMCPServerExecTx struct {
	tx           *sql.Tx
	failContains string
	err          error
}

func (t failOnMCPServerExecTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, t.failContains) {
		return driver.RowsAffected(0), t.err
	}
	return t.tx.ExecContext(ctx, query, args...)
}

func (t failOnMCPServerExecTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

func (t failOnMCPServerExecTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func assertMCPServerTableExists(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if !mcpServerTableExists(t, db, table) {
		t.Fatalf("table %s missing", table)
	}
}

func assertMCPServerTableMissing(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if mcpServerTableExists(t, db, table) {
		t.Fatalf("table %s exists, want missing", table)
	}
}

func mcpServerTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, table).Scan(&count); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return count > 0
}

func assertLegacyMCPServerURL(t *testing.T, db *sql.DB, workspaceRoot, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`
		SELECT url
		FROM mcp_server_configs
		WHERE workspace_root = ? AND name = 'my-search'
	`, workspaceRoot).Scan(&got); err != nil {
		t.Fatalf("read legacy server after failed migration: %v", err)
	}
	if got != want {
		t.Fatalf("legacy server url = %q, want %q", got, want)
	}
}

func TestConfigStorePersistsEnabledState(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	if _, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "sqlite",
		Config: contract.MCPServerConfig{
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"-y", "@bytebase/dbhub@0.23.0", "--dsn=sqlite:///" + filepath.ToSlash(filepath.Join(workspaceRoot, "super-dolphin.db"))},
		},
	}); err != nil {
		t.Fatalf("InsertServer() error = %v", err)
	}
	updated, err := store.SetServerEnabled(ctx, workspaceRoot, "sqlite", false)
	if err != nil {
		t.Fatalf("SetServerEnabled(false) error = %v", err)
	}
	if !updated {
		t.Fatal("SetServerEnabled(false) updated = false, want true")
	}

	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	sqlite := servers["sqlite"]
	if sqlite.Enabled == nil || *sqlite.Enabled {
		t.Fatalf("sqlite enabled = %#v, want false", sqlite.Enabled)
	}
}

func TestConfigStoreBackfillsToolLifecycleWithoutOverwritingManualState(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	ensureMCPToolLifecycleTable(t, store, ctx)

	initial, err := store.BackfillToolLifecycle(ctx, contract.BackfillMCPToolLifecycleParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    "my-search",
		ManifestName:  "manifest-v1",
		ToolName:      "search",
		NowMillis:     100,
	})
	if err != nil {
		t.Fatalf("BackfillToolLifecycle(initial) error = %v", err)
	}
	if initial.State != contract.MCPToolLifecycleEnabled {
		t.Fatalf("initial state = %q, want enabled", initial.State)
	}

	manual, err := store.UpsertToolLifecycle(ctx, contract.StoreMCPToolLifecycleParams{
		WorkspaceRoot:   workspaceRoot,
		ServerName:      "my-search",
		ManifestName:    "manifest-v1",
		ToolName:        "search",
		State:           contract.MCPToolLifecycleSuspended,
		Reason:          "needs review",
		ReplacementTool: "search_v2",
		NowMillis:       200,
	})
	if err != nil {
		t.Fatalf("UpsertToolLifecycle(manual) error = %v", err)
	}
	if manual.State != contract.MCPToolLifecycleSuspended {
		t.Fatalf("manual state = %q, want suspended", manual.State)
	}

	got, err := store.BackfillToolLifecycle(ctx, contract.BackfillMCPToolLifecycleParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    "my-search",
		ManifestName:  "manifest-v2",
		ToolName:      "search",
		NowMillis:     300,
	})
	if err != nil {
		t.Fatalf("BackfillToolLifecycle(existing) error = %v", err)
	}
	assertLifecycleManualStatePreserved(t, got)
}

func TestConfigStoreRejectsUnknownToolLifecycleStateFromDB(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	ensureMCPToolLifecycleTable(t, store, ctx)

	if _, err := store.db.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatalf("enable ignore_check_constraints: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO mcp_tool_lifecycle (
			workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at
		) VALUES (?, 'my-search', '', 'search', 'bogus', '', '', 100, 100, 100)
	`, workspaceRoot); err != nil {
		t.Fatalf("seed bogus lifecycle row: %v", err)
	}

	_, err := store.GetToolLifecycle(ctx, workspaceRoot, "my-search", "search")
	if !errors.Is(err, errInvalidLifecycleState) {
		t.Fatalf("GetToolLifecycle() error = %v, want errInvalidLifecycleState", err)
	}
}

func TestConfigStoreExportsToolLifecycleForRollback(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")
	otherWorkspace := filepath.Join(t.TempDir(), "other")
	ensureMCPToolLifecycleTable(t, store, ctx)

	_, err := store.UpsertToolLifecycle(ctx, contract.StoreMCPToolLifecycleParams{
		WorkspaceRoot:   workspaceRoot,
		ServerName:      "z-search",
		ManifestName:    "manifest-z",
		ToolName:        "search",
		State:           contract.MCPToolLifecycleRemoved,
		Reason:          "migrated",
		ReplacementTool: "search_v2",
		NowMillis:       200,
	})
	if err != nil {
		t.Fatalf("UpsertToolLifecycle(z-search) error = %v", err)
	}
	_, err = store.UpsertToolLifecycle(ctx, contract.StoreMCPToolLifecycleParams{
		WorkspaceRoot: workspaceRoot,
		ServerName:    "a-lsp",
		ManifestName:  "manifest-a",
		ToolName:      "grep",
		State:         contract.MCPToolLifecycleSuspended,
		Reason:        "policy review",
		NowMillis:     300,
	})
	if err != nil {
		t.Fatalf("UpsertToolLifecycle(a-lsp) error = %v", err)
	}
	_, err = store.UpsertToolLifecycle(ctx, contract.StoreMCPToolLifecycleParams{
		WorkspaceRoot: otherWorkspace,
		ServerName:    "a-lsp",
		ToolName:      "hidden",
		State:         contract.MCPToolLifecycleDisabled,
		NowMillis:     400,
	})
	if err != nil {
		t.Fatalf("UpsertToolLifecycle(other workspace) error = %v", err)
	}

	got, err := store.ExportToolLifecycle(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ExportToolLifecycle() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ExportToolLifecycle() len = %d, want 2: %#v", len(got), got)
	}
	assertExportedLifecycleRow(t, got[0], "a-lsp", "grep", contract.MCPToolLifecycleSuspended, "")
	assertExportedLifecycleRow(t, got[1], "z-search", "search", contract.MCPToolLifecycleRemoved, "search_v2")
}

func atomicityOriginalMCPServerConfig() contract.MCPServerConfig {
	return contract.MCPServerConfig{
		Transport: "http",
		URL:       "https://legacy.example/mcp",
		Headers:   map[string]string{"Authorization": "Bearer legacy"},
		Enabled:   boolPtr(false),
	}
}

func atomicityReplacementMCPServerConfig() contract.MCPServerConfig {
	return contract.MCPServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@bytebase/dbhub@0.23.0", "--dsn=sqlite:///replacement.db"},
		Env:       map[string]string{"DBHUB_LOG_LEVEL": "error"},
		Enabled:   boolPtr(true),
	}
}

func insertMCPServerConfig(
	t *testing.T,
	store *configStore,
	ctx context.Context,
	workspaceRoot string,
	name string,
	config contract.MCPServerConfig,
) {
	t.Helper()
	inserted, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          name,
		Config:        config,
	})
	if err != nil {
		t.Fatalf("InsertServer(%s) error = %v", name, err)
	}
	if !inserted {
		t.Fatalf("InsertServer(%s) inserted = false, want true", name)
	}
}

func assertStoredMCPServerConfig(
	t *testing.T,
	store *configStore,
	ctx context.Context,
	workspaceRoot string,
	name string,
	want contract.MCPServerConfig,
) {
	t.Helper()
	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	got, ok := servers[name]
	if !ok {
		t.Fatalf("stored server %q missing: %#v", name, servers)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored server %q = %#v, want %#v", name, got, want)
	}
}

func newSQLiteConfigStore(t *testing.T) (*configStore, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return &configStore{db: db}, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	}
}

func ensureMCPToolLifecycleTable(t *testing.T, store *configStore, ctx context.Context) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "platform", "db", "sqlite", "migrations", "109_mcp_tool_lifecycle.sql"))
	if err != nil {
		t.Fatalf("read lifecycle migration: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatalf("create lifecycle table: %v", err)
	}
}

func assertLifecycleManualStatePreserved(t *testing.T, got contract.MCPToolLifecycleDecision) {
	t.Helper()
	if got.State != contract.MCPToolLifecycleSuspended {
		t.Fatalf("backfilled state = %q, want suspended", got.State)
	}
	if got.Reason != "needs review" || got.ReplacementTool != "search_v2" {
		t.Fatalf("backfilled row = %#v, want manual reason/replacement preserved", got)
	}
	if got.ManifestName != "manifest-v2" || got.LastSeenAt != 300 {
		t.Fatalf("backfilled discovery fields = %#v, want manifest-v2 last_seen=300", got)
	}
}

func assertExportedLifecycleRow(
	t *testing.T,
	got contract.MCPToolLifecycleDecision,
	serverName string,
	toolName string,
	state contract.MCPToolLifecycleState,
	replacementTool string,
) {
	t.Helper()
	if got.ServerName != serverName {
		t.Fatalf("export server = %q, want %q; row=%#v", got.ServerName, serverName, got)
	}
	if got.ToolName != toolName {
		t.Fatalf("export tool = %q, want %q; row=%#v", got.ToolName, toolName, got)
	}
	if got.State != state {
		t.Fatalf("export state = %q, want %q; row=%#v", got.State, state, got)
	}
	if got.ReplacementTool != replacementTool {
		t.Fatalf("export replacement = %q, want %q; row=%#v", got.ReplacementTool, replacementTool, got)
	}
}

func insertHTTPServer(t *testing.T, store *configStore, ctx context.Context, workspaceRoot string) {
	t.Helper()
	inserted, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "my-search",
		Config: contract.MCPServerConfig{
			Transport: "http",
			URL:       "https://your-domain.com/mcp",
			Headers:   map[string]string{"Authorization": "Bearer YOUR_API_KEY"},
		},
	})
	if err != nil {
		t.Fatalf("InsertServer(http) error = %v", err)
	}
	if !inserted {
		t.Fatal("InsertServer(http) inserted = false, want true")
	}
}

func insertPlaywrightServer(t *testing.T, store *configStore, ctx context.Context, workspaceRoot string) {
	t.Helper()
	inserted, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "playwright",
		Config: contract.MCPServerConfig{
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"@playwright/mcp@latest"},
			Env:       map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "/tmp/browsers"},
		},
	})
	if err != nil {
		t.Fatalf("InsertServer(stdio) error = %v", err)
	}
	if !inserted {
		t.Fatal("InsertServer(stdio) inserted = false, want true")
	}
}

func assertPlaywrightServer(t *testing.T, playwright contract.MCPServerConfig, wantEnv bool) {
	t.Helper()
	if playwright.Transport != "stdio" || playwright.Command != "npx" {
		t.Fatalf("playwright server = %#v, want stdio npx command", playwright)
	}
	if len(playwright.Args) != 1 || playwright.Args[0] != "@playwright/mcp@latest" {
		t.Fatalf("playwright args = %#v", playwright.Args)
	}
	if wantEnv && playwright.Env["PLAYWRIGHT_BROWSERS_PATH"] != "/tmp/browsers" {
		t.Fatalf("playwright env = %#v", playwright.Env)
	}
}

const legacyHTTPOnlyMCPServerConfigsTableSQL = `
CREATE TABLE mcp_server_configs (
	workspace_root TEXT NOT NULL,
	name TEXT NOT NULL,
	transport TEXT NOT NULL,
	url TEXT NOT NULL,
	headers TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	PRIMARY KEY (workspace_root, name),
	CHECK (workspace_root <> ''),
	CHECK (name <> ''),
	CHECK (transport <> ''),
	CHECK (url <> ''),
	CHECK (headers <> '')
);
`
