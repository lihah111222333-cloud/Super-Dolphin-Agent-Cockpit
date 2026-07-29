package archtest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"gopkg.in/yaml.v3"
)

const (
	providerRecoveryMigrationVersion = 123
	providerRecoveryMigrationSHA256  = "7dc7bb2fb1d3d38d4f37e9fb3a2b625a46e7d2263fbc6035568888ceae9daaa9"
)

type providerRecoveryMigrationContract struct {
	migrationName    string
	migrationVersion int
	migrationSHA     string
	dispatchNames    []string
	sqlcSchemaNames  []string
	schemaFloor      int
}

// TestProviderRecoveryMigrationVersionFieldGuard 动态锁定恢复迁移、运行时 dispatch、sqlc schema 与版本门槛。
func TestProviderRecoveryMigrationVersionFieldGuard(t *testing.T) {
	root := repoRoot(t)
	migrationName, migrationVersion, migrationSHA := discoverProviderRecoveryMigration(t, root)
	failIfViolations(t, providerRecoveryMigrationContractViolations(providerRecoveryMigrationContract{
		migrationName:    migrationName,
		migrationVersion: migrationVersion,
		migrationSHA:     migrationSHA,
		dispatchNames:    providerRecoveryDispatchNames(t, root),
		sqlcSchemaNames:  providerRecoverySQLCSchemaNames(t, root),
		schemaFloor:      platformdb.MinRequiredSchemaVersion,
	}))
}

func TestProviderRecoveryMigrationVersionFieldGuardRejectsDrift(t *testing.T) {
	valid := providerRecoveryMigrationContract{
		migrationName:    "123_agent_provider_binding_recovery_owner.sql",
		migrationVersion: providerRecoveryMigrationVersion,
		migrationSHA:     providerRecoveryMigrationSHA256,
		dispatchNames:    []string{"123_agent_provider_binding_recovery_owner.sql"},
		sqlcSchemaNames:  []string{"123_agent_provider_binding_recovery_owner.sql"},
		schemaFloor:      providerRecoveryMigrationVersion,
	}
	tests := []struct {
		name   string
		mutate func(*providerRecoveryMigrationContract)
	}{
		{name: "version conflict", mutate: func(contract *providerRecoveryMigrationContract) {
			contract.migrationName = "120_agent_provider_binding_recovery_owner.sql"
			contract.migrationVersion = 120
		}},
		{name: "SQL body drift", mutate: func(contract *providerRecoveryMigrationContract) {
			contract.migrationSHA = strings.Repeat("0", 64)
		}},
		{name: "stale dispatch", mutate: func(contract *providerRecoveryMigrationContract) {
			contract.dispatchNames = []string{"120_agent_provider_binding_recovery_owner.sql"}
		}},
		{name: "missing sqlc schema", mutate: func(contract *providerRecoveryMigrationContract) {
			contract.sqlcSchemaNames = nil
		}},
		{name: "duplicate dispatch owner", mutate: func(contract *providerRecoveryMigrationContract) {
			contract.dispatchNames = append(contract.dispatchNames, contract.migrationName)
		}},
		{name: "schema floor mismatch", mutate: func(contract *providerRecoveryMigrationContract) {
			contract.schemaFloor = 122
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := valid
			contract.dispatchNames = append([]string(nil), valid.dispatchNames...)
			contract.sqlcSchemaNames = append([]string(nil), valid.sqlcSchemaNames...)
			test.mutate(&contract)
			if violations := providerRecoveryMigrationContractViolations(contract); len(violations) == 0 {
				t.Fatalf("mutation %q produced no field guard violation", test.name)
			}
		})
	}
}

