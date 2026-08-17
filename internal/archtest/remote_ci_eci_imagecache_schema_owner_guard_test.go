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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
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

// TestRemoteCIContractConsumerGuardCoversECI keeps Alibaba ECI consumers under
// the same canonical contract-value ownership guard as the gate packages.
func TestRemoteCIContractConsumerGuardCoversECI(t *testing.T) {
	root := findRepoRoot(t)
	const eciRoot = "internal/devtools/alicloud/eci/"
	covered := false
	for _, file := range remoteCIContractConsumerFiles(t, root) {
		if strings.Contains(filepath.ToSlash(file), eciRoot) {
			covered = true
			break
		}
	}
	if !covered {
		t.Fatalf("remote CI contract consumers do not include %s", eciRoot)
	}

	unsafe := remoteCIParseGuardFixture(t, `package fixture
const platform = "linux/amd64"
`)
	if !remoteCIRepeatsContractValue(unsafe) || remoteCIImportsContractOwner(unsafe) {
		t.Fatal("consumer owner guard fixture did not model an unowned repeated contract value")
	}
	safe := remoteCIParseGuardFixture(t, `package fixture
import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
const platform = cicontract.TargetPlatform
`)
	if remoteCIRepeatsContractValue(safe) && !remoteCIImportsContractOwner(safe) {
		t.Fatal("consumer owner guard rejected an owner-backed contract value")
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
	for _, relative := range []string{
		"internal/devtools/cicontract/generation_one.go",
		"internal/devtools/gate/remote_baseline_generation_one.go",
		"internal/devtools/gate/remote_baseline_generation_one_test.go",
	} {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		if strings.Contains(source, "GenerationOneBaselineStateSchemaVersion") {
			t.Errorf("%s retains the retired duplicate generation-one baseline schema owner", relative)
		}
		if strings.HasPrefix(relative, "internal/devtools/gate/") && !strings.Contains(source, "cicontract.BaselineStateSchemaVersion") && !strings.Contains(source, "DecodeAcceptedBaselineState") {
			t.Errorf("%s does not consume cicontract.BaselineStateSchemaVersion", relative)
		}
	}
}

// TestRemoteCIBaselineStateFieldChainGuard keeps the canonical accepted state,
// receipt mapper, remote protocol mapper, and SQLite readers on one field chain.
func TestRemoteCIBaselineStateFieldChainGuard(t *testing.T) {
	canonical := jsonFieldSet(reflect.TypeFor[cicontract.AcceptedBaselineState]())
	assertCanonicalBaselineMappers(t, canonical)
	assertCanonicalReceiptBindings(t, canonical)
	assertCanonicalSQLiteConsumers(t)
}

func TestRemoteCIReceiptStateBindingGuardRejectsMutations(t *testing.T) {
	source := readRemoteCIContractGuardFile(t, findRepoRoot(t)+"/internal/devtools/cicontract/generation_one.go")
	required, _, _ := receiptStateFieldRoles(t)
	for name, mutated := range map[string]string{
		"missing pair":               strings.Replace(source, `{"region_id", state.RegionID, receipt.RegionID},`, "", 1),
		"dead literal":               deadReceiptBindingLiteralFixture(source),
		"dead comparison branch":     deadReceiptBindingComparisonFixture(source),
		"post-loop stale assignment": postLoopReceiptBindingAssignmentFixture(source),
		"wrong comparison receiver": strings.NewReplacer(
			"field.stateValue", "bindings[0].stateValue",
			"field.receiptValue", "bindings[0].receiptValue",
		).Replace(source),
		"cross-wired pair": strings.Replace(
			source,
			`{"region_id", state.RegionID, receipt.RegionID},`,
			`{"region_id", state.RegionID, receipt.ExecutionProvider},`,
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			if mutated == source {
				t.Fatal("receipt binding mutation fixture did not change production source")
			}
			file := remoteCIParseGuardFixture(t, mutated)
			_, violations := receiptStateBindingFields(remoteCIFunctionByName(file, "ValidateGenerationOneProvisionStateBinding"), required)
			if len(violations) == 0 {
				t.Fatal("receipt binding guard accepted a missing or cross-wired production pair")
			}
		})
	}
}

