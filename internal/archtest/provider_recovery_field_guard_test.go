package archtest

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	modulethread "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	moduleuistate "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
	"golang.org/x/tools/go/packages"
)

const providerRecoveryImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"

type providerRecoveryConstructionGuard struct {
	producer reflect.Type
	fields   map[string]string
}

type providerRecoveryConstruction struct {
	fields map[string]string
}

type providerRecoveryFieldExemption struct {
	direction string
	reason    string
	owner     string
}

// TestProviderRecoveryMapperFieldGuard 动态枚举 production Request 构造并验证 registry 差集。
func TestProviderRecoveryMapperFieldGuard(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	inventory, err := discoverProviderRecoveryConstructions(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProviderRecoveryConstructions(
		providerRecoveryRequestFields(),
		providerRecoveryConstructionRegistry(),
		inventory,
		providerRecoveryFieldExemptions(),
	); err != nil {
		t.Fatal(err)
	}
}

// TestProviderRecoveryFieldGuardMutationMatrix 锁定未知 mapper、删字段和错误 selector。
func TestProviderRecoveryFieldGuardMutationMatrix(t *testing.T) {
	t.Parallel()

	requestFields := map[string]bool{"Provider": true, "ProviderThreadID": true}
	exemptions := map[string]providerRecoveryFieldExemption{}
	registry := map[string]providerRecoveryConstructionGuard{
		"internal/example.go:mapBinding": {
			fields: map[string]string{"Provider": "binding.Provider", "ProviderThreadID": "binding.ProviderThreadID"},
		},
	}
	baseInventory := map[string]providerRecoveryConstruction{
		"internal/example.go:mapBinding": {
			fields: map[string]string{"Provider": "binding.Provider", "ProviderThreadID": "binding.ProviderThreadID"},
		},
	}
	tests := []struct {
		name      string
		mutate    func(map[string]providerRecoveryConstruction, map[string]providerRecoveryConstructionGuard)
		wantError string
	}{
		{
			name: "unknown mapper",
			mutate: func(inventory map[string]providerRecoveryConstruction, _ map[string]providerRecoveryConstructionGuard) {
				inventory["internal/unknown.go:newMapper"] = providerRecoveryConstruction{
					fields: map[string]string{"Provider": "binding.Provider", "ProviderThreadID": "binding.ProviderThreadID"},
				}
			},
			wantError: "unregistered production constructions",
		},
		{
			name: "deleted request field",
			mutate: func(inventory map[string]providerRecoveryConstruction, _ map[string]providerRecoveryConstructionGuard) {
				delete(inventory["internal/example.go:mapBinding"].fields, "ProviderThreadID")
			},
			wantError: "field set",
		},
		{
			name: "wrong selector",
			mutate: func(inventory map[string]providerRecoveryConstruction, _ map[string]providerRecoveryConstructionGuard) {
				inventory["internal/example.go:mapBinding"].fields["ProviderThreadID"] = "binding.CodexThreadID"
			},
			wantError: "expression",
		},
		{
			name: "stale registry",
			mutate: func(_ map[string]providerRecoveryConstruction, registry map[string]providerRecoveryConstructionGuard) {
				registry["internal/stale.go:oldMapper"] = providerRecoveryConstructionGuard{
					fields: map[string]string{"Provider": "binding.Provider", "ProviderThreadID": "binding.ProviderThreadID"},
				}
			},
			wantError: "stale registry constructions",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inventory := cloneProviderRecoveryInventory(baseInventory)
			registryCopy := cloneProviderRecoveryRegistry(registry)
			tc.mutate(inventory, registryCopy)
			err := validateProviderRecoveryConstructions(requestFields, registryCopy, inventory, exemptions)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("validateProviderRecoveryConstructions() error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

// TestProviderRecoveryFieldGuardDiscoversAliasAndAssignments 锁定 alias 与逐字段 mapper 的真实类型发现。
func TestProviderRecoveryFieldGuardDiscoversAliasAndAssignments(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	inventory, err := discoverTypedProviderRecoveryConstructions(
		root,
		"./internal/archtest/testdata/providerrecoveryguard",
	)
	if err != nil {
		t.Fatal(err)
	}
	id := "internal/archtest/testdata/providerrecoveryguard/alias_assignment.go:mapAliasByField"
	construction, ok := inventory[id]
	if !ok {
		t.Fatalf("typed inventory missing alias assignment mapper %s: %v", id, mapKeys(inventory))
	}
	if !slices.Equal(mapKeys(construction.fields), mapKeys(providerRecoveryRequestFields())) {
		t.Fatalf("alias assignment fields = %v, want %v", mapKeys(construction.fields), mapKeys(providerRecoveryRequestFields()))
	}
	if construction.fields["ProviderThreadID"] != "binding.ProviderThreadID" {
		t.Fatalf("ProviderThreadID expression = %q", construction.fields["ProviderThreadID"])
	}
}

// TestProviderRecoveryFieldGuardFailsOnRealMapperSelectorMutation 直接变异真实 production mapper overlay。
func TestProviderRecoveryFieldGuardFailsOnRealMapperSelectorMutation(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/module/uistate/module.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	from := []byte("ProviderThreadID: binding.ProviderThreadID,")
	to := []byte("ProviderThreadID: binding.CodexHome,")
	if bytes.Count(source, from) != 1 {
		t.Fatalf("real mapper selector count = %d, want 1", bytes.Count(source, from))
	}
	inventory, err := discoverTypedProviderRecoveryConstructionsWithOverlay(
		root,
		map[string][]byte{path: bytes.Replace(source, from, to, 1)},
		"./internal/module/uistate",
	)
	if err != nil {
		t.Fatal(err)
	}
	id := "internal/module/uistate/module.go:providerRecoveryRequestFromUIBinding"
	registry := map[string]providerRecoveryConstructionGuard{id: providerRecoveryConstructionRegistry()[id]}
	err = validateProviderRecoveryConstructions(
		providerRecoveryRequestFields(),
		registry,
		inventory,
		providerRecoveryFieldExemptions(),
	)
	if err == nil || !strings.Contains(err.Error(), "expression") {
		t.Fatalf("real mapper selector mutation error = %v, want expression fail-first", err)
	}
}

// providerRecoveryConstructionRegistry 登记已审 production 构造及字段来源。
func providerRecoveryConstructionRegistry() map[string]providerRecoveryConstructionGuard {
	return map[string]providerRecoveryConstructionGuard{
		"internal/module/thread/lifecycle_helpers.go:providerRecoveryRequestFromThreadBinding": {
			producer: reflect.TypeFor[modulethread.BindingRecord](),
			fields: map[string]string{
				"Provider": "binding.Provider", "RolloutPath": "binding.RolloutPath",
				"ProviderThreadID": "binding.ProviderThreadID", "SessionUUID": "binding.SessionUUID",
				"CodexHome": "codexHome", "ClaudeHome": "claudeHome",
			},
		},
		"internal/provider/unified/session_resolver.go:providerRecoveryRequestFromSessionBinding": {
			producer: reflect.TypeFor[contract.SessionBinding](),
			fields: map[string]string{
				"Provider": "binding.Provider", "RolloutPath": "binding.RolloutPath",
				"ProviderThreadID": "binding.ProviderThreadID", "SessionUUID": "binding.SessionUUID",
				"CodexHome": "binding.CodexHome", "ClaudeHome": `""`,
			},
		},
		"internal/module/uistate/module.go:providerRecoveryRequestFromUIBinding": {
			producer: reflect.TypeFor[moduleuistate.BindingEntry](),
			fields: map[string]string{
				"Provider": "binding.Provider", "RolloutPath": "binding.RolloutPath",
				"ProviderThreadID": "binding.ProviderThreadID", "SessionUUID": "binding.SessionUUID",
				"CodexHome": "binding.CodexHome", "ClaudeHome": `""`,
			},
		},
		"internal/module/thread/lifecycle_helpers.go:recoverableProviderThreadID": {
			fields: map[string]string{
				"Provider": "provider", "RolloutPath": "rolloutPath",
				"ProviderThreadID": "providerUUID", "SessionUUID": "providerUUID",
				"CodexHome": "codexHome", "ClaudeHome": "claudeHome",
			},
		},
	}
}

// providerRecoveryFieldExemptions 声明 Request 字段的有 owner 豁免。
func providerRecoveryFieldExemptions() map[string]providerRecoveryFieldExemption {
	return map[string]providerRecoveryFieldExemption{}
}

// discoverProviderRecoveryConstructions 扫描全部 production Go 文件中的 Request 构造。
func discoverProviderRecoveryConstructions(root string) (map[string]providerRecoveryConstruction, error) {
	return discoverTypedProviderRecoveryConstructions(root, "./internal/...")
}

// discoverTypedProviderRecoveryConstructions 以 go/types 的真实类型身份枚举构造。
func discoverTypedProviderRecoveryConstructions(root string, patterns ...string) (map[string]providerRecoveryConstruction, error) {
	return discoverTypedProviderRecoveryConstructionsWithOverlay(root, nil, patterns...)
}

func discoverTypedProviderRecoveryConstructionsWithOverlay(
	root string,
	overlay map[string][]byte,
	patterns ...string,
) (map[string]providerRecoveryConstruction, error) {
	config := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:     root,
		Overlay: overlay,
	}
	loaded, err := packages.Load(config, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load production packages for provider recovery guard: %w", err)
	}
	inventory := map[string]providerRecoveryConstruction{}
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("type-check provider recovery guard package %s: %s", pkg.PkgPath, pkg.Errors[0])
		}
		for index, file := range pkg.Syntax {
			path := pkg.CompiledGoFiles[index]
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return nil, fmt.Errorf("rel provider recovery source %s: %w", path, err)
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				fields := map[string]string{}
				owned := map[string]bool{}
				found := false
				var discoveryErr error
				ast.Inspect(function.Body, func(node ast.Node) bool {
					switch typed := node.(type) {
					case *ast.CompositeLit:
						if !isProviderRecoveryRequestType(pkg.TypesInfo.TypeOf(typed)) {
							return true
						}
						found = true
						literalFields, err := providerRecoveryLiteralFields(pkg.Fset, typed)
						if err != nil {
							discoveryErr = err
							return false
						}
						for field, expression := range literalFields {
							if _, duplicate := fields[field]; duplicate {
								discoveryErr = fmt.Errorf("duplicate providerrecovery.Request field %q", field)
								return false
							}
							fields[field] = expression
						}
					case *ast.ValueSpec:
						if isProviderRecoveryRequestType(pkg.TypesInfo.TypeOf(typed.Type)) {
							found = true
							for _, name := range typed.Names {
								owned[name.Name] = true
							}
						}
					case *ast.AssignStmt:
						for i, right := range typed.Rhs {
							if _, ok := right.(*ast.CompositeLit); !ok || !isProviderRecoveryRequestType(pkg.TypesInfo.TypeOf(right)) || i >= len(typed.Lhs) {
								continue
							}
							if name, ok := typed.Lhs[i].(*ast.Ident); ok {
								owned[name.Name] = true
							}
						}
						for i, left := range typed.Lhs {
							selector, ok := left.(*ast.SelectorExpr)
							if !ok || !isProviderRecoveryRequestType(pkg.TypesInfo.TypeOf(selector.X)) {
								continue
							}
							name, ok := selector.X.(*ast.Ident)
							if !ok || !owned[name.Name] {
								continue
							}
							found = true
							if i >= len(typed.Rhs) {
								discoveryErr = errors.New("providerrecovery.Request field assignment has no matching value")
								return false
							}
							var expression bytes.Buffer
							if err := printer.Fprint(&expression, pkg.Fset, typed.Rhs[i]); err != nil {
								discoveryErr = fmt.Errorf("print providerrecovery.Request field %q: %w", selector.Sel.Name, err)
								return false
							}
							fields[selector.Sel.Name] = expression.String()
						}
					}
					return discoveryErr == nil
				})
				if discoveryErr != nil {
					return nil, fmt.Errorf("%s:%s: %w", filepath.ToSlash(relative), function.Name.Name, discoveryErr)
				}
				if !found {
					continue
				}
				id := filepath.ToSlash(relative) + ":" + function.Name.Name
				if _, exists := inventory[id]; exists {
					return nil, fmt.Errorf("duplicate provider recovery construction id %s", id)
				}
				inventory[id] = providerRecoveryConstruction{fields: fields}
			}
		}
	}
	return inventory, nil
}

func isProviderRecoveryRequestType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, ok := typ.(*types.Named)
	return ok &&
		named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == providerRecoveryImportPath &&
		named.Obj().Name() == "Request"
}

