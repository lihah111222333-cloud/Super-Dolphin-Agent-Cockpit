package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRemoteCIExactTimingAuthorityContract keeps the accepted timing ledger
// authority structural: schema, DTO, aggregation, ECI timestamps, and the
// human projection must move together instead of growing a second authority.
func TestRemoteCIExactTimingAuthorityContract(t *testing.T) {
	root := exactTimingRepoRoot(t)
	gate := parseExactTimingFile(t, filepath.Join(root, "internal/devtools/gate/timing_observation.go"))
	remote := parseExactTimingFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_timing.go"))
	wait := parseExactTimingFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_wait.go"))
	human := parseExactTimingFile(t, filepath.Join(root, "internal/devtools/remoteci/human_timing_ledger.go"))

	assertExactTimingSchema(t, root)
	if !exactTimingStructHasField(gate, "TimingObservation", "DurationMS") {
		t.Error("TimingObservation must retain DurationMS for persisted authority")
	}
	if !exactTimingDurationRule(gate) {
		t.Error("raw and critical_path duration_ms must equal their real intervals")
	}
	if !exactTimingUnion(gate, "validateShardIntervalUnion") || !exactTimingUnion(remote, "mergeWorkloadIntervals") {
		t.Error("interval_union must merge every raw workload interval, not use a min/max envelope")
	}
	if !exactTimingECIProviderBinding(wait) {
		t.Error("ECI timing must bind CreationTime, materializer StartTime, and provider terminal time without local-clock authority")
	}
	if !exactTimingHumanLedger(human) {
		t.Error("human timing ledger must project committed SQLite TimingObservations through LoadRemoteCIRun")
	}
}

// TestRemoteCIExactTimingGuardFailsClosed proves the predicates reject the
// concrete regressions that would otherwise look plausible in a text review.
func TestRemoteCIExactTimingGuardFailsClosed(t *testing.T) {
	if exactTimingStructHasField(parseExactTimingSource(t, "package gate; type TimingObservation struct{}"), "TimingObservation", "DurationMS") {
		t.Fatal("missing DurationMS counterexample unexpectedly passed")
	}
	if exactTimingUnion(parseExactTimingSource(t, "package remoteci; func mergeWorkloadIntervals() { earliest := 0; latest := 1; _ = latest-earliest }"), "mergeWorkloadIntervals") {
		t.Fatal("min/max envelope counterexample unexpectedly passed")
	}
	if exactTimingECIProviderBinding(parseExactTimingSource(t, "package remoteci; import \"time\"; func bindObservedECIShardTiming() { _ = time.Now() }")) {
		t.Fatal("local polling clock counterexample unexpectedly passed")
	}
}

func exactTimingRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(directory, "..", ".."))
}

func parseExactTimingFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func parseExactTimingSource(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "counterexample.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func assertExactTimingSchema(t *testing.T, root string) {
	t.Helper()
	contract, err := os.ReadFile(filepath.Join(root, "internal/devtools/cicontract/contract.go"))
	if err != nil || !strings.Contains(string(contract), `TimingObservationsTable = "ci_timing_observations"`) {
		t.Fatal("ci_timing_observations must remain the sole timing authority table owner")
	}
	schema, err := os.ReadFile(filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema.go"))
	if err != nil || !strings.Contains(string(schema), "CREATE TABLE IF NOT EXISTS ci_timing_observations") || !strings.Contains(string(schema), "duration_ms INTEGER NOT NULL") {
		t.Fatal("ci_timing_observations schema must persist duration_ms")
	}
}

func exactTimingStructHasField(file *ast.File, typeName, fieldName string) bool {
	return exactTimingStructFieldNamed(exactTimingNamedStruct(file, typeName), fieldName)
}

// exactTimingNamedStruct 只返回守卫目标的具名结构，避免字段断言承担声明筛选。
func exactTimingNamedStruct(file *ast.File, typeName string) *ast.StructType {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			structType, isStruct := typeSpec.Type.(*ast.StructType)
			if !ok || !isStruct || typeSpec.Name.Name != typeName {
				continue
			}
			return structType
		}
	}
	return nil
}

