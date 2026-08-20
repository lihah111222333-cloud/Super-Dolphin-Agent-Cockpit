package archtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"golang.org/x/tools/go/packages"
)

const heterociSnapshotV3PackagePath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"

type heterociSnapshotV3FieldSpec struct {
	name string
	typ  reflect.Type
	tag  string
}

// TestHeterociSnapshotSetV3WireContract freezes the six-field v3 wire shape.
func TestHeterociSnapshotSetV3WireContract(t *testing.T) {
	if got, want := heteroci.SnapshotSchemaVersion, "heteroci-snapshot/v3"; got != want {
		t.Fatalf("SnapshotSchemaVersion = %q, want %q", got, want)
	}
	want := []heterociSnapshotV3FieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), tag: `json:"schema_version"`},
		{name: "Authority", typ: reflect.TypeFor[string](), tag: `json:"authority"`},
		{name: "OwnerEpoch", typ: reflect.TypeFor[heteroci.RoutingOwnerEpoch](), tag: `json:"owner_epoch"`},
		{name: "CapacityBasis", typ: reflect.TypeFor[heteroci.SnapshotCapacityBasis](), tag: `json:"capacity_basis"`},
		{name: "CapturedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: `json:"captured_at_unix_milli"`},
		{name: "Nodes", typ: reflect.TypeFor[[]heteroci.NodeSnapshot](), tag: `json:"nodes"`},
	}
	got := reflect.TypeFor[heteroci.SnapshotSet]()
	if got.Kind() != reflect.Struct || got.NumField() != len(want) {
		t.Fatalf("SnapshotSet layout = %s/%d fields, want struct/%d", got.Kind(), got.NumField(), len(want))
	}
	for index, expected := range want {
		field := got.Field(index)
		if field.Name != expected.name || field.Type != expected.typ || string(field.Tag) != expected.tag {
			t.Fatalf("SnapshotSet field %d = %s %s %q, want %s %s %q", index, field.Name, field.Type, field.Tag, expected.name, expected.typ, expected.tag)
		}
	}
}

// TestHeterociSnapshotSetV3RequiredFieldsGuard derives producers from tags and
// proves both snapshot_codec.go root required sets are exact consumers.
func TestHeterociSnapshotSetV3RequiredFieldsGuard(t *testing.T) {
	source := loadHeterociSnapshotV3Production(t)
	want, err := heterociSnapshotV3ReflectJSONTags(reflect.TypeFor[heteroci.SnapshotSet]())
	if err != nil {
		t.Fatal(err)
	}
	if violations := source.rootRequiredViolations(want); len(violations) != 0 {
		t.Fatalf("SnapshotSet root required-field violations: %s", strings.Join(violations, "; "))
	}
}

// TestHeterociSnapshotSetV3AcquireProductionGuard checks the producer's typed
// success mapping and fail-closed zero-value returns from AST plus go/types.
func TestHeterociSnapshotSetV3AcquireProductionGuard(t *testing.T) {
	source := loadHeterociSnapshotV3Production(t)
	if violations := source.acquireViolations(); len(violations) != 0 {
		t.Fatalf("AcquireSnapshot producer violations: %s", strings.Join(violations, "; "))
	}
}

// TestHeterociSnapshotSetV3BehaviorMatrix checks owner passthrough and whole
// zero rejection for invalid epoch, old schema, and missing owner fields.
func TestHeterociSnapshotSetV3BehaviorMatrix(t *testing.T) {
	now := time.UnixMilli(1_725_000_000_000)
	ttl := 2 * time.Minute
	manifest := newHeterociProbeManifest(t, []string{"node-owner"})
	valid := assertHeterociSnapshotV3ValidOwner(t, manifest, now, ttl)
	assertHeterociSnapshotV3InvalidOwners(t, manifest, now, ttl)
	assertHeterociSnapshotV3InvalidWire(t, valid, manifest, now, ttl)
}

