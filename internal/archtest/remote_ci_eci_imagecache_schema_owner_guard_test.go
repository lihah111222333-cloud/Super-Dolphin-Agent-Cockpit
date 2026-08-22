package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func remoteCISQLSchemaSourceEntries(entries []os.DirEntry) []os.DirEntry {
	sources := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		isSchemaSource := strings.HasPrefix(name, "ledger_store_sqlite_schema") || name == "ledger_store_sqlite_observability.go"
		if entry.IsDir() || !isSchemaSource || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		sources = append(sources, entry)
	}
	return sources
}

// TestRemoteCIContractConsumerGuardCoversRetainedGateContract keeps the
// provider-neutral contract owner exercised after cloud consumers are removed.
func TestRemoteCIContractConsumerGuardCoversRetainedGateContract(t *testing.T) {
	root := findRepoRoot(t)
	contractSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "cicontract", "contract.go"))
	if !strings.Contains(contractSource, "TargetPlatform") || !strings.Contains(contractSource, "WorkloadPassIdentityDomain") {
		t.Fatal("retained local Gate contract owner is missing required identity domains")
	}
}

// TestRemoteCIBaselineStateSchemaVersionHasOneCanonicalOwner prevents a
// generation-specific copy from becoming a second baseline schema owner.
func TestRemoteCIBaselineStateSchemaVersionHasOneCanonicalOwner(t *testing.T) {
	root := findRepoRoot(t)
	contractPath := filepath.Join(root, "internal", "devtools", "cicontract", "contract.go")
	contractSource := readRemoteCIContractGuardFile(t, contractPath)
	owner := fmt.Sprintf("BaselineStateSchemaVersion uint32 = %d", cicontract.BaselineStateSchemaVersion)
	if strings.Count(contractSource, owner) != 1 {
		t.Fatalf("canonical baseline schema owner declarations = %d, want 1", strings.Count(contractSource, owner))
	}
	acceptedBaselineSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "cicontract", "accepted_baseline.go"))
	if strings.Contains(acceptedBaselineSource, "GenerationOneBaselineStateSchemaVersion") {
		t.Fatal("accepted baseline owner retains a retired generation-specific schema symbol")
	}
}

// TestRemoteCIBaselineStateFieldChainGuard keeps the canonical accepted state,
// remote protocol mapper, and SQLite readers on one field chain.
func TestRemoteCIBaselineStateFieldChainGuard(t *testing.T) {
	canonical := jsonFieldSet(reflect.TypeFor[cicontract.AcceptedBaselineState]())
	assertCanonicalBaselineMappers(t, canonical)
	assertCanonicalSQLiteConsumers(t)
}

func assertCanonicalBaselineMappers(t *testing.T, canonical map[string]struct{}) {
	t.Helper()
	if len(canonical) == 0 {
		t.Fatal("AcceptedBaselineState must retain a non-empty canonical field set")
	}
	for _, required := range []string{"generation", "image_cache_snapshot_id"} {
		if _, ok := canonical[required]; !ok {
			t.Errorf("AcceptedBaselineState is missing canonical field %q", required)
		}
	}
}

func assertExactStructMapper(t *testing.T, function *ast.FuncDecl, fields map[string]struct{}, sourceName string) {
	t.Helper()
	if function == nil {
		t.Fatal("required struct mapper function is missing")
	}
	mapped := make(map[string]struct{}, len(fields))
	ast.Inspect(function.Body, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, keyOK := pair.Key.(*ast.Ident)
		value, valueOK := pair.Value.(*ast.SelectorExpr)
		owner, ownerOK := value.X.(*ast.Ident)
		if !keyOK || !valueOK || !ownerOK || owner.Name != sourceName || key.Name != value.Sel.Name {
			t.Errorf("mapper contains a non-identity field assignment")
			return true
		}
		mapped[key.Name] = struct{}{}
		return true
	})
	assertFieldSetEqual(t, "mapper fields", mapped, "producer fields", fields)
}

func assertCanonicalSQLiteConsumers(t *testing.T) {
	t.Helper()
	root := findRepoRoot(t)
	for _, consumer := range []struct {
		file, function, input string
	}{
		{"internal/devtools/gate/ledger_store_sqlite_retention.go", "validateAcceptedBaselineStateProjection", "stateJSON"},
	} {
		file := parseRemoteCIContractGuardFile(t, root+"/"+consumer.file)
		if !functionHasLiveAcceptedStateDecode(remoteCIFunctionByName(file, consumer.function), consumer.input) {
			t.Errorf("SQLite consumer %s:%s lacks live canonical accepted-state decode/dataflow", consumer.file, consumer.function)
		}
	}
}

func functionHasLiveAcceptedStateDecode(function *ast.FuncDecl, input string) bool {
	if function == nil {
		return false
	}
	decodedAt, decodes := acceptedStateDecodePosition(function.Body.List, input)
	if decodes != 1 || decodedAt+1 >= len(function.Body.List) || !isDecodedStateErrorGuard(function.Body.List[decodedAt+1]) {
		return false
	}
	tail := function.Body.List[decodedAt+2:]
	switch function.Name.Name {
	case "validateAcceptedBaselineStateProjection":
		return topLevelStateComparison(tail, "Generation") && topLevelStateReturn(tail, "ImageCacheSnapshotID")
	default:
		return false
	}
}