func TestRemoteCIReceiptStateBindingRolesRejectRenameAndTypeDrift(t *testing.T) {
	state := reflect.StructOf([]reflect.StructField{{Name: "RegionID", Type: reflect.TypeFor[string](), Tag: `json:"region_id"`}})
	for name, receipt := range map[string]reflect.Type{
		"rename": reflect.StructOf([]reflect.StructField{{Name: "Region", Type: reflect.TypeFor[string](), Tag: `json:"region_id"`}}),
		"type":   reflect.StructOf([]reflect.StructField{{Name: "RegionID", Type: reflect.TypeFor[int](), Tag: `json:"region_id"`}}),
	} {
		t.Run(name, func(t *testing.T) {
			_, violations := deriveReceiptStateBindingRoles(receipt, state, []string{"region_id"})
			if len(violations) == 0 {
				t.Fatal("binding role derivation accepted Go rename or type drift")
			}
		})
	}
}

func TestRemoteCIReceiptStateBindingRolesRejectSharedFieldMissingFromOwner(t *testing.T) {
	receipt := reflect.StructOf([]reflect.StructField{{Name: "Shared", Type: reflect.TypeFor[string](), Tag: `json:"shared"`}})
	state := reflect.StructOf([]reflect.StructField{{Name: "Shared", Type: reflect.TypeFor[string](), Tag: `json:"shared"`}})
	if _, violations := deriveReceiptStateBindingRoles(receipt, state, nil); len(violations) == 0 {
		t.Fatal("binding role derivation accepted a shared JSON field missing from the canonical owner")
	}
}

func TestRemoteCISQLiteConsumerGuardRejectsDeadDecodedStateUse(t *testing.T) {
	file := remoteCIParseGuardFixture(t, `package fixture
func validateAcceptedBaselineStateProjection(stateJSON []byte) error {
	state, err := cicontract.DecodeAcceptedBaselineState(stateJSON)
	if err != nil { return err }
	if false { _ = state }
	return nil
}`)
	if functionHasLiveAcceptedStateDecode(remoteCIFunctionByName(file, "validateAcceptedBaselineStateProjection"), "stateJSON") {
		t.Fatal("SQLite consumer guard accepted decoded state used only in a dead branch")
	}
}

func deadReceiptBindingLiteralFixture(source string) string {
	mutated := strings.Replace(source, "\tbindings := []struct {", "\tif false {\n\t\t_ = []struct {", 1)
	return strings.Replace(mutated, "\t}\n\troles := GenerationOneProvisionStateBindingJSONFields()", "\t\t}\n\t}\n\troles := GenerationOneProvisionStateBindingJSONFields()", 1)
}

func deadReceiptBindingComparisonFixture(source string) string {
	mutated := strings.Replace(source, "\t\tif field.name != roles[index] {", "\t\tif false {\n\t\tif field.name != roles[index] {", 1)
	return strings.Replace(mutated, "\t\t}\n\t}\n\treturn nil\n}\n\n// RequiredProvisionChecks", "\t\t}\n\t\t}\n\t}\n\treturn nil\n}\n\n// RequiredProvisionChecks", 1)
}

func postLoopReceiptBindingAssignmentFixture(source string) string {
	start := strings.Index(source, "\tbindings := []struct {")
	if start < 0 {
		return source
	}
	end := strings.Index(source[start:], "\troles := GenerationOneProvisionStateBindingJSONFields()")
	if end < 0 {
		return source
	}
	stale := strings.Replace(source[start:start+end], "bindings :=", "bindings =", 1)
	mutated := strings.Replace(source, `{"region_id", state.RegionID, receipt.RegionID},`, "", 1)
	return strings.Replace(mutated, "\treturn nil\n}\n\n// RequiredProvisionChecks", stale+"\treturn nil\n}\n\n// RequiredProvisionChecks", 1)
}

func assertCanonicalBaselineMappers(t *testing.T, canonical map[string]struct{}) {
	t.Helper()
	remote := jsonFieldSet(reflect.TypeFor[remoteci.BaselineState]())
	assertJSONFieldSetEqual(t, "AcceptedBaselineState", canonical, "remoteci.BaselineState", remote)
	file := parseRemoteCIContractGuardFile(t, findRepoRoot(t)+"/internal/devtools/remoteci/baseline_state.go")
	assertExactStructMapper(t, remoteCIFunctionByName(file, "acceptedBaselineState"), goFieldSet(reflect.TypeFor[remoteci.BaselineState]()), "state")
	cache := jsonFieldSet(reflect.TypeFor[cicontract.AcceptedBaselineOCIProjectCache]())
	remoteCache := jsonFieldSet(reflect.TypeFor[remoteci.BaselineOCIProjectCache]())
	assertJSONFieldSetEqual(t, "AcceptedBaselineOCIProjectCache", cache, "remoteci.BaselineOCIProjectCache", remoteCache)
}