// providerRecoverySkipDir 排除依赖、构建和历史生成目录。
func providerRecoverySkipDir(name string) bool {
	switch name {
	case ".git", ".build-cache", "bin", "node_modules", "dist", ".worktrees", ".workspace",
		".claude", ".agent", ".agnet", "vendor":
		return true
	default:
		return false
	}
}

// providerRecoveryConstructionsInFile 解析单文件内所有 Request composite literal。
func providerRecoveryConstructionsInFile(path, relative string) (map[string]providerRecoveryConstruction, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse provider recovery source %s: %w", relative, err)
	}
	alias, imported, err := providerRecoveryImportAlias(file)
	if err != nil || !imported {
		return map[string]providerRecoveryConstruction{}, err
	}
	inventory := map[string]providerRecoveryConstruction{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		found := 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || providerRecoveryRequestExprName(literal.Type) != alias+".Request" {
				return true
			}
			found++
			id := relative + ":" + function.Name.Name
			if found > 1 {
				err = fmt.Errorf("%s contains multiple providerrecovery.Request constructions", id)
				return false
			}
			fields, fieldErr := providerRecoveryLiteralFields(fileSet, literal)
			if fieldErr != nil {
				err = fmt.Errorf("%s: %w", id, fieldErr)
				return false
			}
			inventory[id] = providerRecoveryConstruction{fields: fields}
			return false
		})
		if err != nil {
			return nil, err
		}
	}
	return inventory, nil
}

