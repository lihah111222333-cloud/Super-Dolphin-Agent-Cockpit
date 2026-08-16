package installer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"testing"
)

func TestInstallerConfigFieldsAreConsumedByProviderImplementation(t *testing.T) {
	producer := reflect.TypeFor[InstallerConfig]()
	producerFields := make([]string, 0, producer.NumField())
	for index := range producer.NumField() {
		producerFields = append(producerFields, producer.Field(index).Name)
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), "installer.go", nil, 0)
	if err != nil {
		t.Fatalf("parse installer provider implementation: %v", err)
	}
	consumed := make(map[string]struct{}, len(producerFields))
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "cfg" {
			consumed[selector.Sel.Name] = struct{}{}
		}
		return true
	})

	missing, stale := installerConfigFieldDiff(producerFields, consumed)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("InstallerConfig field coverage missing=%v stale=%v", missing, stale)
	}

	// Fail-first 证明：任意真实字段从实现消费集合消失时，守卫必须给出精确缺口。
	delete(consumed, "ManagedInstall")
	missing, stale = installerConfigFieldDiff(producerFields, consumed)
	if !slices.Equal(missing, []string{"ManagedInstall"}) || len(stale) != 0 {
		t.Fatalf("field guard fail-first missing=%v stale=%v, want [ManagedInstall]", missing, stale)
	}
}

func installerConfigFieldDiff(producer []string, consumed map[string]struct{}) (missing, stale []string) {
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
