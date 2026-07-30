package archtest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"golang.org/x/tools/go/packages"
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

type providerRecoveryDispatchMutation struct {
	name   string
	source string
}

var providerRecoveryDispatchMutations = []providerRecoveryDispatchMutation{
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
		name: "valid branch plus stale constant alias",
		source: `package sqlite
const staleProviderRecoveryMigration = "120_agent_provider_binding_recovery_owner.sql"
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	if name == staleProviderRecoveryMigration {
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "valid branch plus constant concat duplicate",
		source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	if name == "123_agent_provider_binding_" + "recovery_owner.sql" {
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "valid branch plus dynamic comparison",
		source: `package sqlite
var dynamicProviderRecoveryMigration string
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	if name == dynamicProviderRecoveryMigration {
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "valid branch plus switch duplicate",
		source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	switch name {
	case "123_agent_provider_binding_recovery_owner.sql":
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "local same name wrong callee",
		source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	migrateAgentProviderBindingRecoveryOwner := migrateSystemLogsTraceSpan
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "valid branch plus block local wrong function value",
		source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	{
		migrateAgentProviderBindingRecoveryOwner := migrateSystemLogsTraceSpan
		_ = migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "if init shadowed ctx",
		source: `package sqlite
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if ctx := tx; name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "selector wrong owner",
		source: `package sqlite
type wrongRecoveryOwner struct{}
func (wrongRecoveryOwner) migrateAgentProviderBindingRecoveryOwner(ctx, tx any) error {
	return nil
}
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	var wrong wrongRecoveryOwner
	if body == "selector-decoy" {
		return wrong.migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`,
	},
	{
		name: "valid branch plus top level selector wrong owner",
		source: `package sqlite
func migrateAgentProviderBindingRecoveryOwner(ctx, tx any) error { return nil }
type wrongTopLevelRecoveryOwner struct{}
func (wrongTopLevelRecoveryOwner) migrateAgentProviderBindingRecoveryOwner(ctx, tx any) error {
	return nil
}
func executeMigrationBody(ctx any, tx any, name, body string) error {
	var wrong wrongTopLevelRecoveryOwner
	_ = wrong.migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
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

func TestProviderRecoveryMigrationVersionFieldGuardRejectsDispatchASTDrift(t *testing.T) {
	const migrationName = "123_agent_provider_binding_recovery_owner.sql"
	for _, test := range providerRecoveryDispatchMutations {
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

func TestProviderRecoveryDispatchSelectsTopLevelFunctionObject(t *testing.T) {
	root := writeProviderRecoveryDispatchSource(t, `package sqlite
type recoveryOwnerMethodDeclaredFirst struct{}
func (recoveryOwnerMethodDeclaredFirst) migrateAgentProviderBindingRecoveryOwner(ctx, tx any) error {
	return nil
}
func executeMigrationBody(ctx any, tx any, name, body string) error {
	if name == "123_agent_provider_binding_recovery_owner.sql" {
		return migrateAgentProviderBindingRecoveryOwner(ctx, tx)
	}
	return nil
}
`)
	names := providerRecoveryDispatchNames(t, root)
	if len(names) != 1 || names[0] != "123_agent_provider_binding_recovery_owner.sql" {
		t.Fatalf("provider recovery dispatch names = %v, want canonical top-level dispatch", names)
	}
}

func writeProviderRecoveryDispatchSource(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "db", "sqlite")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create synthetic sqlite package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module synthetic\n\ngo 1.25.7\n"), 0o600); err != nil {
		t.Fatalf("write synthetic go.mod: %v", err)
	}
	body := source
	if !strings.Contains(source, "func migrateAgentProviderBindingRecoveryOwner(") {
		body += "\nfunc migrateAgentProviderBindingRecoveryOwner(ctx, tx any) error { return nil }\n"
	}
	body += "\nfunc migrateSystemLogsTraceSpan(ctx, tx any) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(dir, "migrate.go"), []byte(body), 0o600); err != nil {
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
	loaded := loadProviderRecoveryTypedSource(t, root)
	executeMigrationBody := findExecuteMigrationBody(t, loaded.file)
	scanner := providerRecoveryDispatchScanner{
		info:         loaded.info,
		nameObject:   findFunctionParameterObject(t, executeMigrationBody, loaded.info, "name"),
		ctxObject:    findFunctionParameterObject(t, executeMigrationBody, loaded.info, "ctx"),
		txObject:     findFunctionParameterObject(t, executeMigrationBody, loaded.info, "tx"),
		calleeObject: findNamedFunctionObject(t, loaded.file, loaded.info, "migrateAgentProviderBindingRecoveryOwner"),
		handled:      make(map[*ast.Ident]struct{}),
		handledCalls: make(map[*ast.CallExpr]struct{}),
	}
	ast.Inspect(executeMigrationBody.Body, scanner.inspect)
	scanner.rejectUnhandledNameUses(executeMigrationBody.Body)
	scanner.rejectUnexpectedProviderRecoveryCalls(executeMigrationBody.Body)
	return scanner.names
}

type providerRecoveryTypedSource struct {
	file *ast.File
	info *types.Info
}

func loadProviderRecoveryTypedSource(t *testing.T, root string) providerRecoveryTypedSource {
	t.Helper()
	config := &packages.Config{Mode: packages.LoadSyntax, Dir: root}
	loaded, err := packages.Load(config, "./internal/platform/db/sqlite")
	if err != nil {
		t.Fatalf("load sqlite package: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded sqlite packages = %d, want 1", len(loaded))
	}
	if len(loaded[0].Errors) != 0 {
		t.Fatalf("type-check sqlite package: %v", loaded[0].Errors)
	}
	return providerRecoveryTypedSource{
		file: findMigrateSyntax(t, loaded[0]),
		info: loaded[0].TypesInfo,
	}
}

func findMigrateSyntax(t *testing.T, loaded *packages.Package) *ast.File {
	t.Helper()
	for index, filename := range loaded.CompiledGoFiles {
		if filepath.Base(filename) == "migrate.go" {
			return loaded.Syntax[index]
		}
	}
	t.Fatalf("loaded sqlite package does not contain migrate.go")
	return nil
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

func findFunctionParameterObject(
	t *testing.T,
	function *ast.FuncDecl,
	info *types.Info,
	name string,
) types.Object {
	t.Helper()
	for _, field := range function.Type.Params.List {
		for _, identifier := range field.Names {
			if identifier.Name == name && info.Defs[identifier] != nil {
				return info.Defs[identifier]
			}
		}
	}
	t.Fatalf("%s does not define the %s parameter", function.Name.Name, name)
	return nil
}

func findNamedFunctionObject(
	t *testing.T,
	file *ast.File,
	info *types.Info,
	name string,
) types.Object {
	t.Helper()
	var object types.Object
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name.Name != name || info.Defs[function.Name] == nil {
			continue
		}
		if object != nil {
			t.Fatalf("migrate.go contains duplicate top-level %s declarations", name)
		}
		object = info.Defs[function.Name]
	}
	if object != nil {
		return object
	}
	t.Fatalf("migrate.go does not define %s", name)
	return nil
}

type providerRecoveryDispatchScanner struct {
	info         *types.Info
	nameObject   types.Object
	ctxObject    types.Object
	txObject     types.Object
	calleeObject types.Object
	handled      map[*ast.Ident]struct{}
	handledCalls map[*ast.CallExpr]struct{}
	names        []string
}

func (scanner *providerRecoveryDispatchScanner) inspect(node ast.Node) bool {
	switch node := node.(type) {
	case *ast.IfStmt:
		scanner.inspectIf(node)
	case *ast.SwitchStmt:
		scanner.inspectSwitch(node)
	}
	return true
}

func (scanner *providerRecoveryDispatchScanner) inspectIf(branch *ast.IfStmt) {
	uses := parameterUses(branch.Cond, scanner.info, scanner.nameObject)
	candidate := len(uses) != 0 ||
		containsRecoveryConstant(branch.Cond, scanner.info) ||
		containsProviderRecoveryCall(branch.Body)
	if !candidate {
		return
	}
	scanner.markHandled(uses)
	name, ok := nameComparisonConstant(branch.Cond, scanner.info, scanner.nameObject)
	if !ok {
		scanner.names = append(scanner.names, "invalid-condition")
		return
	}
	if !strings.HasSuffix(name, "_agent_provider_binding_recovery_owner.sql") {
		return
	}
	call := providerRecoveryDispatchReturnCall(branch.Body)
	if call == nil || !isProviderRecoveryDispatchCall(
		call, scanner.info, scanner.calleeObject, scanner.ctxObject, scanner.txObject,
	) {
		scanner.names = append(scanner.names, "invalid-return:"+name)
		return
	}
	scanner.handledCalls[call] = struct{}{}
	scanner.names = append(scanner.names, name)
}

func (scanner *providerRecoveryDispatchScanner) inspectSwitch(branch *ast.SwitchStmt) {
	uses := parameterUses(branch, scanner.info, scanner.nameObject)
	candidate := len(uses) != 0 ||
		containsRecoveryConstant(branch, scanner.info) ||
		containsProviderRecoveryCall(branch)
	if !candidate {
		return
	}
	scanner.markHandled(uses)
	scanner.names = append(scanner.names, "invalid-switch")
}

func (scanner *providerRecoveryDispatchScanner) markHandled(identifiers []*ast.Ident) {
	for _, identifier := range identifiers {
		scanner.handled[identifier] = struct{}{}
	}
}

func (scanner *providerRecoveryDispatchScanner) rejectUnhandledNameUses(body *ast.BlockStmt) {
	rejected := false
	ast.Inspect(body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || scanner.info.Uses[identifier] != scanner.nameObject {
			return true
		}
		if _, ok := scanner.handled[identifier]; ok || rejected {
			return true
		}
		scanner.names = append(scanner.names, "invalid-name-dispatch")
		rejected = true
		return true
	})
}

func (scanner *providerRecoveryDispatchScanner) rejectUnexpectedProviderRecoveryCalls(body *ast.BlockStmt) {
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || providerRecoveryCalleeName(call.Fun) != "migrateAgentProviderBindingRecoveryOwner" {
			return true
		}
		if _, ok := scanner.handledCalls[call]; ok {
			return true
		}
		scanner.names = append(scanner.names, "invalid-provider-recovery-call")
		return true
	})
}

func parameterUses(node ast.Node, info *types.Info, object types.Object) []*ast.Ident {
	var identifiers []*ast.Ident
	ast.Inspect(node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && info.Uses[identifier] == object {
			identifiers = append(identifiers, identifier)
		}
		return true
	})
	return identifiers
}

func containsRecoveryConstant(node ast.Node, info *types.Info) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		expression, ok := node.(ast.Expr)
		if !ok || found {
			return true
		}
		value, ok := constantStringValue(expression, info)
		found = ok && strings.HasSuffix(value, "_agent_provider_binding_recovery_owner.sql")
		return true
	})
	return found
}