func assertHeterociSnapshotV3ValidOwner(t *testing.T, manifest heteroci.Manifest, now time.Time, ttl time.Duration) heteroci.SnapshotSet {
	t.Helper()
	const owner = heteroci.RoutingOwnerEpoch(37)
	transport := newHeterociProbeTransport(manifest, owner, now)
	got, err := heteroci.AcquireSnapshot(context.Background(), manifest, owner, transport, now, ttl)
	if err != nil || got.OwnerEpoch != owner {
		t.Fatalf("valid owner AcquireSnapshot = %#v, %v; want owner_epoch=%d", got, err, owner)
	}
	assertHeterociSnapshotV3ProbeOwner(t, transport, owner)
	return got
}

func assertHeterociSnapshotV3InvalidOwners(t *testing.T, manifest heteroci.Manifest, now time.Time, ttl time.Duration) {
	t.Helper()
	for _, invalid := range []heteroci.RoutingOwnerEpoch{0, heteroci.RoutingOwnerEpoch(math.MaxInt64)} {
		transport := newHeterociProbeTransport(manifest, 37, now)
		got, err := heteroci.AcquireSnapshot(context.Background(), manifest, invalid, transport, now, ttl)
		if err == nil || !reflect.DeepEqual(got, heteroci.SnapshotSet{}) {
			t.Fatalf("invalid owner %d AcquireSnapshot = %#v, %v; want whole zero and error", invalid, got, err)
		}
	}
}

func assertHeterociSnapshotV3InvalidWire(t *testing.T, valid heteroci.SnapshotSet, manifest heteroci.Manifest, now time.Time, ttl time.Duration) {
	t.Helper()
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid snapshot: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{name: "v2 schema", mutate: func(object map[string]json.RawMessage) {
			object["schema_version"] = json.RawMessage(`"heteroci-snapshot/v2"`)
		}},
		{name: "missing owner", mutate: func(object map[string]json.RawMessage) { delete(object, "owner_epoch") }},
	}
	for _, testCase := range cases {
		assertHeterociSnapshotV3InvalidWireCase(t, testCase.name, raw, testCase.mutate, manifest, now, ttl)
	}
}

func assertHeterociSnapshotV3InvalidWireCase(t *testing.T, name string, raw []byte, mutate func(map[string]json.RawMessage), manifest heteroci.Manifest, now time.Time, ttl time.Duration) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("unmarshal snapshot fixture: %v", err)
	}
	mutate(object)
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal mutated snapshot: %v", err)
	}
	decoded, err := heteroci.DecodeSnapshotSet(bytes.NewReader(payload), manifest, now, ttl)
	if err == nil || !reflect.DeepEqual(decoded, heteroci.SnapshotSet{}) {
		t.Fatalf("DecodeSnapshotSet(%s) = %#v, %v; want whole zero and error", name, decoded, err)
	}
}

func assertHeterociSnapshotV3ProbeOwner(t *testing.T, transport *heterociProbeTransportStub, want heteroci.RoutingOwnerEpoch) {
	t.Helper()
	if len(transport.calls) == 0 {
		t.Fatal("valid owner produced no probe calls")
	}
	for index, call := range transport.calls {
		var request heteroci.ProbeRequest
		if err := json.Unmarshal(call.request, &request); err != nil {
			t.Fatalf("probe call %d request: %v", index, err)
		}
		if request.OwnerEpoch != want {
			t.Fatalf("probe call %d owner_epoch = %d, want %d", index, request.OwnerEpoch, want)
		}
	}
}

func heterociSnapshotV3ReflectJSONTags(typ reflect.Type) ([]string, error) {
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("SnapshotSet producer type = %s, want struct", typ)
	}
	fields := make([]string, 0, typ.NumField())
	seen := make(map[string]bool, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		name, ok := typ.Field(index).Tag.Lookup("json")
		if !ok {
			return nil, fmt.Errorf("SnapshotSet field %d has no json tag", index)
		}
		name = strings.Split(name, ",")[0]
		if name == "" || name == "-" || seen[name] {
			return nil, fmt.Errorf("SnapshotSet field %d has invalid or duplicate json tag %q", index, name)
		}
		seen[name] = true
		fields = append(fields, name)
	}
	return fields, nil
}

