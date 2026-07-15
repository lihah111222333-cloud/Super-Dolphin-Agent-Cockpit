package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestRouteSQLDiagnosticsInputSelectsSQLiteServiceFromSQLC(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: sql/queries\n")
	target := writeSQLDialectTestFile(t, root, "sql/queries/card.sql", "SELECT * FROM cards WHERE card_key = ?;\n")

	routed, err := routeSQLDiagnosticsInput(sqlDialectTestContext(root), fileToolInput{}, []diagnosticTarget{{AbsPath: target}})
	if err != nil {
		t.Fatalf("route SQL diagnostics: %v", err)
	}
	if routed.LanguageID != sqliteSQLLanguageID {
		t.Fatalf("language_id = %q, want %q", routed.LanguageID, sqliteSQLLanguageID)
	}
}

func TestRouteSQLDiagnosticsInputRejectsPostgreSQLOwner(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: postgresql\n    queries: sql/queries\n")
	target := writeSQLDialectTestFile(t, root, "sql/queries/card.sql", "SELECT $1;\n")

	if _, err := routeSQLDiagnosticsInput(sqlDialectTestContext(root), fileToolInput{}, []diagnosticTarget{{AbsPath: target}}); err == nil {
		t.Fatal("route SQL diagnostics succeeded for unsupported PostgreSQL owner")
	}
}

func TestResolveLanguageIDForSQLRejectsNonSQLOverride(t *testing.T) {
	root := t.TempDir()
	target := writeSQLDialectTestFile(t, root, "query.sql", "SELECT ?;\n")

	if _, err := resolveLanguageIDForFile(sqlDialectTestContext(root), target, "javascript"); err == nil {
		t.Fatal("resolve SQL language accepted a non-SQL language override")
	}
}

func TestResolveLanguageIDForPostgreSQLOwnerCannotBeBypassedByOverride(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: postgresql\n    queries: queries\n")
	target := writeSQLDialectTestFile(t, root, "queries/query.sql", "SELECT $1;\n")

	if _, err := resolveLanguageIDForFile(sqlDialectTestContext(root), target, "sql"); err == nil {
		t.Fatal("resolve SQL language accepted an unsupported PostgreSQL owner with explicit sql override")
	}
}

func TestRouteSQLDiagnosticsInputDefaultsUnownedSQLToSQLite(t *testing.T) {
	root := t.TempDir()
	target := writeSQLDialectTestFile(t, root, "query.sql", "SELECT ?;\n")

	routed, err := routeSQLDiagnosticsInput(sqlDialectTestContext(root), fileToolInput{}, []diagnosticTarget{{AbsPath: target}})
	if err != nil {
		t.Fatalf("route SQL diagnostics: %v", err)
	}
	if routed.LanguageID != sqliteSQLLanguageID {
		t.Fatalf("language_id = %q, want %q", routed.LanguageID, sqliteSQLLanguageID)
	}
}

func TestRouteSQLDiagnosticsInputRejectsMalformedSQLC(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "sql: [\n")
	target := writeSQLDialectTestFile(t, root, "query.sql", "SELECT 1;\n")

	if _, err := routeSQLDiagnosticsInput(sqlDialectTestContext(root), fileToolInput{}, []diagnosticTarget{{AbsPath: target}}); err == nil {
		t.Fatal("route SQL diagnostics succeeded with malformed sqlc config")
	}
}

func TestDiagnosticsInputsBySQLDialectSplitsMixedBatch(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlite/sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: queries\n")
	sqliteTarget := writeSQLDialectTestFile(t, root, "sqlite/queries/card.sql", "SELECT ?;\n")
	secondSQLiteTarget := writeSQLDialectTestFile(t, root, "other/query.sql", "SELECT ?;\n")
	targets := []diagnosticTarget{{AbsPath: sqliteTarget}, {AbsPath: secondSQLiteTarget}}

	inputs, split, err := diagnosticsInputsBySQLDialect(sqlDialectTestContext(root), fileToolInput{FilePaths: []string{sqliteTarget, secondSQLiteTarget}}, targets)
	if err != nil {
		t.Fatalf("route mixed SQL diagnostics: %v", err)
	}
	if !split || len(inputs) != 2 {
		t.Fatalf("split=%v inputs=%#v, want two routed inputs", split, inputs)
	}
	if inputs[0].LanguageID != sqliteSQLLanguageID || inputs[1].LanguageID != sqliteSQLLanguageID {
		t.Fatalf("mixed languages = %q, %q", inputs[0].LanguageID, inputs[1].LanguageID)
	}
}

func TestSQLDialectSearchStopsAtTrustedWorkspaceRoot(t *testing.T) {
	outer := t.TempDir()
	workspace := filepath.Join(outer, "workspace")
	writeSQLDialectTestFile(t, outer, "sqlc.yaml", "sql: [\n")
	target := writeSQLDialectTestFile(t, workspace, "query.sql", "SELECT 1;\n")

	engine, err := sqlFileEngine(sqlDialectTestContext(workspace), target)
	if err != nil {
		t.Fatalf("resolve SQL engine: %v", err)
	}
	if engine != "" {
		t.Fatalf("engine = %q, want no owner inside workspace", engine)
	}
}

func TestSQLDialectRejectsNearestPostgreSQLConfig(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: .\n")
	writeSQLDialectTestFile(t, root, "nested/sqlc.yaml", "version: \"2\"\nsql:\n  - engine: postgresql\n    queries: .\n")
	target := writeSQLDialectTestFile(t, root, "nested/query.sql", "SELECT $1;\n")

	if _, err := resolveLanguageIDForFile(sqlDialectTestContext(root), target, ""); err == nil {
		t.Fatal("resolve SQL language succeeded for nearest PostgreSQL owner")
	}
}

func TestSQLDialectRejectsAmbiguousOwnersInNearestConfig(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: queries\n  - engine: postgresql\n    queries: queries\n")
	target := writeSQLDialectTestFile(t, root, "queries/query.sql", "SELECT 1;\n")

	if _, err := sqlFileEngine(sqlDialectTestContext(root), target); err == nil {
		t.Fatal("resolve SQL engine succeeded with ambiguous owners")
	}
}

func TestSQLDialectRejectsSymlinkConfig(t *testing.T) {
	root := t.TempDir()
	realConfig := writeSQLDialectTestFile(t, root, "config-source.yaml", "version: \"2\"\nsql: []\n")
	if err := os.Symlink(realConfig, filepath.Join(root, "sqlc.yaml")); err != nil {
		t.Skipf("create sqlc symlink: %v", err)
	}
	target := writeSQLDialectTestFile(t, root, "query.sql", "SELECT 1;\n")

	if _, err := sqlFileEngine(sqlDialectTestContext(root), target); err == nil {
		t.Fatal("resolve SQL engine succeeded with symlink config")
	}
}

func sqlDialectTestContext(root string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
}

func writeSQLDialectTestFile(t *testing.T, root, relative, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
