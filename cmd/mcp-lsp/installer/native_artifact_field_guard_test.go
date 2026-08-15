package installer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"testing"
)

// TestNativeArtifactSpecFieldsAreConsumedByInstaller guards the manifest
// contract so a newly added field cannot silently become dead configuration.
func TestNativeArtifactSpecFieldsAreConsumedByInstaller(t *testing.T) {
	producer := reflect.TypeFor[NativeArtifactSpec]()
	fields := make([]string, 0, producer.NumField())
	producerFields := make(map[string]struct{}, producer.NumField())
	for index := range producer.NumField() {
		name := producer.Field(index).Name
		fields = append(fields, name)
		producerFields[name] = struct{}{}
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), "native_artifact.go", nil, 0)
	if err != nil {
		t.Fatalf("parse native artifact installer: %v", err)
	}
	consumed := make(map[string]struct{}, len(fields))
	ast.Inspect(parsed, nativeArtifactFieldConsumer(producerFields, consumed))

	missing, stale := nativeArtifactSpecFieldDiff(fields, consumed)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("NativeArtifactSpec field coverage missing=%v stale=%v", missing, stale)
	}

	// Fail-first proof: removing a real consumer must identify that exact field.
	delete(consumed, "AllowSymlinks")
	missing, stale = nativeArtifactSpecFieldDiff(fields, consumed)
	if !slices.Equal(missing, []string{"AllowSymlinks"}) || len(stale) != 0 {
		t.Fatalf("field guard fail-first missing=%v stale=%v, want [AllowSymlinks]", missing, stale)
	}
}

func nativeArtifactFieldConsumer(producerFields, consumed map[string]struct{}) func(ast.Node) bool {
	return func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		_, isProducerField := producerFields[selector.Sel.Name]
		if ok && ident.Name == "spec" && isProducerField {
			consumed[selector.Sel.Name] = struct{}{}
		}
		return true
	}
}

func nativeArtifactSpecFieldDiff(producer []string, consumed map[string]struct{}) (missing, stale []string) {
	known := make(map[string]struct{}, len(producer))
	for _, field := range producer {
		known[field] = struct{}{}
		if _, ok := consumed[field]; !ok {
			missing = append(missing, field)
		}
	}
	for field := range consumed {
		if _, ok := known[field]; !ok {
			stale = append(stale, field)
		}
	}
	slices.Sort(missing)
	slices.Sort(stale)
	return missing, stale
}