// exactTimingStructFieldNamed 保持匿名字段不被误当作目标具名字段。
func exactTimingStructFieldNamed(structType *ast.StructType, fieldName string) bool {
	if structType == nil {
		return false
	}
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			if name.Name == fieldName {
				return true
			}
		}
	}
	return false
}

func exactTimingDurationRule(file *ast.File) bool {
	function := exactTimingMethod(file, "TimingObservation", "Validate")
	return exactTimingIdentifiers(function, "TimingAggregationRaw", "TimingAggregationCriticalPath") &&
		exactTimingComparison(function, "DurationMS", "envelopeMS")
}

func exactTimingUnion(file *ast.File, functionName string) bool {
	function := exactTimingFunction(file, functionName)
	return exactTimingCall(function, "Slice") &&
		exactTimingAddAssign(function, "durationMS") &&
		exactTimingCall(function, "Milliseconds")
}

func exactTimingECIProviderBinding(file *ast.File) bool {
	function := exactTimingFunction(file, "bindObservedECIShardTiming")
	materializer := exactTimingFunction(file, "observedECIMaterializerStartTime")
	return !exactTimingCall(function, "Now") &&
		exactTimingSelector(function, "group", "CreationTime") &&
		exactTimingCall(function, "observedECIMaterializerStartTime") &&
		exactTimingSelector(materializer, "container", "CurrentState", "StartTime") &&
		exactTimingIdentifiers(function, "SucceededTime", "FailedTime", "terminalAt")
}

func exactTimingHumanLedger(file *ast.File) bool {
	function := exactTimingFunction(file, "RenderHumanTimingLedgerFromAuthority")
	return exactTimingCall(function, "LoadRemoteCIRun") && exactTimingSelector(function, "record", "TimingObservations")
}

func exactTimingFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func exactTimingMethod(file *ast.File, receiverType, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name || function.Recv == nil || len(function.Recv.List) != 1 {
			continue
		}
		if exactTimingExprName(function.Recv.List[0].Type) == receiverType {
			return function
		}
	}
	return nil
}

func exactTimingIdentifiers(function *ast.FuncDecl, names ...string) bool {
	if function == nil {
		return false
	}
	found := make(map[string]bool, len(names))
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			found[identifier.Name] = true
		}
		return true
	})
	for _, name := range names {
		if !found[name] {
			return false
		}
	}
	return true
}

func exactTimingCall(function *ast.FuncDecl, name string) bool {
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && exactTimingCalledName(call.Fun) == name {
			found = true
		}
		return !found
	})
	return found
}

func exactTimingCalledName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	default:
		return ""
	}
}

func exactTimingComparison(function *ast.FuncDecl, left, right string) bool {
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if ok && binary.Op == token.NEQ && exactTimingExprName(binary.X) == left && exactTimingExprName(binary.Y) == right {
			found = true
		}
		return !found
	})
	return found
}

func exactTimingAddAssign(function *ast.FuncDecl, fieldName string) bool {
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || assignment.Tok != token.ADD_ASSIGN {
			return true
		}
		for _, left := range assignment.Lhs {
			if exactTimingExprName(left) == fieldName {
				found = true
			}
		}
		return !found
	})
	return found
}

func exactTimingSelector(function *ast.FuncDecl, root string, path ...string) bool {
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if exactTimingSelectorPath(node, root, path...) {
			found = true
		}
		return !found
	})
	return found
}

func exactTimingSelectorPath(node ast.Node, root string, path ...string) bool {
	expression, ok := node.(ast.Expr)
	if !ok || len(path) == 0 {
		return false
	}
	for _, fieldName := range slices.Backward(path) {
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != fieldName {
			return false
		}
		expression = selector.X
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == root
}

func exactTimingExprName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	default:
		return ""
	}
}
