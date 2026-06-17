package mcpserver

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	_ "modernc.org/sqlite"
)

func TestConfigStorePersistsHTTPAndStdioServers(t *testing.T) {
	store, closeDB := newSQLiteConfigStore(t)
	defer closeDB()
	ctx := context.Background()
	workspaceRoot := filepath.Join(t.TempDir(), "project")

	insertHTTPServer(t, store, ctx, workspaceRoot)
	insertPostgresServer(t, store, ctx, workspaceRoot)

	servers, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if servers["my-search"].URL != "https://your-domain.com/mcp" {
		t.Fatalf("http server = %#v", servers["my-search"])
	}
	assertPostgresServer(t, servers["postgres"], true)
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
		Name:          "postgres",
		Config: contract.MCPServerConfig{
			Transport: "stdio",
			Command:   "mcp-server-postgres",
			Args:      []string{"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"},
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
	assertPostgresServer(t, servers["postgres"], false)
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
			Args:      []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:///" + filepath.ToSlash(filepath.Join(workspaceRoot, "super-dolphin.db"))},
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

func insertPostgresServer(t *testing.T, store *configStore, ctx context.Context, workspaceRoot string) {
	t.Helper()
	inserted, err := store.InsertServer(ctx, contract.StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          "postgres",
		Config: contract.MCPServerConfig{
			Transport: "stdio",
			Command:   "mcp-server-postgres",
			Args:      []string{"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"},
			Env:       map[string]string{"PGAPPNAME": "super-dolphin"},
		},
	})
	if err != nil {
		t.Fatalf("InsertServer(stdio) error = %v", err)
	}
	if !inserted {
		t.Fatal("InsertServer(stdio) inserted = false, want true")
	}
}

func assertPostgresServer(t *testing.T, postgres contract.MCPServerConfig, wantEnv bool) {
	t.Helper()
	if postgres.Transport != "stdio" || postgres.Command != "mcp-server-postgres" {
		t.Fatalf("postgres server = %#v, want stdio command", postgres)
	}
	if len(postgres.Args) != 1 || postgres.Args[0] != "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable" {
		t.Fatalf("postgres args = %#v", postgres.Args)
	}
	if wantEnv && postgres.Env["PGAPPNAME"] != "super-dolphin" {
		t.Fatalf("postgres env = %#v", postgres.Env)
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
