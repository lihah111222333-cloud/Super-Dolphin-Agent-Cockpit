package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const recoveryCustodySQLiteAuthorityOwner = "cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"

// recoveryCustodyCoordinatorOwner owns a separate coordinator queue database;
// it is not the authority DB or product DB and remains outside public Fx wiring.
const recoveryCustodyCoordinatorOwner = "cmd/mcp-orch/orchestration/recoverycustody/reconcilecoordinator"

const platformSQLiteImportPath = modulePath + "/internal/platform/db/sqlite"

// TestMCPOrchProductionDoesNotOpenRecoveryCustodyAuthoritySQLite 禁止普通生产代码绕过独立服务协议直接打开authority SQLite。
func TestMCPOrchProductionDoesNotOpenRecoveryCustodyAuthoritySQLite(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := orchestrationInternalBoundaryGoFiles(t, root, mcpOrchPkg)
	var violations []string
	for _, relPath := range files {
		if !recoveryCustodyAuthoritySQLiteGuardScansFile(relPath) {
			continue
		}
		fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)
		violations = append(violations, recoveryCustodyAuthoritySQLiteOpenViolations(fset, file, relPath)...)
	}
	failIfViolations(t, violations)
}

func TestRecoveryCustodyAuthoritySQLiteOpenGuardRejectsRawOpen(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "platform sqlite alias",
			source: `package fixture
import dbsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
func open() { _, _ = dbsqlite.Open(ctx, options) }
`,
			want: "dbsqlite.Open",
		},
		{
			name: "database sql OpenDB",
			source: `package fixture
import db "database/sql"
func open() { _ = db.OpenDB(connector) }
`,
			want: "db.OpenDB",
		},
		{
			name: "dot import",
			source: `package fixture
import . "database/sql"
func open() {}
`,
			want: "dot-imports database/sql",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "cmd/mcp-orch/runtime_fixture.go", tc.source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse raw SQLite open fixture: %v", err)
			}
			got := strings.Join(recoveryCustodyAuthoritySQLiteOpenViolations(fset, file, "cmd/mcp-orch/runtime_fixture.go"), "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("raw SQLite open violations = %q, want marker %q", got, tc.want)
			}
		})
	}
}

func TestRecoveryCustodyAuthoritySQLiteOpenGuardAllowsOwnedAndNonOpenCode(t *testing.T) {
	t.Parallel()

	for _, relPath := range []string{
		recoveryCustodySQLiteAuthorityOwner + "/authority.go",
		"cmd/mcp-orch/runtime_fixture_test.go",
	} {
		if recoveryCustodyAuthoritySQLiteGuardScansFile(relPath) {
			t.Errorf("guard scans allowed path %s", relPath)
		}
	}
	const source = `package fixture
import sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
func migrate() { _ = sqliteruntime.RunEmbeddedMigrations(ctx, db) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "cmd/mcp-orch/serve.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse allowed SQLite migration fixture: %v", err)
	}
	violations := recoveryCustodyAuthoritySQLiteOpenViolations(fset, file, "cmd/mcp-orch/serve.go")
	if len(violations) != 0 {
		t.Fatalf("RunEmbeddedMigrations violations = %v, want none", violations)
	}
}

func recoveryCustodyAuthoritySQLiteGuardScansFile(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	if !strings.HasSuffix(relPath, ".go") || strings.HasSuffix(relPath, "_test.go") {
		return false
	}
	return relPath != recoveryCustodySQLiteAuthorityOwner &&
		!strings.HasPrefix(relPath, recoveryCustodySQLiteAuthorityOwner+"/") &&
		relPath != recoveryCustodyCoordinatorOwner &&
		!strings.HasPrefix(relPath, recoveryCustodyCoordinatorOwner+"/")
}

func recoveryCustodyAuthoritySQLiteOpenViolations(fset *token.FileSet, file *ast.File, relPath string) []string {
	aliases, violations := recoveryCustodyAuthoritySQLiteOpenAliases(fset, file, relPath)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath, ok := aliases[qualifier.Name]
		if !ok || !recoveryCustodyAuthoritySQLiteOpenSymbol(importPath, selector.Sel.Name) {
			return true
		}
		violations = append(violations, fmt.Sprintf(
			"%s:%d calls %s.%s; ordinary mcp-orch production must use the product DB owner or an independent RecoveryCustody service adapter",
			relPath,
			fset.Position(call.Pos()).Line,
			qualifier.Name,
			selector.Sel.Name,
		))
		return true
	})
	return violations
}

func recoveryCustodyAuthoritySQLiteOpenAliases(fset *token.FileSet, file *ast.File, relPath string) (map[string]string, []string) {
	aliases := make(map[string]string)
	var violations []string
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s:%d has invalid import path %s", relPath, fset.Position(spec.Pos()).Line, spec.Path.Value))
			continue
		}
		if importPath != platformSQLiteImportPath && importPath != "database/sql" {
			continue
		}
		alias := pathpkg.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		switch alias {
		case "_":
			continue
		case ".":
			violations = append(violations, fmt.Sprintf("%s:%d dot-imports %s; direct SQLite open ownership must remain auditable", relPath, fset.Position(spec.Pos()).Line, importPath))
		default:
			aliases[alias] = importPath
		}
	}
	return aliases, violations
}

func recoveryCustodyAuthoritySQLiteOpenSymbol(importPath, symbol string) bool {
	switch importPath {
	case platformSQLiteImportPath:
		return symbol == "Open" || symbol == "OpenTest"
	case "database/sql":
		return symbol == "Open" || symbol == "OpenDB"
	default:
		return false
	}
}