type heterociSnapshotV3Source struct {
	pkg *packages.Package
}

func loadHeterociSnapshotV3Production(t *testing.T) *heterociSnapshotV3Source {
	t.Helper()
	root := findRepoRoot(t)
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./internal/devtools/heteroci"},
		Tests:    false,
		LoadMode: packages.LoadSyntax,
		Include:  func(pkg *packages.Package) bool { return pkg.PkgPath == heterociSnapshotV3PackagePath },
	})
	if err != nil {
		t.Fatalf("load SnapshotSet production package: %v", err)
	}
	return newHeterociSnapshotV3Source(t, loaded)
}

func newHeterociSnapshotV3Source(t *testing.T, loaded []*packages.Package) *heterociSnapshotV3Source {
	t.Helper()
	if len(loaded) != 1 || loaded[0] == nil || loaded[0].Types == nil || loaded[0].TypesInfo == nil {
		t.Fatalf("SnapshotSet source package count/types = %d, want one typed package", len(loaded))
	}
	return &heterociSnapshotV3Source{pkg: loaded[0]}
}

func (source *heterociSnapshotV3Source) function(name string) (*ast.FuncDecl, error) {
	var found []*ast.FuncDecl
	for _, file := range source.pkg.Syntax {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.Name == name {
				found = append(found, function)
			}
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("%s top-level function count = %d, want one", name, len(found))
	}
	return found[0], nil
}

func (source *heterociSnapshotV3Source) snapshotType() (*types.TypeName, error) {
	object, ok := source.pkg.Types.Scope().Lookup("SnapshotSet").(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("SnapshotSet type is missing")
	}
	if _, ok := object.Type().Underlying().(*types.Struct); !ok {
		return nil, fmt.Errorf("SnapshotSet underlying type = %s, want struct", object.Type().Underlying())
	}
	return object, nil
}

func (source *heterociSnapshotV3Source) rootRequiredViolations(want []string) []string {
	found, err := source.rootWalkRequiredSets()
	if err != nil {
		return []string{err.Error()}
	}
	validate, err := source.function("validateSnapshotRequiredFields")
	if err != nil {
		return append(found, err.Error())
	}
	validationSets, rootCalls := heterociSnapshotV3ValidationRequiredSets(validate)
	found = append(found, validationSets...)
	if len(found) != 2 || rootCalls != 1 {
		found = append(found, fmt.Sprintf("root required-set count = %d, root call count = %d, want 2 and 1", len(found), rootCalls))
	}
	return heterociSnapshotV3NormalizeRequiredSets(found, want, rootCalls)
}

func heterociSnapshotV3NormalizeRequiredSets(found, want []string, rootCalls int) []string {
	if heterociSnapshotV3RequiredSetsMatch(found, want, rootCalls) {
		return nil
	}
	for index, got := range found {
		if index >= 2 {
			break
		}
		if !heterociSnapshotV3StringSetEqual(strings.Split(got, "\x00"), want) {
			found[index] = fmt.Sprintf("root required set %d = %q, want %q", index, got, heterociSnapshotV3Sequence(want))
		}
	}
	return found
}

func heterociSnapshotV3RequiredSetsMatch(found, want []string, rootCalls int) bool {
	return len(found) == 2 && rootCalls == 1 &&
		heterociSnapshotV3StringSetEqual(strings.Split(found[0], "\x00"), want) &&
		heterociSnapshotV3StringSetEqual(strings.Split(found[1], "\x00"), want)
}

func (source *heterociSnapshotV3Source) rootWalkRequiredSets() ([]string, error) {
	walk, err := source.function("walkSnapshotJSONObject")
	if err != nil {
		return nil, err
	}
	found := make([]string, 0, 1)
	ast.Inspect(walk.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !heterociSnapshotV3StringSliceType(literal.Type) {
			return true
		}
		values, parseErr := heterociSnapshotV3StringSlice(literal)
		if parseErr != nil {
			found = append(found, parseErr.Error())
		} else {
			found = append(found, heterociSnapshotV3Sequence(values))
		}
		return true
	})
	return found, nil
}