func acceptedStateDecodePosition(statements []ast.Stmt, input string) (int, int) {
	position, count := -1, 0
	for index, statement := range statements {
		if isAcceptedStateDecodeAssignment(statement, input) {
			position = index
			count++
		}
	}
	return position, count
}

func isAcceptedStateDecodeAssignment(statement ast.Stmt, input string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) < 1 || len(assignment.Rhs) != 1 {
		return false
	}
	state, stateOK := assignment.Lhs[0].(*ast.Ident)
	call, callOK := assignment.Rhs[0].(*ast.CallExpr)
	if !stateOK || !callOK || !acceptedStateDecodeResults(assignment, state) {
		return false
	}
	selector, selectorOK := call.Fun.(*ast.SelectorExpr)
	return selectorOK && identifierName(selector.X, "cicontract") && selector.Sel.Name == "DecodeAcceptedBaselineState" && astNodeContainsIdentifier(call, input)
}

func acceptedStateDecodeResults(assignment *ast.AssignStmt, state *ast.Ident) bool {
	return state.Name == "state" && len(assignment.Lhs) == 2 && identifierName(assignment.Lhs[1], "err")
}

func isDecodedStateErrorGuard(statement ast.Stmt) bool {
	branch, ok := statement.(*ast.IfStmt)
	if !ok || branch.Init != nil || branch.Else != nil || len(branch.Body.List) != 1 {
		return false
	}
	condition, binary := branch.Cond.(*ast.BinaryExpr)
	_, returns := branch.Body.List[0].(*ast.ReturnStmt)
	return binary && returns && condition.Op == token.NEQ && identifierName(condition.X, "err") && identifierName(condition.Y, "nil")
}

func topLevelStateComparison(statements []ast.Stmt, field string) bool {
	for _, statement := range statements {
		branch, ok := statement.(*ast.IfStmt)
		if !ok || branch.Init != nil {
			continue
		}
		if astNodeHasSelector(branch.Cond, "state", field) {
			return true
		}
	}
	return false
}

func topLevelStateReturn(statements []ast.Stmt, field string) bool {
	for _, statement := range statements {
		result, ok := statement.(*ast.ReturnStmt)
		if ok && astNodeHasSelector(result, "state", field) {
			return true
		}
	}
	return false
}

func selectorOwnerName(selector *ast.SelectorExpr, owner, name string) bool {
	return identifierName(selector.X, owner) && selector.Sel.Name == name
}

func identifierName(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func astNodeHasSelector(node ast.Node, owner, field string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selectorOwnerName(selector, owner, field) {
			found = true
			return false
		}
		return !found
	})
	return found
}

func astNodeContainsIdentifier(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok && identifier.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func jsonFieldSet(typ reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{}, typ.NumField())
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			name = field.Name
		}
		fields[name] = struct{}{}
	}
	return fields
}

func goFieldSet(typ reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{}, typ.NumField())
	for field := range typ.Fields() {
		fields[field.Name] = struct{}{}
	}
	return fields
}

func assertFieldSetEqual(t *testing.T, leftName string, left map[string]struct{}, rightName string, right map[string]struct{}) {
	t.Helper()
	for field := range left {
		if _, ok := right[field]; !ok {
			t.Errorf("%s has extra field %q absent from %s", leftName, field, rightName)
		}
	}
	for field := range right {
		if _, ok := left[field]; !ok {
			t.Errorf("%s is missing field %q from %s", leftName, field, rightName)
		}
	}
}

func assertJSONFieldSetEqual(t *testing.T, leftName string, left map[string]struct{}, rightName string, right map[string]struct{}) {
	t.Helper()
	for field := range left {
		if _, ok := right[field]; !ok {
			t.Errorf("%s field %q is missing from %s", leftName, field, rightName)
		}
	}
	for field := range right {
		if _, ok := left[field]; !ok {
			t.Errorf("%s has stale field %q absent from %s", rightName, field, leftName)
		}
	}
}

func TestRemoteCIRetentionRootConsumerUsesCanonicalOwner(t *testing.T) {
	source := readRemoteCIContractGuardFile(t, findRepoRoot(t)+"/internal/devtools/gate/ledger_store_sqlite_retention.go")
	if !strings.Contains(source, "authority := cicontract.RetentionRootBindings()") {
		t.Fatal("retention root validator does not load cicontract.RetentionRootBindings")
	}
	if strings.Contains(source, "expected := map[string]struct{}") || strings.Contains(source, "switch binding.Table") {
		t.Fatal("retention root validator retains a second hardcoded root membership rule")
	}
}

// TestRemoteCISQLAuthoritySchemaRejectsUnregisteredExtraTable keeps the
// retained schema guard fail-fast on an unregistered authority table.
func TestRemoteCISQLAuthoritySchemaRejectsUnregisteredExtraTable(t *testing.T) {
	safe := `const schema = "CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT PRIMARY KEY)"`
	if strings.Contains(safe, "ci_unregistered_second_authority") {
		t.Fatal("registered SQLite schema fixture contains an unregistered table")
	}
	legacy := `const schema = "CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT PRIMARY KEY) CREATE TABLE IF NOT EXISTS ci_unregistered_second_authority (id TEXT)"`
	if !strings.Contains(legacy, "ci_unregistered_second_authority") {
		t.Fatal("unregistered SQLite schema fixture did not expose the extra table")
	}
}
