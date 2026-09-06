package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const recoveryCustodyServiceSurface = "cmd/mcp-recovery-custody"

// TestRecoveryCustodyServiceUsesPrivateSQLiteOnly 锁定外部 RecoveryCustody
// 服务只能通过独立 sqliteauthority owner 持久化，禁止 PostgreSQL、产品 DB
// 或服务进程自行取得通用数据库句柄。
func TestRecoveryCustodyServiceUsesPrivateSQLiteOnly(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := recoveryCustodyServiceProductionFiles(t, root)
	if len(files) == 0 {
		t.Fatal("RecoveryCustody service has no production Go files")
	}
	foundAuthorityOwner := false
	var violations []string
	for _, relPath := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			t.Fatalf("read RecoveryCustody service file %s: %v", relPath, err)
		}
		if strings.Contains(string(data), "recoverycustody/sqliteauthority") {
			foundAuthorityOwner = true
		}
		for _, violation := range recoveryCustodySQLiteOnlyViolations(relPath, string(data)) {
			violations = append(violations, violation)
		}
	}
	if !foundAuthorityOwner {
		violations = append(violations, "service does not reference the independent sqliteauthority owner")
	}
	if len(violations) > 0 {
		t.Fatalf("RecoveryCustody service SQLite-only guard failed:\n%s", strings.Join(violations, "\n"))
	}
}

func TestRecoveryCustodyServiceConfigUsesPrivateOnlySchema(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	path := filepath.Join(root, recoveryCustodyServiceSurface, "config.go")
	fields := recoveryCustodyServiceConfigFields(t, path)
	if fields["ExternalService"] != "`json:\"external_service\"`" {
		t.Fatalf("ExternalService JSON tag = %q, want explicit external_service", fields["ExternalService"])
	}
	for _, name := range []string{"ProductSQLitePath", "WorkspaceRoot"} {
		if fields[name] != "`json:\"-\"`" {
			t.Fatalf("%s JSON tag = %q, want hidden generic path", name, fields[name])
		}
	}
}

// recoveryCustodyServiceConfigFields 读取 service authority JSON carrier 的字段标签。
func recoveryCustodyServiceConfigFields(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse RecoveryCustody service config: %v", err)
	}
	var fields map[string]string
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "authorityConfigJSON" {
			return true
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		fields = make(map[string]string)
		for _, field := range structType.Fields.List {
			if len(field.Names) != 1 || field.Tag == nil {
				continue
			}
			fields[field.Names[0].Name] = field.Tag.Value
		}
		return false
	})
	if fields == nil {
		t.Fatal("authorityConfigJSON type was not found")
	}
	return fields
}

// TestRecoveryCustodySQLiteOnlyGuardRejectsPostgreSQLAndDirectDBFixtures 锁定
// 守卫本身会拒绝常见 PG、产品数据库和通用 database/sql 绕过路径。
func TestRecoveryCustodySQLiteOnlyGuardRejectsPostgreSQLAndDirectDBFixtures(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"pgx import": `package fixture
import _ "github.com/jackc/pgx/v5"
`,
		"postgres DSN": `package fixture
const dsn = "postgres://127.0.0.1:5432/recovery"
`,
		"direct product db": `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
`,
		"generic database sql": `package fixture
import _ "database/sql"
`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) { assertForbiddenRecoveryCustodyFixture(t, source) })
	}
	valid := `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"
`
	assertAllowedRecoveryCustodyFixture(t, valid)
}

// assertForbiddenRecoveryCustodyFixture 确认服务守卫拒绝非 SQLite owner 依赖。
func assertForbiddenRecoveryCustodyFixture(t *testing.T, source string) {
	t.Helper()
	if violations := recoveryCustodySQLiteOnlyViolations("cmd/mcp-recovery-custody/fixture.go", source); len(violations) == 0 {
		t.Fatal("SQLite-only guard accepted forbidden fixture")
	}
}

// assertAllowedRecoveryCustodyFixture 确认独立 sqliteauthority owner 依赖被允许。
func assertAllowedRecoveryCustodyFixture(t *testing.T, source string) {
	t.Helper()
	if violations := recoveryCustodySQLiteOnlyViolations("cmd/mcp-recovery-custody/fixture.go", source); len(violations) != 0 {
		t.Fatalf("SQLite authority owner fixture was rejected: %v", violations)
	}
}

// recoveryCustodyServiceProductionFiles 返回服务 surface 下的生产 Go 文件。
func recoveryCustodyServiceProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(recoveryCustodyServiceSurface)), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relPath))
		return nil
	})
	if err != nil {
		t.Fatalf("walk RecoveryCustody service: %v", err)
	}
	return files
}

// recoveryCustodySQLiteOnlyViolations 对单个服务源码执行无状态 SQLite-only 检查。
func recoveryCustodySQLiteOnlyViolations(relPath, source string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), relPath, []byte(source), parser.SkipObjectResolution)
	if err != nil {
		return []string{fmt.Sprintf("%s: parse failed: %v", relPath, err)}
	}
	var violations []string
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: invalid import path", relPath))
			continue
		}
		if recoveryCustodyForbiddenImport(importPath) {
			violations = append(violations, fmt.Sprintf("%s: forbidden non-private-SQLite import %q", relPath, importPath))
		}
	}
	lower := strings.ToLower(source)
	for _, marker := range []string{"postgres://", "postgresql://", "pgsql", "postgres_connection_string"} {
		if strings.Contains(lower, marker) {
			violations = append(violations, fmt.Sprintf("%s: forbidden PostgreSQL marker %q", relPath, marker))
		}
	}
	return violations
}

// recoveryCustodyForbiddenImport 列出服务不得直接拥有的数据库实现依赖。
func recoveryCustodyForbiddenImport(importPath string) bool {
	return strings.HasPrefix(importPath, "github.com/jackc/pgx") ||
		strings.HasPrefix(importPath, "github.com/lib/pq") ||
		strings.HasPrefix(importPath, "gorm.io/driver/postgres") ||
		importPath == "database/sql" ||
		strings.HasPrefix(importPath, "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db") ||
		strings.HasPrefix(importPath, "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store")
}