func assertCanonicalReceiptBindings(t *testing.T, canonical map[string]struct{}) {
	t.Helper()
	file := parseRemoteCIContractGuardFile(t, findRepoRoot(t)+"/internal/devtools/cicontract/generation_one.go")
	requiredBindings, receiptValidationOnly, stateValidationOnly := receiptStateFieldRoles(t)
	bindings, violations := receiptStateBindingFields(remoteCIFunctionByName(file, "ValidateGenerationOneProvisionStateBinding"), requiredBindings)
	for _, violation := range violations {
		t.Error(violation)
	}
	receiptFields := goFieldSet(reflect.TypeFor[cicontract.GenerationOneProvisionReceipt]())
	validationFields := reachableSelectorFields(file, generationOneReceiptValidate(file), "receipt")
	requiredSet := stringMapKeySet(requiredBindings)
	assertFieldSetEqual(t, "receipt binding fields", bindings, "required receipt-state bindings", requiredSet)
	assertFieldSubset(t, "receipt validation-only fields", receiptValidationOnly, validationFields)
	assertFieldUnion(t, "generation-one receipt", receiptFields, requiredSet, receiptValidationOnly)
	canonicalFields := goFieldSet(reflect.TypeFor[cicontract.AcceptedBaselineState]())
	canonicalValidation := reachableSelectorFields(file, acceptedBaselineStateValidate(file), "state")
	assertFieldSubset(t, "state validation-only fields", stateValidationOnly, canonicalValidation)
	assertFieldUnion(t, "accepted baseline state", canonicalFields, requiredSet, stateValidationOnly)
	if len(canonical) != len(canonicalFields) {
		t.Fatalf("canonical JSON field count = %d, Go field count = %d", len(canonical), len(canonicalFields))
	}
}

func receiptStateFieldRoles(t *testing.T) (map[string]string, map[string]struct{}, map[string]struct{}) {
	t.Helper()
	receiptType := reflect.TypeFor[cicontract.GenerationOneProvisionReceipt]()
	stateType := reflect.TypeFor[cicontract.AcceptedBaselineState]()
	required, violations := deriveReceiptStateBindingRoles(receiptType, stateType, cicontract.GenerationOneProvisionStateBindingJSONFields())
	for _, violation := range violations {
		t.Error(violation)
	}
	requiredSet := stringMapKeySet(required)
	return required, fieldSetDifference(goFieldSet(receiptType), requiredSet), fieldSetDifference(goFieldSet(stateType), requiredSet)
}

func deriveReceiptStateBindingRoles(receiptType, stateType reflect.Type, roles []string) (map[string]string, []string) {
	receiptByJSON := jsonStructFields(receiptType)
	stateByJSON := jsonStructFields(stateType)
	required, roleSet, violations := validateReceiptStateBindingRoles(receiptByJSON, stateByJSON, roles)
	return required, append(violations, missingSharedReceiptStateBindingRoles(receiptByJSON, stateByJSON, roleSet)...)
}

func validateReceiptStateBindingRoles(receiptByJSON, stateByJSON map[string]reflect.StructField, roles []string) (map[string]string, map[string]struct{}, []string) {
	required := make(map[string]string)
	violations := make([]string, 0)
	roleSet := make(map[string]struct{}, len(roles))
	for _, jsonName := range roles {
		if _, duplicate := roleSet[jsonName]; duplicate {
			violations = append(violations, fmt.Sprintf("binding role %q is duplicated", jsonName))
			continue
		}
		roleSet[jsonName] = struct{}{}
		stateField, stateOK := stateByJSON[jsonName]
		receiptField, receiptOK := receiptByJSON[jsonName]
		if !stateOK || !receiptOK || stateField.Type != receiptField.Type || stateField.Name != receiptField.Name {
			violations = append(violations, fmt.Sprintf("binding role %q does not map to equal receipt/state Go fields", jsonName))
			continue
		}
		required[stateField.Name] = jsonName
	}
	return required, roleSet, violations
}