func heterociSnapshotV3ValidationRequiredSets(validate *ast.FuncDecl) ([]string, int) {
	found, rootCalls := make([]string, 0, 1), 0
	ast.Inspect(validate.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !heterociSnapshotV3CallNamed(call, "requireSnapshotFields") || len(call.Args) != 3 {
			return true
		}
		path, ok := heterociSnapshotV3StringLiteral(call.Args[1])
		if !ok || path != "root" {
			return true
		}
		rootCalls++
		literal, ok := call.Args[2].(*ast.CompositeLit)
		if !ok || !heterociSnapshotV3StringSliceType(literal.Type) {
			found = append(found, "root requireSnapshotFields call has no []string literal")
			return true
		}
		values, parseErr := heterociSnapshotV3StringSlice(literal)
		if parseErr != nil {
			found = append(found, parseErr.Error())
		} else {
			found = append(found, heterociSnapshotV3Sequence(values))
		}
		return true
	})
	return found, rootCalls
}

func heterociSnapshotV3Sequence(values []string) string { return strings.Join(values, "\x00") }

func heterociSnapshotV3StringSetEqual(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gotCopy, wantCopy := append([]string(nil), got...), append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	return reflect.DeepEqual(gotCopy, wantCopy)
}

func heterociSnapshotV3StringSliceType(expression ast.Expr) bool {
	array, ok := expression.(*ast.ArrayType)
	if !ok || array.Len != nil {
		return false
	}
	element, ok := array.Elt.(*ast.Ident)
	return ok && element.Name == "string"
}

func heterociSnapshotV3StringSlice(literal *ast.CompositeLit) ([]string, error) {
	if !heterociSnapshotV3StringSliceType(literal.Type) {
		return nil, fmt.Errorf("required set is not []string")
	}
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		basic, ok := element.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			return nil, fmt.Errorf("required set contains a non-string literal")
		}
		value, err := strconv.Unquote(basic.Value)
		if err != nil {
			return nil, fmt.Errorf("required set string literal: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func heterociSnapshotV3CallNamed(call *ast.CallExpr, name string) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	return ok && identifier.Name == name
}

func heterociSnapshotV3StringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func (source *heterociSnapshotV3Source) acquireViolations() []string {
	function, err := source.function("AcquireSnapshot")
	if err != nil {
		return []string{err.Error()}
	}
	snapshot, err := source.snapshotType()
	if err != nil {
		return []string{err.Error()}
	}
	owner := heterociSnapshotV3ParamObject(source.pkg.TypesInfo, function, "ownerEpoch")
	if owner == nil {
		return []string{"AcquireSnapshot ownerEpoch parameter is missing"}
	}
	violations := heterociSnapshotV3AcquireParameterViolations(owner)
	fieldNames, fieldErr := heterociSnapshotV3TypeFieldNames(snapshot)
	if fieldErr != nil {
		violations = append(violations, fieldErr.Error())
		return violations
	}
	success, zero, successVariable := source.snapshotLiterals(function, snapshot)
	violations = append(violations, heterociSnapshotV3SuccessViolations(success, successVariable, fieldNames, owner, source.pkg.TypesInfo)...)
	violations = append(violations, source.acquireReturnViolations(function, success, zero, successVariable)...)
	return violations
}

func heterociSnapshotV3ParamObject(info *types.Info, function *ast.FuncDecl, name string) *types.Var {
	for _, field := range function.Type.Params.List {
		for _, identifier := range field.Names {
			if identifier.Name == name {
				object, _ := info.ObjectOf(identifier).(*types.Var)
				return object
			}
		}
	}
	return nil
}

func heterociSnapshotV3AcquireParameterViolations(owner *types.Var) []string {
	named, ok := owner.Type().(*types.Named)
	if !ok || named.Obj().Name() != "RoutingOwnerEpoch" {
		return []string{fmt.Sprintf("ownerEpoch type = %s, want RoutingOwnerEpoch", owner.Type())}
	}
	return nil
}

func (source *heterociSnapshotV3Source) snapshotLiterals(function *ast.FuncDecl, snapshot *types.TypeName) ([]*ast.CompositeLit, []*ast.CompositeLit, *types.Var) {
	var success, zero []*ast.CompositeLit
	var successVariable *types.Var
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		literal, ok := node.(*ast.CompositeLit)
		if !ok || source.snapshotLiteralType(literal) != snapshot {
			return true
		}
		if len(literal.Elts) == 0 {
			zero = append(zero, literal)
			return true
		}
		success = append(success, literal)
		return true
	})
	if len(success) == 1 {
		successVariable = source.successVariable(function.Body, success[0])
	}
	return success, zero, successVariable
}