// providerRecoveryImportAlias 返回 providerrecovery import 的文件内别名。
func providerRecoveryImportAlias(file *ast.File) (string, bool, error) {
	for _, imported := range file.Imports {
		if strings.Trim(imported.Path.Value, `"`) != providerRecoveryImportPath {
			continue
		}
		if imported.Name == nil {
			return "providerrecovery", true, nil
		}
		if imported.Name.Name == "." || imported.Name.Name == "_" {
			return "", false, fmt.Errorf("providerrecovery import must use a named package alias")
		}
		return imported.Name.Name, true, nil
	}
	return "", false, nil
}

// providerRecoveryLiteralFields 提取 keyed literal 的字段和精确表达式。
func providerRecoveryLiteralFields(fileSet *token.FileSet, literal *ast.CompositeLit) (map[string]string, error) {
	fields := make(map[string]string, len(literal.Elts))
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, errors.New("providerrecovery.Request must use keyed fields")
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			return nil, errors.New("providerrecovery.Request field key must be an identifier")
		}
		if _, duplicate := fields[key.Name]; duplicate {
			return nil, fmt.Errorf("duplicate providerrecovery.Request field %q", key.Name)
		}
		var expression bytes.Buffer
		if err := printer.Fprint(&expression, fileSet, keyValue.Value); err != nil {
			return nil, fmt.Errorf("print providerrecovery.Request field %q: %w", key.Name, err)
		}
		fields[key.Name] = expression.String()
	}
	return fields, nil
}