func missingSharedReceiptStateBindingRoles(receiptByJSON, stateByJSON map[string]reflect.StructField, roleSet map[string]struct{}) []string {
	violations := make([]string, 0)
	for jsonName, stateField := range stateByJSON {
		receiptField, shared := receiptByJSON[jsonName]
		if !shared || stateField.Type != receiptField.Type || stateField.Name != receiptField.Name {
			continue
		}
		if jsonName == "schema_version" {
			continue // receipt/state schemas have independent version owners.
		}
		if _, covered := roleSet[jsonName]; !covered {
			violations = append(violations, fmt.Sprintf("shared receipt/state JSON field %q is missing from the binding role owner", jsonName))
		}
	}
	return violations
}

func jsonStructFields(typ reflect.Type) map[string]reflect.StructField {
	fields := make(map[string]reflect.StructField, typ.NumField())
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields[name] = field
	}
	return fields
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

func receiptStateBindingFields(function *ast.FuncDecl, required map[string]string) (map[string]struct{}, []string) {
	if function == nil {
		return nil, []string{"ValidateGenerationOneProvisionStateBinding is missing"}
	}
	literal := liveReceiptBindingLiteral(function)
	if literal == nil {
		return nil, []string{"ValidateGenerationOneProvisionStateBinding has no live top-level binding loop"}
	}
	bindings := make(map[string]struct{})
	violations := make([]string, 0)
	for _, element := range literal.Elts {
		state, elementViolations := receiptBindingFromElement(element, required)
		violations = append(violations, elementViolations...)
		if state != "" {
			bindings[state] = struct{}{}
		}
	}
	violations = append(violations, receiptBindingSetViolations(bindings, stringMapKeySet(required))...)
	return bindings, violations
}

func receiptBindingFromElement(element ast.Expr, required map[string]string) (string, []string) {
	binding, ok := element.(*ast.CompositeLit)
	if !ok || len(binding.Elts) != 3 {
		return "", nil
	}
	state := selectorField(binding.Elts[1], "state")
	receipt := selectorField(binding.Elts[2], "receipt")
	if state == "" && receipt == "" {
		return "", nil
	}
	if state == "" || receipt == "" || state != receipt {
		return "", []string{fmt.Sprintf("receipt binding contains a missing or cross-wired field: state=%q receipt=%q", state, receipt)}
	}
	name, _ := binding.Elts[0].(*ast.BasicLit)
	if name == nil || strings.Trim(name.Value, `"`) != required[state] {
		return state, []string{fmt.Sprintf("receipt binding field %q does not use canonical JSON role %q", state, required[state])}
	}
	return state, nil
}

func liveReceiptBindingLiteral(function *ast.FuncDecl) *ast.CompositeLit {
	var literal *ast.CompositeLit
	assignmentIndex, rangeIndex := -1, -1
	assignments, ranges := 0, 0
	for index, statement := range function.Body.List {
		if candidate := receiptBindingAssignment(statement); candidate != nil {
			literal = candidate
			assignmentIndex = index
			assignments++
		}
		if receiptBindingRangeConsumes(statement) {
			rangeIndex = index
			ranges++
		}
	}
	if assignments != 1 || ranges != 1 || rangeIndex != assignmentIndex+3 {
		return nil
	}
	return literal
}

func receiptBindingAssignment(statement ast.Stmt) *ast.CompositeLit {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return nil
	}
	name, nameOK := assignment.Lhs[0].(*ast.Ident)
	value, valueOK := assignment.Rhs[0].(*ast.CompositeLit)
	if !nameOK || !valueOK || name.Name != "bindings" {
		return nil
	}
	return value
}

func receiptBindingRangeConsumes(statement ast.Stmt) bool {
	loop, ok := statement.(*ast.RangeStmt)
	if !ok {
		return false
	}
	name, nameOK := loop.X.(*ast.Ident)
	return nameOK && name.Name == "bindings" && receiptBindingLoopConsumesValues(loop)
}

