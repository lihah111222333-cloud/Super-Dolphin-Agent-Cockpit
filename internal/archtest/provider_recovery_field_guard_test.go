package archtest

import (
	"bytes"
	"crypto/sha256"
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
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	modulethread "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	moduleuistate "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
	"golang.org/x/tools/go/packages"
)

const providerRecoveryModulePath = "github.com/lihah111222333-cloud/super-dolphin-agent"
const providerRecoveryImportPath = providerRecoveryModulePath + "/internal/util/providerrecovery"

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

// TestProviderRecoveryCandidateDiscoveryParity 固定候选 production 包与编译文件集合，避免 typed loader 静默缩小基线。
func TestProviderRecoveryCandidateDiscoveryParity(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	candidates, err := discoverProviderRecoveryCandidateSet(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	const (
		wantPackageCount  = 3
		wantPackageDigest = "8bba5c3271a3a6a4f28807be4128721aa82a8940bc364388e2babafa8c5136db"
		wantFileCount     = 63
		wantFileDigest    = "cb05d4a57fa29866f2010614fe177f060baccd93e93b5483dc1e2c53649a5996"
	)
	if len(candidates.packagePaths) != wantPackageCount ||
		providerRecoveryCandidateDigest(candidates.packagePaths) != wantPackageDigest {
		t.Fatalf("provider recovery candidate packages count=%d digest=%s", len(candidates.packagePaths), providerRecoveryCandidateDigest(candidates.packagePaths))
	}
	if len(candidates.filePaths) != wantFileCount ||
		providerRecoveryCandidateDigest(candidates.filePaths) != wantFileDigest {
		t.Fatalf("provider recovery candidate files count=%d digest=%s", len(candidates.filePaths), providerRecoveryCandidateDigest(candidates.filePaths))
	}
	for _, required := range []string{
		"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread",
		"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate",
		"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified",
	} {
		if !slices.Contains(candidates.packagePaths, required) {
			t.Fatalf("provider recovery candidate packages missing %s", required)
		}
	}
	for _, required := range []string{
		"internal/module/thread/lifecycle_helpers.go",
		"internal/module/uistate/module.go",
		"internal/provider/unified/session_resolver.go",
	} {
		if !slices.Contains(candidates.filePaths, required) {
			t.Fatalf("provider recovery candidate files missing %s", required)
		}
	}
}

// TestProviderRecoveryCandidateDiscoveryRejectsEmptyOverlay 固定候选为空时必须 fail-fast。
func TestProviderRecoveryCandidateDiscoveryRejectsEmptyOverlay(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	candidates, err := discoverProviderRecoveryCandidateSet(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	overlay := make(map[string][]byte, len(candidates.filePaths))
	for _, relative := range candidates.filePaths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		overlay[path] = bytes.ReplaceAll(source, []byte(providerRecoveryImportPath), []byte("example.com/removed/providerrecovery"))
	}
	if _, err := discoverProviderRecoveryCandidateSet(root, overlay); err == nil ||
		!strings.Contains(err.Error(), "found no production packages or files") {
		t.Fatalf("empty candidate overlay error = %v", err)
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
	from := []byte("ClaudeHome:       binding.ProviderRecoveryHome,")
	to := []byte(`ClaudeHome:       "",`)
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
	if err == nil || !strings.Contains(err.Error(), "empty constant") {
		t.Fatalf("real mapper selector deletion error = %v, want empty constant fail-first", err)
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
				"CodexHome": "providerRecoveryCodexHome(binding.CodexHome, binding.ProviderRecoveryHome)", "ClaudeHome": "binding.ProviderRecoveryHome",
			},
		},
		"internal/provider/unified/session_resolver.go:providerRecoveryRequestFromSessionBinding": {
			producer: reflect.TypeFor[contract.SessionBinding](),
			fields: map[string]string{
				"Provider": "binding.Provider", "RolloutPath": "binding.RolloutPath",
				"ProviderThreadID": "binding.ProviderThreadID", "SessionUUID": "binding.SessionUUID",
				"CodexHome": "providerRecoveryCodexHome(binding.CodexHome, binding.ProviderRecoveryHome)", "ClaudeHome": "binding.ProviderRecoveryHome",
			},
		},
		"internal/module/uistate/module.go:providerRecoveryRequestFromUIBinding": {
			producer: reflect.TypeFor[moduleuistate.BindingEntry](),
			fields: map[string]string{
				"Provider": "binding.Provider", "RolloutPath": "binding.RolloutPath",
				"ProviderThreadID": "binding.ProviderThreadID", "SessionUUID": "binding.SessionUUID",
				"CodexHome": "providerRecoveryCodexHome(binding.CodexHome, binding.ProviderRecoveryHome)", "ClaudeHome": "binding.ProviderRecoveryHome",
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
	return discoverProviderRecoveryConstructionsWithOverlay(root, nil)
}

func discoverProviderRecoveryConstructionsWithOverlay(
	root string,
	overlay map[string][]byte,
) (map[string]providerRecoveryConstruction, error) {
	candidates, err := discoverProviderRecoveryCandidateSet(root, overlay)
	if err != nil {
		return nil, err
	}
	return discoverTypedProviderRecoveryConstructionsWithOverlay(root, overlay, candidates.packagePaths...)
}

type providerRecoveryCandidateSet struct {
	packagePaths []string
	filePaths    []string
}

// discoverProviderRecoveryCandidateSet 先用轻量文件元数据装载定位 provider recovery 包，再进入 typed loader。
func discoverProviderRecoveryCandidateSet(root string, overlay map[string][]byte) (providerRecoveryCandidateSet, error) {
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./internal/..."},
		Tests:    false,
		Overlay:  overlay,
		LoadMode: packages.LoadFiles,
	})
	if err != nil {
		return providerRecoveryCandidateSet{}, fmt.Errorf("load provider recovery candidate packages: %w", err)
	}
	candidates := providerRecoveryCandidateSet{}
	for _, pkg := range loaded {
		if pkg == nil || !providerRecoveryProductionPackagePath(pkg.PkgPath) || len(pkg.GoFiles) == 0 {
			continue
		}
		sourceFiles := append([]string(nil), pkg.GoFiles...)
		sort.Strings(sourceFiles)
		imported := false
		for _, path := range sourceFiles {
			source, err := providerRecoverySourceBytes(path, overlay)
			if err != nil {
				return providerRecoveryCandidateSet{}, fmt.Errorf("read provider recovery candidate source %s: %w", path, err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
			if err != nil {
				return providerRecoveryCandidateSet{}, fmt.Errorf("parse provider recovery candidate source %s: %w", path, err)
			}
			_, hasImport, err := providerRecoveryImportAlias(file)
			if err != nil {
				return providerRecoveryCandidateSet{}, fmt.Errorf("inspect provider recovery candidate source %s: %w", path, err)
			}
			imported = imported || hasImport
		}
		if !imported {
			continue
		}
		candidates.packagePaths = append(candidates.packagePaths, pkg.PkgPath)
		for _, path := range sourceFiles {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return providerRecoveryCandidateSet{}, fmt.Errorf("rel provider recovery candidate source %s: %w", path, err)
			}
			candidates.filePaths = append(candidates.filePaths, filepath.ToSlash(relative))
		}
	}
	sort.Strings(candidates.packagePaths)
	sort.Strings(candidates.filePaths)
	if len(candidates.packagePaths) == 0 || len(candidates.filePaths) == 0 {
		return providerRecoveryCandidateSet{}, errors.New("provider recovery candidate discovery found no production packages or files")
	}
	return candidates, nil
}

// providerRecoveryProductionPackagePath 只接受当前模块内的非 archtest 生产包。
func providerRecoveryProductionPackagePath(pkgPath string) bool {
	archtestPath := providerRecoveryModulePath + "/internal/archtest"
	return strings.HasPrefix(pkgPath, providerRecoveryModulePath+"/internal/") &&
		pkgPath != archtestPath && !strings.HasPrefix(pkgPath, archtestPath+"/")
}

// providerRecoverySourceBytes 读取 overlay 覆盖后的候选源码，缺失时直接返回错误。
func providerRecoverySourceBytes(path string, overlay map[string][]byte) ([]byte, error) {
	if source, ok := overlay[path]; ok {
		return source, nil
	}
	return os.ReadFile(path)
}

// providerRecoveryCandidateDigest 对排序后的候选包或文件路径生成稳定摘要。
func providerRecoveryCandidateDigest(paths []string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(paths, "\n"))))
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
			if implementation.fields[field] == `""` {
				return fmt.Errorf("%s field %s uses unexempted empty constant", id, field)
			}
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
