package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSQLiteDiagnosticsPath(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		rel  string
		want bool
	}{
		{name: "SQLite baseline", rel: "internal/platform/db/sqlite/migrations/001_baseline.sql", want: true},
		{name: "orchestration query", rel: "cmd/mcp-orch/sql/queries/command_card.sql", want: true},
		{name: "root query", rel: "sql/queries/command_card.sql", want: true},
		{name: "SQL outside SQLite owners", rel: "fixtures/schema.sql", want: false},
		{name: "non SQL", rel: "sql/queries/query.go", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, isSQLiteDiagnosticsPath(root, filepath.Join(root, filepath.FromSlash(test.rel))))
		})
	}
}

func TestIsSQLiteDiagnosticsPathAcceptsPhysicalAndSymlinkAliases(t *testing.T) {
	physical := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(physical, "sql", "queries"), 0o755))
	queryPath := filepath.Join(physical, "sql", "queries", "query.sql")
	require.NoError(t, os.WriteFile(queryPath, []byte("SELECT 1;\n"), 0o600))
	alias := filepath.Join(t.TempDir(), "repo-link")
	createSQLiteDiagnosticsDirectoryAlias(t, physical, alias)
	canonicalAlias, aliasErr := canonicalSQLiteDiagnosticsPath(alias)
	require.NoError(t, aliasErr)
	canonicalPhysical, physicalErr := canonicalSQLiteDiagnosticsPath(physical)
	require.NoError(t, physicalErr)
	require.Equal(t, canonicalPhysical, canonicalAlias, "platform directory alias must resolve to the physical SQLite diagnostics root")
	require.True(t, isSQLiteDiagnosticsPath(alias, queryPath))
	require.True(t, isSQLiteDiagnosticsPath(alias, filepath.Join(physical, "sql", "queries", "new.sql")))
}

func TestIsSQLiteDiagnosticsWorkspace(t *testing.T) {
	matched, err := isSQLiteDiagnosticsWorkspace(sqliteDiagnosticsRepoRoot(t))
	require.NoError(t, err)
	require.True(t, matched)

	external := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(external, "sql", "queries"), 0o755))
	matched, err = isSQLiteDiagnosticsWorkspace(external)
	require.NoError(t, err)
	require.False(t, matched)
}

func TestValidateSQLiteDocumentRepositoryFixtures(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	state := newSQLiteDiagnosticsState()
	tests := []struct {
		name string
		rel  string
	}{
		{name: "baseline with triggers", rel: "internal/platform/db/sqlite/migrations/001_baseline.sql"},
		{name: "split migration", rel: "internal/platform/db/sqlite/migrations/106_agent_provider_binding_codex_home_alias_repair.sql"},
		{name: "incremental trigger migration", rel: "internal/platform/db/sqlite/migrations/113_bus_exception_log_flags.sql"},
		{name: "root sqlc query", rel: "sql/queries/agent_status.sql"},
		{name: "orchestration named query", rel: "cmd/mcp-orch/sql/queries/command_card.sql"},
		{name: "orchestration schema patch", rel: "cmd/mcp-orch/sql/schema_sqlc_patch.sql"},
		{name: "SQLite release fixture", rel: "internal/platform/db/sqlite/testdata/minimal_fixture.sql"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(test.rel))
			text, err := os.ReadFile(path)
			require.NoError(t, err)
			diagnostics, err := state.validateSQLiteDocument(context.Background(), root, sqliteDiagnosticsFileURI(path), string(text))
			require.NoError(t, err)
			require.Empty(t, diagnostics)
		})
	}
}

func TestValidateSQLiteDocumentReusesUnchangedMigrationChain(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	state := newSQLiteDiagnosticsState()

	for _, rel := range []string{
		"internal/platform/db/sqlite/migrations/001_baseline.sql",
		"internal/platform/db/sqlite/migrations/106_agent_provider_binding_codex_home_alias_repair.sql",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		diagnostics, err := state.validateSQLiteDocument(context.Background(), root, sqliteDiagnosticsFileURI(path), string(body))
		require.NoError(t, err)
		require.Empty(t, diagnostics)
	}

	state.migrationChainCache.Lock()
	defer state.migrationChainCache.Unlock()
	require.Len(t, state.migrationChainCache.entries, 1)
}