func (source *heterociSnapshotV3Source) successVariable(body *ast.BlockStmt, literal *ast.CompositeLit) *types.Var {
	var result *types.Var
	ast.Inspect(body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || assignment.Rhs[0] != literal {
			return true
		}
		identifier, ok := assignment.Lhs[0].(*ast.Ident)
		if ok {
			result, _ = source.pkg.TypesInfo.ObjectOf(identifier).(*types.Var)
		}
		return true
	})
	return result
}

func (source *heterociSnapshotV3Source) snapshotLiteralType(literal *ast.CompositeLit) *types.TypeName {
	named, ok := source.pkg.TypesInfo.TypeOf(literal.Type).(*types.Named)
	if !ok {
		return nil
	}
	return named.Obj()
}

func heterociSnapshotV3SuccessViolations(success []*ast.CompositeLit, _ *types.Var, want []string, owner *types.Var, info *types.Info) []string {
	if len(success) != 1 {
		return []string{fmt.Sprintf("AcquireSnapshot success SnapshotSet literal count = %d, want 1", len(success))}
	}
	literal := success[0]
	keys := make([]string, 0, len(literal.Elts))
	var ownerValues []ast.Expr
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return []string{"AcquireSnapshot success SnapshotSet literal must use keyed fields"}
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			return []string{"AcquireSnapshot success SnapshotSet key is not an identifier"}
		}
		keys = append(keys, key.Name)
		if key.Name == "OwnerEpoch" {
			ownerValues = append(ownerValues, keyValue.Value)
		}
	}
	violations := make([]string, 0, 2)
	if !heterociSnapshotV3FieldNamesMatch(keys, want) {
		violations = append(violations, fmt.Sprintf("AcquireSnapshot success fields = %v, want %v", keys, want))
	}
	if len(ownerValues) != 1 {
		violations = append(violations, fmt.Sprintf("AcquireSnapshot success OwnerEpoch mapping count = %d, want 1", len(ownerValues)))
	} else if identifier, ok := ownerValues[0].(*ast.Ident); !ok || info.ObjectOf(identifier) != owner {
		violations = append(violations, "AcquireSnapshot success OwnerEpoch must be the ownerEpoch parameter")
	}
	return violations
}

func heterociSnapshotV3FieldNamesMatch(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func heterociSnapshotV3TypeFieldNames(snapshot *types.TypeName) ([]string, error) {
	structure, ok := snapshot.Type().Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("SnapshotSet underlying type = %s, want struct", snapshot.Type().Underlying())
	}
	fields := make([]string, structure.NumFields())
	for index := range fields {
		fields[index] = structure.Field(index).Name()
	}
	return fields, nil
}

func (source *heterociSnapshotV3Source) acquireReturnViolations(function *ast.FuncDecl, success, zero []*ast.CompositeLit, successVariable *types.Var) []string {
	zeroSet := make(map[*ast.CompositeLit]bool, len(zero))
	for _, literal := range zero {
		zeroSet[literal] = true
	}
	zeroReturns, successReturns, violations := source.scanAcquireReturns(function, success, zeroSet, successVariable)
	if successReturns != 1 {
		violations = append(violations, fmt.Sprintf("AcquireSnapshot nil-error return count = %d, want 1", successReturns))
	}
	if zeroReturns != len(zero) || zeroReturns == 0 {
		violations = append(violations, fmt.Sprintf("AcquireSnapshot zero error returns = %d/%d, want every zero literal", zeroReturns, len(zero)))
	}
	if len(success) == 0 {
		violations = append(violations, "AcquireSnapshot has no non-zero success literal")
	}
	return violations
}