// validateProviderRecoveryConstructions 计算 unknown、stale、missing 和错误映射差集。
func validateProviderRecoveryConstructions(
	requestFields map[string]bool,
	registry map[string]providerRecoveryConstructionGuard,
	inventory map[string]providerRecoveryConstruction,
	exemptions map[string]providerRecoveryFieldExemption,
) error {
	if err := validateProviderRecoveryExemptions(requestFields, exemptions); err != nil {
		return err
	}
	unknown := stringSetDifference(mapKeys(inventory), mapKeys(registry))
	if len(unknown) > 0 {
		return fmt.Errorf("unregistered production constructions: %v", unknown)
	}
	stale := stringSetDifference(mapKeys(registry), mapKeys(inventory))
	if len(stale) > 0 {
		return fmt.Errorf("stale registry constructions: %v", stale)
	}
	expectedFields := cloneBoolMap(requestFields)
	for field := range exemptions {
		delete(expectedFields, field)
	}
	for id, registered := range registry {
		if !slices.Equal(mapKeys(registered.fields), mapKeys(expectedFields)) {
			return fmt.Errorf("%s registry field set = %v, want %v", id, mapKeys(registered.fields), mapKeys(expectedFields))
		}
		implementation := inventory[id]
		if !slices.Equal(mapKeys(implementation.fields), mapKeys(expectedFields)) {
			return fmt.Errorf("%s implementation field set = %v, want %v", id, mapKeys(implementation.fields), mapKeys(expectedFields))
		}
		if registered.producer != nil {
			producerFields := structFieldSet(registered.producer)
			for _, expression := range registered.fields {
				selector := strings.TrimPrefix(expression, "binding.")
				if selector == expression {
					continue
				}
				if !producerFields[selector] {
					return fmt.Errorf("%s producer %s missing selector %q", id, registered.producer, expression)
				}
			}
		}
		for field, wantExpression := range registered.fields {
			if gotExpression := implementation.fields[field]; gotExpression != wantExpression {
				return fmt.Errorf("%s field %s expression = %q, want %q", id, field, gotExpression, wantExpression)
			}
		}
	}
	return nil
}