func receiptBindingLoopConsumesValues(loop *ast.RangeStmt) bool {
	index, indexOK := loop.Key.(*ast.Ident)
	field, fieldOK := loop.Value.(*ast.Ident)
	if !indexOK || !fieldOK || index.Name != "index" || field.Name != "field" || len(loop.Body.List) != 2 {
		return false
	}
	return receiptBindingRoleCheck(loop.Body.List[0], index.Name, field.Name) && receiptBindingValueCheck(loop.Body.List[1], field.Name)
}

func receiptBindingRoleCheck(statement ast.Stmt, indexName, fieldName string) bool {
	condition, ok := receiptBindingReturningCondition(statement)
	if !ok || condition.Op != token.NEQ {
		return false
	}
	left, leftOK := condition.X.(*ast.SelectorExpr)
	right, rightOK := condition.Y.(*ast.IndexExpr)
	return leftOK && selectorOwnerName(left, fieldName, "name") && rightOK && identifierName(right.X, "roles") && identifierName(right.Index, indexName)
}

func receiptBindingValueCheck(statement ast.Stmt, fieldName string) bool {
	condition, ok := receiptBindingReturningCondition(statement)
	if !ok || condition.Op != token.NEQ {
		return false
	}
	left, leftOK := condition.X.(*ast.SelectorExpr)
	right, rightOK := condition.Y.(*ast.SelectorExpr)
	return leftOK && rightOK && selectorOwnerName(left, fieldName, "stateValue") && selectorOwnerName(right, fieldName, "receiptValue")
}

func receiptBindingReturningCondition(statement ast.Stmt) (*ast.BinaryExpr, bool) {
	branch, ok := statement.(*ast.IfStmt)
	if !ok || branch.Init != nil || branch.Else != nil || len(branch.Body.List) != 1 {
		return nil, false
	}
	_, returns := branch.Body.List[0].(*ast.ReturnStmt)
	condition, binary := branch.Cond.(*ast.BinaryExpr)
	return condition, returns && binary
}

func selectorOwnerName(selector *ast.SelectorExpr, owner, name string) bool {
	return identifierName(selector.X, owner) && selector.Sel.Name == name
}

func identifierName(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func receiptBindingSetViolations(bindings, required map[string]struct{}) []string {
	violations := make([]string, 0)
	for field := range required {
		if _, ok := bindings[field]; !ok {
			violations = append(violations, fmt.Sprintf("receipt binding is missing required field %q", field))
		}
	}
	for field := range bindings {
		if _, ok := required[field]; !ok {
			violations = append(violations, fmt.Sprintf("receipt binding has validation-only or stale field %q", field))
		}
	}
	return violations
}

func selectorField(expression ast.Expr, owner string) string {
	if call, ok := expression.(*ast.CallExpr); ok && len(call.Args) == 1 {
		expression = call.Args[0]
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	identifier, ownerOK := selector.X.(*ast.Ident)
	if !ownerOK || identifier.Name != owner {
		return ""
	}
	return selector.Sel.Name
}

func reachableSelectorFields(file *ast.File, root *ast.FuncDecl, owner string) map[string]struct{} {
	fields := make(map[string]struct{})
	if root == nil {
		return fields
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	queue := []*ast.FuncDecl{root}
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		function := queue[0]
		queue = queue[1:]
		if _, ok := seen[function.Name.Name]; ok {
			continue
		}
		seen[function.Name.Name] = struct{}{}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			queue = collectReachableSelectorNode(node, owner, functions, fields, queue)
			return true
		})
	}
	return fields
}

func collectReachableSelectorNode(node ast.Node, owner string, functions map[string]*ast.FuncDecl, fields map[string]struct{}, queue []*ast.FuncDecl) []*ast.FuncDecl {
	if selector, ok := node.(*ast.SelectorExpr); ok {
		if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == owner {
			if called := functions[selector.Sel.Name]; called != nil {
				return append(queue, called)
			}
			fields[selector.Sel.Name] = struct{}{}
		}
	}
	if call, ok := node.(*ast.CallExpr); ok {
		if identifier, ok := call.Fun.(*ast.Ident); ok && functions[identifier.Name] != nil {
			return append(queue, functions[identifier.Name])
		}
	}
	return queue
}