func containsProviderRecoveryCall(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || found {
			return true
		}
		found = providerRecoveryCalleeName(call.Fun) == "migrateAgentProviderBindingRecoveryOwner"
		return true
	})
	return found
}

func providerRecoveryCalleeName(expression ast.Expr) string {
	switch callee := expression.(type) {
	case *ast.Ident:
		return callee.Name
	case *ast.SelectorExpr:
		return callee.Sel.Name
	default:
		return ""
	}
}

func nameComparisonConstant(
	expression ast.Expr,
	info *types.Info,
	nameObject types.Object,
) (string, bool) {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return "", false
	}
	if isObjectIdentifier(comparison.X, info, nameObject) {
		return constantStringValue(comparison.Y, info)
	}
	if isObjectIdentifier(comparison.Y, info, nameObject) {
		return constantStringValue(comparison.X, info)
	}
	return "", false
}

func isObjectIdentifier(expression ast.Expr, info *types.Info, object types.Object) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && info.Uses[identifier] == object
}

func constantStringValue(expression ast.Expr, info *types.Info) (string, bool) {
	typed, ok := info.Types[expression]
	if !ok || typed.Value == nil || typed.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(typed.Value), true
}

func providerRecoveryDispatchReturnCall(body *ast.BlockStmt) *ast.CallExpr {
	if body == nil || len(body.List) != 1 {
		return nil
	}
	returnStatement, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		return nil
	}
	call, ok := returnStatement.Results[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	return call
}

func isProviderRecoveryDispatchCall(
	call *ast.CallExpr,
	info *types.Info,
	calleeObject types.Object,
	ctxObject types.Object,
	txObject types.Object,
) bool {
	if len(call.Args) != 2 {
		return false
	}
	if calledObject(call.Fun, info) != calleeObject {
		return false
	}
	return isObjectIdentifier(call.Args[0], info, ctxObject) &&
		isObjectIdentifier(call.Args[1], info, txObject)
}

func calledObject(expression ast.Expr, info *types.Info) types.Object {
	switch callee := expression.(type) {
	case *ast.Ident:
		return info.Uses[callee]
	case *ast.SelectorExpr:
		return info.Uses[callee.Sel]
	default:
		return nil
	}
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
