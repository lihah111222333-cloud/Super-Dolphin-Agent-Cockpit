package commandintent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestAssemblerProducerFieldsRemainReferenced 动态枚举 owner DTO，新增字段未进入组合层时立即失败。
func TestAssemblerProducerFieldsRemainReferenced(t *testing.T) {
	selectors := assemblerProductionSelectors(t)
	for _, typ := range []reflect.Type{
		reflect.TypeFor[common.CallerPrincipal](),
		reflect.TypeFor[accesswait.AccessAuthorization](),
		reflect.TypeFor[identity.CurrentBinding](),
		reflect.TypeFor[ContentAdmissionProofRequest](),
		reflect.TypeFor[ContentAdmissionProof](),
	} {
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if !selectors[field.Name] {
				t.Fatalf("production assembler does not reference %s.%s", typ.Name(), field.Name)
			}
		}
	}
}

// TestAssemblerWireDTOsHaveEnumerableJSONFields 锁定组合输入与 immutable 输出的可枚举字段。
func TestAssemblerWireDTOsHaveEnumerableJSONFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[ReserveAuthorizedSendDraftRequest](),
		reflect.TypeFor[ContentAdmissionProofRequest](),
		reflect.TypeFor[ContentAdmissionProof](),
		reflect.TypeFor[orchestrationcommand.CallerSnapshot](),
		reflect.TypeFor[orchestrationcommand.AuthorizationDecision](),
		reflect.TypeFor[orchestrationcommand.ActiveBindingSelector](),
		reflect.TypeFor[orchestrationcommand.SendMessageProfile](),
		reflect.TypeFor[orchestrationcommand.CompleteIntentRecord](),
	} {
		assertAssemblerJSONFields(t, typ)
	}
}

// TestContentProofDigestsCoverEveryBusinessField 动态枚举proof request/proof字段并锁定摘要覆盖。
func TestContentProofDigestsCoverEveryBusinessField(t *testing.T) {
	fixture := newAssemblerTestFixture(t)
	proofRequest, err := buildContentProofRequest(fixture.request, fixture.principal, fixture.authorization)
	if err != nil {
		t.Fatal(err)
	}
	assertContentProofDigestFieldCoverage(t, proofRequest, "RequestDigest", func(value ContentAdmissionProofRequest) (string, error) {
		return computeContentProofRequestDigest(value)
	})
	assertContentProofDigestFieldCoverage(t, fixture.content.proof, "SnapshotDigest", func(value ContentAdmissionProof) (string, error) {
		return computeContentProofSnapshotDigest(value)
	})
}

// TestContentProofRequestCannotBindCandidateCommandID 禁止proof lookup被每次重试变化的候选command ID污染。
func TestContentProofRequestCannotBindCandidateCommandID(t *testing.T) {
	typ := reflect.TypeFor[ContentAdmissionProofRequest]()
	for _, forbidden := range []string{"CandidateCommandID", "CommandID"} {
		if field, ok := typ.FieldByName(forbidden); ok {
			t.Fatalf("ContentAdmissionProofRequest exposes forbidden field %s", field.Name)
		}
	}
}

// TestAssemblerDoesNotMapCandidateContentRefDirectlyToIntent 防止caller候选ref绕过CONTENT proof进入intent。
func TestAssemblerDoesNotMapCandidateContentRefDirectlyToIntent(t *testing.T) {
	for _, file := range parseAssemblerProduction(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isAssemblerIntentLiteral(literal) {
				return true
			}
			for _, element := range literal.Elts {
				pair, pairOK := element.(*ast.KeyValueExpr)
				if pairOK && assemblerIdentName(pair.Key) == "RequestContentRef" && assemblerExprSelects(pair.Value, "RequestContentCandidateRef") {
					t.Fatal("orchestrationcommand.Intent.RequestContentRef directly consumes RequestContentCandidateRef")
				}
			}
			return true
		})
	}
}

// TestSelectorFromBindingCoversEveryCurrentBindingField 对 ID owner 字段逐一做 one-hot 映射验证。
func TestSelectorFromBindingCoversEveryCurrentBindingField(t *testing.T) {
	base := assemblerBinding("source", "a", time.Now().UTC().Truncate(time.Millisecond))
	baseSelector, err := selectorFromBinding(base)
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeFor[identity.CurrentBinding]()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			mutated := mutateAssemblerBindingField(t, base, field)
			got, mapErr := selectorFromBinding(mutated)
			if mapErr != nil {
				t.Fatalf("selectorFromBinding(%s) error = %v", field.Name, mapErr)
			}
			assertSelectorFieldChanged(t, baseSelector, got, field.Name)
		})
	}
}