func (source *heterociSnapshotV3Source) scanAcquireReturns(function *ast.FuncDecl, success []*ast.CompositeLit, zeroSet map[*ast.CompositeLit]bool, successVariable *types.Var) (int, int, []string) {
	zeroReturns, successReturns := 0, 0
	violations := make([]string, 0, 2)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		returnStatement, ok := node.(*ast.ReturnStmt)
		if !ok || len(returnStatement.Results) != 2 {
			return true
		}
		if heterociSnapshotV3IsNil(returnStatement.Results[1]) {
			if heterociSnapshotV3SuccessReturn(returnStatement.Results[0], success, successVariable, source.pkg.TypesInfo) {
				successReturns++
			} else {
				violations = append(violations, "AcquireSnapshot nil-error return does not return the unique success literal")
			}
			return true
		}
		if !heterociSnapshotV3IsZeroSnapshot(returnStatement.Results[0], zeroSet) {
			violations = append(violations, "AcquireSnapshot error return must return SnapshotSet{}")
			return true
		}
		zeroReturns++
		return true
	})
	return zeroReturns, successReturns, violations
}

func heterociSnapshotV3SuccessReturn(expression ast.Expr, success []*ast.CompositeLit, variable *types.Var, info *types.Info) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		return variable != nil && info.ObjectOf(identifier) == variable
	}
	literal, ok := expression.(*ast.CompositeLit)
	return ok && len(success) == 1 && literal == success[0]
}

func heterociSnapshotV3IsZeroSnapshot(expression ast.Expr, zero map[*ast.CompositeLit]bool) bool {
	literal, ok := expression.(*ast.CompositeLit)
	return ok && zero[literal]
}

func heterociSnapshotV3IsNil(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "nil"
}

func heterociSnapshotV3FieldsFromTypes(source *heterociSnapshotV3Source) ([]string, error) {
	object, err := source.snapshotType()
	if err != nil {
		return nil, err
	}
	structure := object.Type().Underlying().(*types.Struct)
	fields := make([]string, 0, structure.NumFields())
	for index := 0; index < structure.NumFields(); index++ {
		name, ok := reflect.StructTag(structure.Tag(index)).Lookup("json")
		if !ok {
			return nil, fmt.Errorf("synthetic SnapshotSet field %d has no json tag", index)
		}
		fields = append(fields, strings.Split(name, ",")[0])
	}
	return fields, nil
}

// TestHeterociSnapshotSetV3Counterexamples keeps the guard RED for common
// producer/consumer regressions instead of testing only today's source.
func TestHeterociSnapshotSetV3Counterexamples(t *testing.T) {
	for _, testCase := range heterociSnapshotV3Counterexamples() {
		t.Run(testCase.name, func(t *testing.T) {
			source := loadHeterociSnapshotV3Synthetic(t, testCase.source)
			fields, err := heterociSnapshotV3FieldsFromTypes(source)
			if err != nil {
				t.Fatal(err)
			}
			root := strings.Join(source.rootRequiredViolations(fields), "; ")
			acquire := strings.Join(source.acquireViolations(), "; ")
			if testCase.rootWant != "" && !strings.Contains(root, testCase.rootWant) {
				t.Fatalf("root violations = %q, want %q", root, testCase.rootWant)
			}
			if testCase.acquireWant != "" && !strings.Contains(acquire, testCase.acquireWant) {
				t.Fatalf("AcquireSnapshot violations = %q, want %q", acquire, testCase.acquireWant)
			}
		})
	}
}

type heterociSnapshotV3Counterexample struct {
	name        string
	source      string
	rootWant    string
	acquireWant string
}