// validateProviderRecoveryExemptions 拒绝未知字段和无理由豁免。
func validateProviderRecoveryExemptions(
	requestFields map[string]bool,
	exemptions map[string]providerRecoveryFieldExemption,
) error {
	for field, exemption := range exemptions {
		if !requestFields[field] {
			return fmt.Errorf("stale provider recovery field exemption %q", field)
		}
		if strings.TrimSpace(exemption.direction) == "" ||
			strings.TrimSpace(exemption.reason) == "" ||
			strings.TrimSpace(exemption.owner) == "" {
			return fmt.Errorf("provider recovery field exemption %q lacks direction, reason, or owner", field)
		}
	}
	return nil
}

// providerRecoveryRequestFields 从 port 类型动态派生生产字段集合。
func providerRecoveryRequestFields() map[string]bool {
	typ := reflect.TypeFor[providerrecovery.Request]()
	fields := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		fields[typ.Field(i).Name] = true
	}
	return fields
}

// providerRecoveryRequestExprName 返回 composite literal 的限定类型名。
func providerRecoveryRequestExprName(expression ast.Expr) string {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	prefix, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return prefix.Name + "." + selector.Sel.Name
}

// stringSetDifference 返回 left 中不存在于 right 的稳定差集。
func stringSetDifference(left, right []string) []string {
	seen := make(map[string]bool, len(right))
	for _, value := range right {
		seen[value] = true
	}
	var difference []string
	for _, value := range left {
		if !seen[value] {
			difference = append(difference, value)
		}
	}
	return difference
}

// cloneProviderRecoveryInventory 深拷贝 mutation 测试 inventory。
func cloneProviderRecoveryInventory(input map[string]providerRecoveryConstruction) map[string]providerRecoveryConstruction {
	cloned := make(map[string]providerRecoveryConstruction, len(input))
	for id, construction := range input {
		cloned[id] = providerRecoveryConstruction{fields: cloneStringMap(construction.fields)}
	}
	return cloned
}

// cloneProviderRecoveryRegistry 深拷贝 mutation 测试 registry。
func cloneProviderRecoveryRegistry(input map[string]providerRecoveryConstructionGuard) map[string]providerRecoveryConstructionGuard {
	cloned := make(map[string]providerRecoveryConstructionGuard, len(input))
	for id, guard := range input {
		cloned[id] = providerRecoveryConstructionGuard{producer: guard.producer, fields: cloneStringMap(guard.fields)}
	}
	return cloned
}

// cloneStringMap 深拷贝字符串 map。
func cloneStringMap(input map[string]string) map[string]string {
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

// cloneBoolMap 深拷贝布尔 map。
func cloneBoolMap(input map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
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