func TestProviderRecoveryMigrationVersionFieldGuardRejectsDispatchASTDrift(t *testing.T) {
	const migrationName = "123_agent_provider_binding_recovery_owner.sql"
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "decoy literal",
			source: `package sqlite
const decoy = "123_agent_provider_binding_recovery_owner.sql"
func executeMigrationBody(ctx any, tx any, name, body string) error { return nil }
`,
		},
		{
			name: "wrong callee",
			source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	return nil
}
`,
		},
		{
			name: "wrong arguments",
			source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(tx, ctx)
	}
	return nil
}
`,
		},
		{
			name: "wrong condition owner",
			source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if body == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
		},
		{
			name: "duplicate branch",
			source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
		},
		{
			name: "valid branch plus wrong callee duplicate",
			source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	return nil
}
`,
		},
		{
			name: "stale branch",
			source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "120_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeProviderRecoveryDispatchSource(t, test.source)
			contract := providerRecoveryMigrationContract{
				migrationName:    migrationName,
				migrationVersion: providerRecoveryMigrationVersion,
				migrationSHA:     providerRecoveryMigrationSHA256,
				dispatchNames:    providerRecoveryDispatchNames(t, root),
				sqlcSchemaNames:  []string{migrationName},
				schemaFloor:      providerRecoveryMigrationVersion,
			}
			if violations := providerRecoveryMigrationContractViolations(contract); len(violations) == 0 {
				t.Fatalf("dispatch AST mutation %q produced no field guard violation", test.name)
			}
		})
	}
}

func writeProviderRecoveryDispatchSource(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "db", "sqlite")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create synthetic sqlite package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "migrate.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write synthetic migrate.go: %v", err)
	}
	return root
}

func providerRecoveryMigrationContractViolations(contract providerRecoveryMigrationContract) []string {
	var violations []string
	wantName := fmt.Sprintf("%03d_agent_provider_binding_recovery_owner.sql", providerRecoveryMigrationVersion)
	if contract.migrationName != wantName || contract.migrationVersion != providerRecoveryMigrationVersion {
		violations = append(violations, fmt.Sprintf(
			"provider recovery migration = %s version %d, want %s version %d",
			contract.migrationName, contract.migrationVersion, wantName, providerRecoveryMigrationVersion,
		))
	}
	if contract.migrationSHA != providerRecoveryMigrationSHA256 {
		violations = append(violations, fmt.Sprintf(
			"provider recovery migration SHA256 = %s, want rename-only body %s",
			contract.migrationSHA, providerRecoveryMigrationSHA256,
		))
	}
	if len(contract.dispatchNames) != 1 || contract.dispatchNames[0] != contract.migrationName {
		violations = append(violations, fmt.Sprintf(
			"provider recovery dispatch names = %v, want [%s]",
			contract.dispatchNames, contract.migrationName,
		))
	}
	if len(contract.sqlcSchemaNames) != 1 || contract.sqlcSchemaNames[0] != contract.migrationName {
		violations = append(violations, fmt.Sprintf(
			"provider recovery sqlc schema names = %v, want [%s]",
			contract.sqlcSchemaNames, contract.migrationName,
		))
	}
	if contract.schemaFloor != contract.migrationVersion {
		violations = append(violations, fmt.Sprintf(
			"MinRequiredSchemaVersion = %d, want provider recovery migration version %d",
			contract.schemaFloor, contract.migrationVersion,
		))
	}
	return violations
}

func discoverProviderRecoveryMigration(t *testing.T, root string) (string, int, string) {
	t.Helper()
	pattern := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations", "*_agent_provider_binding_recovery_owner.sql")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob provider recovery migrations: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("provider recovery migration files = %v, want exactly one", paths)
	}
	name := filepath.Base(paths[0])
	match := regexp.MustCompile(`^(\d+)_agent_provider_binding_recovery_owner\.sql$`).FindStringSubmatch(name)
	if match == nil {
		t.Fatalf("provider recovery migration filename = %q, want numeric version prefix", name)
	}
	version, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("parse provider recovery migration version %q: %v", match[1], err)
	}
	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read provider recovery migration: %v", err)
	}
	sum := sha256.Sum256(body)
	return name, version, hex.EncodeToString(sum[:])
}

func providerRecoveryDispatchNames(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrate.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse migrate.go: %v", err)
	}
	executeMigrationBody := findExecuteMigrationBody(t, file)
	return collectProviderRecoveryDispatchNames(executeMigrationBody.Body)
}

func findExecuteMigrationBody(t *testing.T, file *ast.File) *ast.FuncDecl {
	t.Helper()
	var executeMigrationBody *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "executeMigrationBody" {
			continue
		}
		if executeMigrationBody != nil {
			t.Fatalf("migrate.go contains duplicate executeMigrationBody declarations")
		}
		executeMigrationBody = function
	}
	if executeMigrationBody == nil || executeMigrationBody.Body == nil {
		t.Fatalf("migrate.go does not define executeMigrationBody")
	}
	return executeMigrationBody
}

func collectProviderRecoveryDispatchNames(body *ast.BlockStmt) []string {
	var names []string
	ast.Inspect(body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		name, found := providerRecoveryOwnerLiteral(branch.Cond)
		if !found {
			return true
		}
		names = append(names, validateProviderRecoveryDispatchBranch(branch, name))
		return true
	})
	return names
}

func providerRecoveryOwnerLiteral(expression ast.Expr) (string, bool) {
	var name string
	ast.Inspect(expression, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING || name != "" {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.HasSuffix(value, "_agent_provider_binding_recovery_owner.sql") {
			name = value
		}
		return true
	})
	return name, name != ""
}

func validateProviderRecoveryDispatchBranch(branch *ast.IfStmt, name string) string {
	if !isProviderRecoveryDispatchCondition(branch.Cond, name) {
		return "invalid-condition:" + name
	}
	if !isProviderRecoveryDispatchReturn(branch.Body) {
		return "invalid-return:" + name
	}
	return name
}

func isProviderRecoveryDispatchCondition(expression ast.Expr, name string) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return false
	}
	return isNameIdentifier(comparison.X) && isStringLiteral(comparison.Y, name)
}

func isNameIdentifier(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "name"
}

func isStringLiteral(expression ast.Expr, expected string) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == expected
}

func isProviderRecoveryDispatchReturn(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) != 1 {
		return false
	}
	returnStatement, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		return false
	}
	call, ok := returnStatement.Results[0].(*ast.CallExpr)
	return ok && isProviderRecoveryDispatchCall(call)
}

func isProviderRecoveryDispatchCall(call *ast.CallExpr) bool {
	if len(call.Args) != 2 {
		return false
	}
	callee, ok := call.Fun.(*ast.Ident)
	if !ok || callee.Name != "migrateAgentProviderBindingRecoveryOwner" {
		return false
	}
	return isIdentifier(call.Args[0], "ctx") && isIdentifier(call.Args[1], "tx")
}

func isIdentifier(expression ast.Expr, expected string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == expected
}

func providerRecoverySQLCSchemaNames(t *testing.T, root string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		t.Fatalf("read sqlc.yaml: %v", err)
	}
	var config sqlcDatabaseConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse sqlc.yaml: %v", err)
	}
	if len(config.SQL) != 1 {
		t.Fatalf("sqlc.yaml sql entries = %d, want 1", len(config.SQL))
	}
	var names []string
	for _, schema := range config.SQL[0].Schema {
		name := filepath.Base(schema)
		if strings.HasSuffix(name, "_agent_provider_binding_recovery_owner.sql") {
			names = append(names, name)
		}
	}
	return names
}