func heterociSnapshotV3Counterexamples() []heterociSnapshotV3Counterexample {
	return []heterociSnapshotV3Counterexample{
		{name: "deleted OwnerEpoch mapping", source: heterociSnapshotV3Fixture("", ""), acquireWant: "success fields"},
		{name: "wrong OwnerEpoch parameter", source: heterociSnapshotV3Fixture("OwnerEpoch: ownerEpoch,", "func AcquireSnapshot(ownerEpoch RoutingOwnerEpoch, other RoutingOwnerEpoch) (SnapshotSet, error) {\n set := SnapshotSet{SchemaVersion: \"v\", Authority: \"a\", OwnerEpoch: other, CapacityBasis: \"b\", CapturedAtUnixMilli: 1, Nodes: nil}\n return set, nil\n}"), acquireWant: "ownerEpoch parameter"},
		{name: "stale compatibility required field", source: heterociSnapshotV3Fixture("OwnerEpoch: ownerEpoch,", "", "legacy"), rootWant: "root required set"},
		{name: "second compatibility root set", source: heterociSnapshotV3Fixture("OwnerEpoch: ownerEpoch,", "", "", true), rootWant: "root required-set count"},
	}
}

func heterociSnapshotV3Fixture(ownerField, acquireOverride string, extras ...any) string {
	legacy := ""
	third := false
	for _, extra := range extras {
		switch value := extra.(type) {
		case string:
			legacy = value
		case bool:
			third = value
		}
	}
	root := `"schema_version", "authority", "owner_epoch", "capacity_basis", "captured_at_unix_milli", "nodes"`
	if legacy != "" {
		root += `, "` + legacy + `"`
	}
	second := root
	thirdCall := ""
	if third {
		thirdCall = `
 requireSnapshotFields(nil, "root", []string{` + root + `})`
	}
	acquire := `func AcquireSnapshot(ownerEpoch RoutingOwnerEpoch) (SnapshotSet, error) {
 set := SnapshotSet{SchemaVersion: "v", Authority: "a", ` + ownerField + ` CapacityBasis: "b", CapturedAtUnixMilli: 1, Nodes: nil}
 return set, nil
}`
	if acquireOverride != "" {
		acquire = acquireOverride
	}
	return `package heteroci
type RoutingOwnerEpoch int64
type SnapshotCapacityBasis string
type NodeSnapshot struct{}
type SnapshotSet struct {
 SchemaVersion string ` + "`json:\"schema_version\"`" + `
 Authority string ` + "`json:\"authority\"`" + `
 OwnerEpoch RoutingOwnerEpoch ` + "`json:\"owner_epoch\"`" + `
 CapacityBasis SnapshotCapacityBasis ` + "`json:\"capacity_basis\"`" + `
 CapturedAtUnixMilli int64 ` + "`json:\"captured_at_unix_milli\"`" + `
 Nodes []NodeSnapshot ` + "`json:\"nodes\"`" + `
}
func requireSnapshotFields(any, string, []string) {}
func walkSnapshotJSONObject() {
 for _, field := range []string{` + root + `} { _ = field }
}
func validateSnapshotRequiredFields() {
 requireSnapshotFields(nil, "root", []string{` + second + `})` + thirdCall + `
}
` + acquire + `
func invalid() {}`
}

func loadHeterociSnapshotV3Synthetic(t *testing.T, source string) *heterociSnapshotV3Source {
	t.Helper()
	root := t.TempDir()
	writeHeterociSnapshotV3Fixture(t, filepath.Join(root, "go.mod"), "module synthetic\n\ngo 1.26\n")
	writeHeterociSnapshotV3Fixture(t, filepath.Join(root, "heteroci", "fixture.go"), source)
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./heteroci"},
		Tests:    false,
		LoadMode: packages.LoadSyntax,
		Include:  func(pkg *packages.Package) bool { return pkg.PkgPath == "synthetic/heteroci" },
	})
	if err != nil {
		t.Fatalf("load synthetic SnapshotSet source: %v", err)
	}
	return newHeterociSnapshotV3Source(t, loaded)
}

func writeHeterociSnapshotV3Fixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create SnapshotSet fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write SnapshotSet fixture: %v", err)
	}
}