func TestValidateSQLiteQueriesReuseMigratedSchema(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	state := newSQLiteDiagnosticsState()
	state.schemaDBCache.Lock()
	for key, entry := range state.schemaDBCache.entries {
		require.NoError(t, entry.db.Close())
		delete(state.schemaDBCache.entries, key)
	}
	state.schemaDBCache.clock = 0
	state.schemaDBCache.Unlock()

	for _, query := range []string{
		"-- name: Agent :one\nSELECT * FROM agent_status LIMIT 1;",
		"-- name: Thread :one\nSELECT * FROM agent_threads LIMIT 1;",
	} {
		diagnostics, err := state.validateSQLiteQueries(context.Background(), root, query)
		require.NoError(t, err)
		require.Empty(t, diagnostics)
	}

	state.schemaDBCache.Lock()
	defer state.schemaDBCache.Unlock()
	require.Len(t, state.schemaDBCache.entries, 1)
}

func TestValidateSQLiteQueriesEvictsOldSchemasWithoutRejectingNinthFingerprint(t *testing.T) {
	root := t.TempDir()
	migrationsDir := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations")
	require.NoError(t, os.MkdirAll(migrationsDir, 0o755))
	migrationPath := filepath.Join(migrationsDir, "001_baseline.sql")
	repoBaseline, err := os.ReadFile(filepath.Join(sqliteDiagnosticsRepoRoot(t), "internal", "platform", "db", "sqlite", "migrations", "001_baseline.sql"))
	require.NoError(t, err)
	state := newSQLiteDiagnosticsState()

	state.schemaDBCache.Lock()
	for key, entry := range state.schemaDBCache.entries {
		require.NoError(t, entry.db.Close())
		delete(state.schemaDBCache.entries, key)
	}
	state.schemaDBCache.clock = 0
	state.schemaDBCache.Unlock()

	for version := range sqliteSchemaCacheCapacity + 1 {
		body := fmt.Sprintf("%s\n-- cache test version %d\n", repoBaseline, version)
		require.NoError(t, os.WriteFile(migrationPath, []byte(body), 0o600))
		diagnostics, err := state.validateSQLiteQueries(context.Background(), root, "-- name: Agent :one\nSELECT * FROM agent_status LIMIT 1;")
		require.NoError(t, err)
		require.Empty(t, diagnostics)
	}

	state.schemaDBCache.Lock()
	defer state.schemaDBCache.Unlock()
	require.LessOrEqual(t, len(state.schemaDBCache.entries), sqliteSchemaCacheCapacity)
	for _, entry := range state.schemaDBCache.entries {
		require.Zero(t, entry.refs)
	}
}

func TestValidateSQLiteDocumentRejectsInvalidSQL(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	state := newSQLiteDiagnosticsState()
	tests := []struct {
		name string
		rel  string
		text string
	}{
		{
			name: "bad DDL",
			rel:  "internal/platform/db/sqlite/migrations/001_baseline.sql",
			text: "CREATE TABLE broken (id INTEGER PRIMARY KEY,);",
		},
		{
			name: "bad trigger",
			rel:  "internal/platform/db/sqlite/migrations/001_baseline.sql",
			text: "CREATE TABLE x (id INTEGER); CREATE TRIGGER bad AFTER INSERT ON x BEGIN BROKEN SQL; END;",
		},
		{
			name: "bad query",
			rel:  "sql/queries/agent_status.sql",
			text: "-- name: Broken :one\nSELECT FROM agent_status;",
		},
		{
			name: "bad new migration",
			rel:  "internal/platform/db/sqlite/migrations/999_new_bad.sql",
			text: "CREATE TABLE broken (id INTEGER PRIMARY KEY,);",
		},
		{
			name: "bad declarative special migration",
			rel:  "internal/platform/db/sqlite/migrations/112_system_logs_trace_span.sql",
			text: "BROKEN SQL;",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(test.rel))
			diagnostics, err := state.validateSQLiteDocument(context.Background(), root, sqliteDiagnosticsFileURI(path), test.text)
			require.NoError(t, err)
			require.NotEmpty(t, diagnostics)
			require.Equal(t, sqliteDiagnosticsSource, diagnostics[0].Source)
		})
	}
}

func sqliteDiagnosticsRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}

func sqliteDiagnosticsFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
