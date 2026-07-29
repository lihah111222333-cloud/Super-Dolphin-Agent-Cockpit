package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	modulethread "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	moduleuistate "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
)

type providerRecoveryMapperGuard struct {
	path       string
	function   string
	producer   reflect.Type
	sourceName map[string]string
}

// TestProviderRecoveryMapperFieldGuard 动态约束 recovery port 到三个消费者的字段映射。
func TestProviderRecoveryMapperFieldGuard(t *testing.T) {
	t.Parallel()

	guards := []providerRecoveryMapperGuard{
		{
			path:     "internal/module/thread/lifecycle_helpers.go",
			function: "providerRecoveryRequestFromThreadBinding",
			producer: reflect.TypeFor[modulethread.BindingRecord](),
			sourceName: map[string]string{
				"Provider": "Provider", "RolloutPath": "RolloutPath", "PublicThreadID": "CodexThreadID",
				"ProviderThreadID": "ProviderThreadID", "SessionUUID": "SessionUUID", "CodexHome": "CodexHome",
			},
		},
		{
			path:     "internal/provider/unified/session_resolver.go",
			function: "providerRecoveryRequestFromSessionBinding",
			producer: reflect.TypeFor[contract.SessionBinding](),
			sourceName: map[string]string{
				"Provider": "Provider", "RolloutPath": "RolloutPath", "PublicThreadID": "CodexThreadID",
				"ProviderThreadID": "ProviderThreadID", "SessionUUID": "SessionUUID", "CodexHome": "CodexHome",
			},
		},
		{
			path:     "internal/module/uistate/module.go",
			function: "providerRecoveryRequestFromUIBinding",
			producer: reflect.TypeFor[moduleuistate.BindingEntry](),
			sourceName: map[string]string{
				"Provider": "Provider", "RolloutPath": "RolloutPath", "PublicThreadID": "CodexThreadID",
				"ProviderThreadID": "ProviderThreadID", "SessionUUID": "SessionUUID", "CodexHome": "CodexHome",
			},
		},
	}
	wantFunctions := []string{
		"providerRecoveryRequestFromSessionBinding",
		"providerRecoveryRequestFromThreadBinding",
		"providerRecoveryRequestFromUIBinding",
	}
	gotFunctions := make([]string, 0, len(guards))
	for _, guard := range guards {
		gotFunctions = append(gotFunctions, guard.function)
		assertProviderRecoveryMapper(t, guard)
	}
	slices.Sort(gotFunctions)
	if !slices.Equal(gotFunctions, wantFunctions) {
		t.Fatalf("provider recovery mapper registry = %v, want %v", gotFunctions, wantFunctions)
	}
}

// assertProviderRecoveryMapper 校验单个 producer 和 mapper 覆盖所有非豁免 port 字段。
func assertProviderRecoveryMapper(t *testing.T, guard providerRecoveryMapperGuard) {
	t.Helper()

	requestFields := providerRecoveryRequestFields()
	delete(requestFields, "ClaudeHome") // binding DTO 不持久化 provider home；Claude 运行时使用 canonical 环境根。
	if !reflect.DeepEqual(mapKeys(guard.sourceName), mapKeys(requestFields)) {
		t.Fatalf("%s field registry = %v, want request fields %v", guard.function, mapKeys(guard.sourceName), mapKeys(requestFields))
	}
	producerFields := structFieldSet(guard.producer)
	for requestField, producerField := range guard.sourceName {
		if !producerFields[producerField] {
			t.Fatalf("%s producer %s missing source field %q for %q", guard.function, guard.producer, producerField, requestField)
		}
	}
	got := providerRecoveryMapperFields(t, guard.path, guard.function)
	if !reflect.DeepEqual(got, guard.sourceName) {
		t.Fatalf("%s mapper fields = %v, want %v", guard.function, got, guard.sourceName)
	}
}

// providerRecoveryRequestFields 由 port 类型动态推导字段，避免硬编码字段计数。
func providerRecoveryRequestFields() map[string]bool {
	typ := reflect.TypeFor[providerrecovery.Request]()
	fields := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		fields[typ.Field(i).Name] = true
	}
	return fields
}

// providerRecoveryMapperFields 读取指定 mapper 的 keyed literal 与 producer selector。
func providerRecoveryMapperFields(t *testing.T, relPath, function string) map[string]string {
	t.Helper()
	path := filepath.Join(repoRootForGuardTests(t), relPath)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		fields := map[string]string{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || providerRecoveryExprName(literal.Type) != "providerrecovery.Request" {
				return true
			}
			for _, element := range literal.Elts {
				keyValue, keyed := element.(*ast.KeyValueExpr)
				key, keyOK := keyValue.Key.(*ast.Ident)
				value, valueOK := keyValue.Value.(*ast.SelectorExpr)
				if !keyed || !keyOK || !valueOK {
					t.Fatalf("%s must use keyed direct producer selectors", function)
				}
				fields[key.Name] = value.Sel.Name
			}
			return false
		})
		if len(fields) == 0 {
			t.Fatalf("%s missing providerrecovery.Request literal", function)
		}
		return fields
	}
	t.Fatalf("missing mapper function %s in %s", function, relPath)
	return nil
}

// providerRecoveryExprName 返回 mapper composite literal 的限定类型名。
func providerRecoveryExprName(expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	prefix, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return prefix.Name + "." + selector.Sel.Name
}

// mapKeys 返回稳定排序后的 map key。
func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