// TestAssemblerProductionDependencySurfaceHasNoEffectPorts 禁止组合层接入 Fx、tool、provider 或 journal。
func TestAssemblerProductionDependencySurfaceHasNoEffectPorts(t *testing.T) {
	allowed := map[string]bool{
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait":           true,
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity":             true,
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand": true,
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc":                 true, // 仅用于同一 SQLite DBTX 的事务身份重读端口。
		"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common":               true,
	}
	for _, imported := range assemblerProductionImports(t) {
		if strings.Contains(imported, "super-dolphin-agent/") && !allowed[imported] {
			t.Fatalf("production assembler imports unapproved repository dependency %q", imported)
		}
		for _, forbidden := range []string{"go.uber.org/fx", "/tools", "/provider", "/coordinator", "/journal"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("production assembler imports effect dependency %q", imported)
			}
		}
	}
}

func assemblerProductionSelectors(t *testing.T) map[string]bool {
	t.Helper()
	selectors := make(map[string]bool)
	for _, file := range parseAssemblerProduction(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok {
				selectors[selector.Sel.Name] = true
			}
			return true
		})
	}
	return selectors
}

func assemblerProductionImports(t *testing.T) []string {
	t.Helper()
	var imports []string
	for _, file := range parseAssemblerProduction(t) {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			imports = append(imports, path)
		}
	}
	return imports
}

func parseAssemblerProduction(t *testing.T) []*ast.File {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate commandintent package")
	}
	dir := filepath.Dir(current)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("commandintent production files are missing")
	}
	return files
}

func assertAssemblerJSONFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	seen := make(map[string]bool, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || seen[name] {
			t.Fatalf("%s.%s has invalid or duplicate JSON field %q", typ.Name(), field.Name, name)
		}
		seen[name] = true
	}
}

func assertContentProofDigestFieldCoverage[T any](t *testing.T, base T, selfDigest string, digest func(T) (string, error)) {
	t.Helper()
	baseDigest, err := digest(base)
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(base)
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.Name == selfDigest {
			continue
		}
		t.Run(typ.Name()+"."+field.Name, func(t *testing.T) {
			mutated := mutateContentProofField(t, base, field)
			got, digestErr := digest(mutated)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			if got == baseDigest {
				t.Fatalf("%s.%s does not affect digest", typ.Name(), field.Name)
			}
		})
	}
}

func mutateContentProofField[T any](t *testing.T, base T, field reflect.StructField) T {
	t.Helper()
	mutated := base
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	switch value.Kind() {
	case reflect.String:
		value.SetString(value.String() + "-mutated")
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	case reflect.Struct:
		mutateContentProofStruct(t, value, field.Name)
	default:
		t.Fatalf("%s has unsupported mutation kind %s", field.Name, value.Kind())
	}
	return mutated
}

func mutateContentProofStruct(t *testing.T, value reflect.Value, fieldName string) {
	t.Helper()
	switch value.Type() {
	case reflect.TypeFor[time.Time]():
		value.Set(reflect.ValueOf(value.Interface().(time.Time).Add(time.Minute)))
	case reflect.TypeFor[accesswait.FenceSnapshot]():
		agentID := value.FieldByName("AgentID")
		agentID.SetString(agentID.String() + "-mutated")
	default:
		t.Fatalf("%s has unsupported struct type %s", fieldName, value.Type())
	}
}

func isAssemblerIntentLiteral(literal *ast.CompositeLit) bool {
	selector, ok := literal.Type.(*ast.SelectorExpr)
	return ok && assemblerIdentName(selector.X) == "orchestrationcommand" && selector.Sel.Name == "Intent"
}

func assemblerIdentName(expression ast.Expr) string {
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func assemblerExprSelects(expression ast.Expr, fieldName string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == fieldName {
			found = true
		}
		return !found
	})
	return found
}

func mutateAssemblerBindingField(t *testing.T, base identity.CurrentBinding, field reflect.StructField) identity.CurrentBinding {
	t.Helper()
	mutated := base
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	switch value.Kind() {
	case reflect.String:
		if field.Name == "UpdatedAt" {
			value.SetString(time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano))
		} else if field.Name == "BindingDigest" {
			value.SetString(strings.Repeat("f", 64))
		} else {
			value.SetString(value.String() + "-mutated")
		}
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		t.Fatalf("CurrentBinding.%s has unsupported mutation kind %s", field.Name, value.Kind())
	}
	return mutated
}

func assertSelectorFieldChanged(t *testing.T, before, after orchestrationcommand.ActiveBindingSelector, sourceField string) {
	t.Helper()
	destination := map[string]string{"SourceFenceRef": "FenceRef", "SchemaVersion": "BindingSchemaVersion"}[sourceField]
	if destination == "" {
		destination = sourceField
	}
	beforeValue := reflect.ValueOf(before).FieldByName(destination)
	afterValue := reflect.ValueOf(after).FieldByName(destination)
	if !beforeValue.IsValid() || reflect.DeepEqual(beforeValue.Interface(), afterValue.Interface()) {
		t.Fatalf("CurrentBinding.%s did not change ActiveBindingSelector.%s", sourceField, destination)
	}
}