func assertCanonicalSQLiteConsumers(t *testing.T) {
	t.Helper()
	root := findRepoRoot(t)
	for _, consumer := range []struct {
		file, function, input string
	}{
		{"internal/devtools/gate/ledger_store_sqlite_retention.go", "validateAcceptedBaselineStateProjection", "stateJSON"},
		{"internal/devtools/gate/remote_baseline_generation_one.go", "InitializeRemoteBaselineGenerationOne", "receipt"},
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
	case "InitializeRemoteBaselineGenerationOne":
		return topLevelStateCall(tail, "ValidateGenerationOneProvisionStateBinding") && topLevelStateReturnCall(tail, "insertGenerationOneState")
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
	return selectorOK && remoteCISelectorMatches(selector, "cicontract", "DecodeAcceptedBaselineState") && astNodeContainsIdentifier(call, input)
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

func topLevelStateCall(statements []ast.Stmt, name string) bool {
	for _, statement := range statements {
		branch, ok := statement.(*ast.IfStmt)
		if ok && callWithIdentifier(branch.Init, name, "state") {
			return true
		}
	}
	return false
}

func topLevelStateReturnCall(statements []ast.Stmt, name string) bool {
	for _, statement := range statements {
		result, ok := statement.(*ast.ReturnStmt)
		if !ok || len(result.Results) == 0 {
			continue
		}
		call, ok := result.Results[0].(*ast.CallExpr)
		if ok && calledFunctionName(call) == name && astNodeContainsIdentifier(call, "state") {
			return true
		}
	}
	return false
}

func callWithIdentifier(node ast.Node, name, identifier string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && calledFunctionName(call) == name && astNodeContainsIdentifier(call, identifier) {
			found = true
			return false
		}
		return !found
	})
	return found
}

func calledFunctionName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	default:
		return ""
	}
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

func generationOneReceiptValidate(file *ast.File) *ast.FuncDecl {
	return methodByReceiver(file, "GenerationOneProvisionReceipt", "Validate")
}

func acceptedBaselineStateValidate(file *ast.File) *ast.FuncDecl {
	return methodByReceiver(file, "AcceptedBaselineState", "Validate")
}

func methodByReceiver(file *ast.File, receiver, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name || function.Recv == nil || len(function.Recv.List) != 1 {
			continue
		}
		if identifier, ok := function.Recv.List[0].Type.(*ast.Ident); ok && identifier.Name == receiver {
			return function
		}
	}
	return nil
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

func assertFieldUnion(t *testing.T, name string, want, mapped, validated map[string]struct{}) {
	t.Helper()
	actual := make(map[string]struct{}, len(mapped)+len(validated))
	for field := range mapped {
		actual[field] = struct{}{}
	}
	for field := range validated {
		actual[field] = struct{}{}
	}
	assertFieldSetEqual(t, name+" covered fields", actual, name+" declared fields", want)
}

func fieldSetDifference(all, excluded map[string]struct{}) map[string]struct{} {
	difference := make(map[string]struct{}, len(all))
	for field := range all {
		if _, ok := excluded[field]; !ok {
			difference[field] = struct{}{}
		}
	}
	return difference
}

func assertFieldSubset(t *testing.T, name string, required, actual map[string]struct{}) {
	t.Helper()
	for field := range required {
		if _, ok := actual[field]; !ok {
			t.Errorf("%s is missing %q", name, field)
		}
	}
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

func stringMapKeySet(values map[string]string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for key := range values {
		set[key] = struct{}{}
	}
	return set
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

// TestRemoteCISQLAuthoritySchemaRejectsUnregisteredExtraTable proves the
// reverse schema guard rejects a second physical SQLite authority.
func TestRemoteCISQLAuthoritySchemaRejectsUnregisteredExtraTable(t *testing.T) {
	safe := `const schema = "CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT PRIMARY KEY)"`
	if extras := remoteCIUnregisteredSQLSchemaTables(safe); len(extras) != 0 {
		t.Fatalf("registered SQLite schema fixture was rejected: %v", extras)
	}
	legacy := `const schema = "CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT PRIMARY KEY) CREATE TABLE IF NOT EXISTS ci_unregistered_second_authority (id TEXT)"`
	extras := remoteCIUnregisteredSQLSchemaTables(legacy)
	if len(extras) != 1 || extras[0] != "ci_unregistered_second_authority" {
		t.Fatalf("unregistered SQLite schema fixture violations = %v, want the extra table", extras)
	}
}
