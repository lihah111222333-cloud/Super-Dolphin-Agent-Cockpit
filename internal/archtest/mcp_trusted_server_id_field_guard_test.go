package archtest

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestTrustedServerIDProducerFieldGuard(t *testing.T) {
	field := trustedServerIDOwnerField(t)
	binaryField, ok := reflect.TypeFor[providerdto.MCPBinary]().FieldByName(field.Name)
	if !ok {
		t.Fatalf("MCPBinary missing config-owner field %q", field.Name)
	}
	if binaryField.Tag.Get("json") != field.Tag.Get("json") {
		t.Fatalf("MCPBinary.%s json tag = %q, want owner tag %q", field.Name, binaryField.Tag.Get("json"), field.Tag.Get("json"))
	}

	for _, check := range []struct {
		path     string
		function string
		minUses  int
	}{
		{path: "internal/module/mcp_server/config_helpers.go", function: "mcpServersToContract", minUses: 1},
		{path: "internal/module/thread/contract_adapter.go", function: "cloneSessionMCPServerConfigs", minUses: 2},
		{path: "internal/module/thread/mcp_server_config.go", function: "normalizePromptHTTPMCPServerConfig", minUses: 2},
		{path: "internal/module/thread/mcp_server_config.go", function: "normalizePromptStdioMCPServerConfig", minUses: 2},
		{path: "internal/module/thread/mcp_server_config.go", function: "copyMCPServerConfigs", minUses: 2},
		{path: "internal/module/thread/start_session_helpers.go", function: "renderMCPServerConfigMap", minUses: 1},
		{path: "internal/module/turn/manifest.go", function: "mcpServerConfigBinary", minUses: 4},
		{path: "internal/module/turn/service_helpers.go", function: "normalizeTurnHTTPMCPServerConfig", minUses: 2},
		{path: "internal/module/turn/service_helpers.go", function: "normalizeTurnStdioMCPServerConfig", minUses: 2},
		{path: "internal/module/turn/service_helpers.go", function: "copyTurnMCPServerConfigs", minUses: 2},
	} {
		assertTrustedServerIDFunctionUses(t, check.path, check.function, field.Name, check.minUses)
	}
}

func trustedServerIDOwnerField(t *testing.T) reflect.StructField {
	t.Helper()
	owner := reflect.TypeFor[contract.MCPServerConfig]()
	var matched []reflect.StructField
	for i := range owner.NumField() {
		field := owner.Field(i)
		if strings.Split(field.Tag.Get("json"), ",")[0] == contract.RuntimeMCPTrustedServerIDKey {
			matched = append(matched, field)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("MCPServerConfig fields for owner key %q = %d, want 1", contract.RuntimeMCPTrustedServerIDKey, len(matched))
	}
	return matched[0]
}

func assertTrustedServerIDFunctionUses(t *testing.T, path, function, field string, minUses int) {
	t.Helper()
	source, err := task4BSource(path, nil)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	uses, err := task4BConsumerFieldUses(source, path, function, field)
	if err != nil {
		t.Fatal(err)
	}
	if uses < minUses {
		t.Fatalf("%s:%s uses config-owner field %q %d times, want at least %d", path, function, field, uses, minUses)
	}
}

func TestManagedMCPBinaryProductionOwnerGuard(t *testing.T) {
	calls, err := managedMCPProductionCalls(repoRootForGuardTests(t))
	if err != nil {
		t.Fatalf("scan production Go files: %v", err)
	}
	if err := validateManagedMCPProductionCalls(calls); err != nil {
		t.Fatal(err)
	}
	assertManagedMCPMarkerNotSerializable(t)
}

func TestManagedMCPBinaryProductionOwnerGuardRejectsRepositoryMutation(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		owner       string
		declaration string
	}{
		{
			name: "direct-call", owner: "buildForgedBinary",
			declaration: `func buildForgedBinary() providerdto.MCPBinary {
	return providerdto.NewManagedMCPBinary(providerdto.MCPBinary{Name: "forged"})
}`,
		},
		{
			name: "function-value", owner: "aliasManagedConstructor",
			declaration: `func aliasManagedConstructor() providerdto.MCPBinary {
	constructor := providerdto.NewManagedMCPBinary
	_ = constructor
	return providerdto.MCPBinary{Name: "forged"}
}`,
		},
		{
			name: "package-function-value", owner: "<package>",
			declaration: `var packageConstructor = providerdto.NewManagedMCPBinary`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "cmd", "forged", "main.go")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create mutation fixture directory: %v", err)
			}
			source := fmt.Appendf(nil, `package main

import providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"

%s
`, fixture.declaration)
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatalf("write mutation fixture: %v", err)
			}
			calls, err := managedMCPProductionCalls(root)
			if err != nil {
				t.Fatalf("scan mutation fixture: %v", err)
			}
			err = validateManagedMCPProductionCalls(calls)
			if err == nil || !strings.Contains(err.Error(), "cmd/forged/main.go:"+fixture.owner) {
				t.Fatalf("managed constructor mutation error = %v, want illegal production reference", err)
			}
		})
	}
}

type managedMCPProductionCall struct {
	Path     string
	Function string
	Line     int
	Direct   bool
}

const managedMCPProviderImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"

func managedMCPProductionCalls(root string) ([]managedMCPProductionCall, error) {
	var calls []managedMCPProductionCall
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && managedMCPExcludedProductionDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !managedMCPGuardProductionFile(rel) {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if generatedGoSource(source) {
			return nil
		}
		fileCalls, err := managedMCPCallsInSource(rel, source)
		if err != nil {
			return err
		}
		calls = append(calls, fileCalls...)
		return nil
	})
	return calls, err
}

func managedMCPExcludedProductionDir(rel string) bool {
	for part := range strings.SplitSeq(rel, "/") {
		switch part {
		case ".git", ".worktrees", ".workspace", ".build-cache", ".agent", ".agents",
			"vendor", "node_modules", "dist", "bin", "test", "testdata", "docs", "third_party":
			return true
		}
	}
	return false
}

func managedMCPGuardProductionFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func generatedGoSource(source []byte) bool {
	prefix := source
	if len(prefix) > 4096 {
		prefix = prefix[:4096]
	}
	return strings.Contains(string(prefix), "Code generated") && strings.Contains(string(prefix), "DO NOT EDIT")
}

func managedMCPCallsInSource(path string, source []byte) ([]managedMCPProductionCall, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse imports %s: %w", path, err)
	}
	aliases, dotImport := managedMCPProviderAliases(file)
	if len(aliases) == 0 && !dotImport {
		return nil, nil
	}
	file, err = parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	direct := managedMCPDirectReferences(file, aliases, dotImport)
	return managedMCPReferencesInFile(fset, path, file, aliases, dotImport, direct), nil
}

func managedMCPDirectReferences(root ast.Node, aliases map[string]struct{}, dotImport bool) map[token.Pos]struct{} {
	direct := make(map[token.Pos]struct{})
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && isManagedMCPConstructorReference(call.Fun, aliases, dotImport) {
			direct[call.Fun.Pos()] = struct{}{}
		}
		return true
	})
	return direct
}

func managedMCPReferencesInFile(
	fset *token.FileSet,
	path string,
	file *ast.File,
	aliases map[string]struct{},
	dotImport bool,
	direct map[token.Pos]struct{},
) []managedMCPProductionCall {
	var calls []managedMCPProductionCall
	ast.Inspect(file, func(node ast.Node) bool {
		if !isManagedMCPConstructorReference(node, aliases, dotImport) {
			return true
		}
		_, isDirect := direct[node.Pos()]
		calls = append(calls, managedMCPProductionCall{
			Path: path, Function: managedMCPReferenceOwner(file, node.Pos()), Line: fset.Position(node.Pos()).Line, Direct: isDirect,
		})
		_, selector := node.(*ast.SelectorExpr)
		return !selector
	})
	return calls
}

func managedMCPReferenceOwner(file *ast.File, pos token.Pos) string {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Pos() <= pos && pos < fn.End() {
			return fn.Name.Name
		}
	}
	return "<package>"
}

func managedMCPProviderAliases(file *ast.File) (map[string]struct{}, bool) {
	aliases := make(map[string]struct{})
	dotImport := false
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != managedMCPProviderImportPath {
			continue
		}
		if spec.Name == nil {
			aliases["provider"] = struct{}{}
			continue
		}
		if spec.Name.Name == "." {
			dotImport = true
			continue
		}
		if spec.Name.Name != "_" {
			aliases[spec.Name.Name] = struct{}{}
		}
	}
	return aliases, dotImport
}

func isManagedMCPConstructorReference(node ast.Node, aliases map[string]struct{}, dotImport bool) bool {
	if ident, ok := node.(*ast.Ident); ok {
		return dotImport && ident.Name == "NewManagedMCPBinary"
	}
	selector, ok := node.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewManagedMCPBinary" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = aliases[ident.Name]
	return ok
}

func validateManagedMCPProductionCalls(calls []managedMCPProductionCall) error {
	const ownerPath = "internal/contract/manifest.go"
	const ownerFunction = "BuildManifest"
	foundOwner := false
	for _, call := range calls {
		if call.Path != ownerPath || call.Function != ownerFunction {
			return fmt.Errorf(
				"managed MCP constructor production reference %s:%s:%d is outside manifest owner",
				call.Path, call.Function, call.Line,
			)
		}
		if !call.Direct {
			return fmt.Errorf(
				"managed MCP constructor production reference %s:%s:%d is not a direct call",
				call.Path, call.Function, call.Line,
			)
		}
		foundOwner = true
	}
	if !foundOwner {
		return fmt.Errorf("managed MCP constructor owner %s:%s has no production call", ownerPath, ownerFunction)
	}
	return nil
}

func assertManagedMCPMarkerNotSerializable(t *testing.T) {
	t.Helper()
	managed := providerdto.NewManagedMCPBinary(providerdto.MCPBinary{Name: "fixture"})
	raw, err := json.Marshal(managed)
	if err != nil {
		t.Fatalf("marshal managed MCP binary: %v", err)
	}
	var decoded providerdto.MCPBinary
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal managed MCP binary: %v", err)
	}
	if decoded.IsManagedMCPBinary() || strings.Contains(string(raw), "managed") {
		t.Fatalf("managed marker crossed JSON/config boundary: %s", raw)
	}
	extra := providerdto.MCPBinary{Name: "external-fixture", Command: []string{"/fixture"}}
	manifest := contract.BuildManifest(providerdto.ManifestContext{
		BinaryDir: "/fixture", ExtraBinaries: []providerdto.MCPBinary{extra},
	})
	for _, binary := range manifest.Binaries {
		if binary.Name == extra.Name && binary.IsManagedMCPBinary() {
			t.Fatal("ExtraBinaries elevated an external binary to managed")
		}
	}
}

func TestMCPAuthorityQuarantineCompiledSchemaFieldGuard(t *testing.T) {
	for _, chain := range task4BChangedFieldChains() {
		t.Run(chain.ID, func(t *testing.T) {
			if err := validateTask4BFieldChain(chain, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTask4BChangedFieldGuardMutationFixtures(t *testing.T) {
	chains := task4BChangedFieldChains()
	for _, chain := range chains {
		if chain.ProducerJSON != "" {
			continue
		}
		chain := chain
		t.Run(chain.ID+"/add-unconsumed-producer-field", func(t *testing.T) {
			source := mustTask4BSource(t, chain.ProducerPath)
			mutated, err := addTask4BProducerField(source, chain.ProducerPath, chain.ProducerType, "MutationUnconsumed")
			if err != nil {
				t.Fatalf("mutate producer: %v", err)
			}
			err = validateTask4BFieldChain(chain, map[string][]byte{chain.ProducerPath: mutated})
			assertTask4BMutationError(t, err, chain, "MutationUnconsumed")
		})
	}

	trusted := chains[0]
	consumer := trusted.Consumers[len(trusted.Consumers)-1]
	field := trustedServerIDOwnerField(t).Name
	mutated, err := removeTask4BConsumerFieldUse(
		mustTask4BSource(t, consumer.Path), consumer.Path, consumer.Function, field,
	)
	if err != nil {
		t.Fatalf("mutate TrustedServerID mapper: %v", err)
	}
	err = validateTask4BFieldChain(trusted, map[string][]byte{consumer.Path: mutated})
	assertTask4BMutationError(t, err, trusted, field)
}

type task4BFieldConsumer struct {
	Path     string
	Function string
	MinUses  int
}

type task4BFieldChain struct {
	ID           string
	ProducerPath string
	ProducerType string
	ProducerJSON string
	Consumers    []task4BFieldConsumer
}

func task4BChangedFieldChains() []task4BFieldChain {
	const ownerPath = "internal/module/mcp_server/authority_owner.go"
	const surfacePath = "internal/platform/toolbridge/handler_codex_surface_store.go"
	return []task4BFieldChain{
		{
			ID: "trusted-server-id", ProducerPath: "internal/contract/mcp_control.go", ProducerType: "MCPServerConfig",
			ProducerJSON: contract.RuntimeMCPTrustedServerIDKey,
			Consumers: []task4BFieldConsumer{
				{Path: "internal/module/mcp_server/config_helpers.go", Function: "mcpServersToContract", MinUses: 1},
				{Path: "internal/module/thread/contract_adapter.go", Function: "cloneSessionMCPServerConfigs", MinUses: 2},
				{Path: "internal/module/thread/mcp_server_config.go", Function: "normalizePromptHTTPMCPServerConfig", MinUses: 2},
				{Path: "internal/module/thread/mcp_server_config.go", Function: "normalizePromptStdioMCPServerConfig", MinUses: 2},
				{Path: "internal/module/thread/mcp_server_config.go", Function: "copyMCPServerConfigs", MinUses: 2},
				{Path: "internal/module/thread/start_session_helpers.go", Function: "renderMCPServerConfigMap", MinUses: 1},
				{Path: "internal/module/turn/manifest.go", Function: "mcpServerConfigBinary", MinUses: 4},
				{Path: "internal/module/turn/service_helpers.go", Function: "normalizeTurnHTTPMCPServerConfig", MinUses: 2},
				{Path: "internal/module/turn/service_helpers.go", Function: "normalizeTurnStdioMCPServerConfig", MinUses: 2},
				{Path: "internal/module/turn/service_helpers.go", Function: "copyTurnMCPServerConfigs", MinUses: 2},
			},
		},
		{
			ID: "authority-issue", ProducerPath: "internal/contract/mcp_control.go", ProducerType: "MCPToolAuthorityIssueRequest",
			Consumers: []task4BFieldConsumer{
				{Path: ownerPath, Function: "IssueMCPToolAuthority", MinUses: 1},
				{Path: surfacePath, Function: "beginMCPAuthority", MinUses: 1},
			},
		},
		{
			ID: "authority-token", ProducerPath: "internal/contract/mcp_control.go", ProducerType: "MCPToolAuthority",
			Consumers: []task4BFieldConsumer{{Path: ownerPath, MinUses: 1}},
		},
		{
			ID: "quarantine-commit", ProducerPath: "internal/contract/mcp_control.go", ProducerType: "MCPToolQuarantineCommit",
			Consumers: []task4BFieldConsumer{
				{Path: ownerPath, Function: "CompareAndSwapMCPToolQuarantines", MinUses: 1},
				{Path: surfacePath, Function: "publishMCPSurfaceCurrentCAS", MinUses: 1},
			},
		},
		{
			ID: "authority-owner-state", ProducerPath: ownerPath, ProducerType: "mcpToolAuthorityState",
			Consumers: []task4BFieldConsumer{{Path: ownerPath, MinUses: 1}},
		},
		{
			ID: "schema-authority-state", ProducerPath: surfacePath, ProducerType: "mcpSchemaAuthority",
			Consumers: []task4BFieldConsumer{{Path: surfacePath, MinUses: 1}},
		},
		{
			ID: "compiled-schema", ProducerPath: "internal/platform/toolbridge/schema/canonical.go", ProducerType: "CanonicalSchema",
			Consumers: []task4BFieldConsumer{
				{Path: "internal/platform/toolbridge/schema/protocol.go", Function: "newProtocolRequest", MinUses: 1},
			},
		},
		{
			ID: "compiled-schema-terminal", ProducerPath: surfacePath, ProducerType: "admittedMCPTool",
			Consumers: []task4BFieldConsumer{{Path: surfacePath, Function: "addSingleMCPToolToSurface", MinUses: 1}},
		},
	}
}

func validateTask4BFieldChain(chain task4BFieldChain, overrides map[string][]byte) error {
	producerSource, err := task4BSource(chain.ProducerPath, overrides)
	if err != nil {
		return fmt.Errorf("chain=%s producer=%s field=<read>: %w", chain.ID, chain.ProducerType, err)
	}
	fields, err := task4BProducerFields(chain, producerSource)
	if err != nil {
		return err
	}
	for _, field := range fields {
		for _, consumer := range chain.Consumers {
			source, err := task4BSource(consumer.Path, overrides)
			if err != nil {
				return fmt.Errorf("chain=%s producer=%s field=%s: read consumer %s: %w", chain.ID, chain.ProducerType, field, consumer.Path, err)
			}
			uses, err := task4BConsumerFieldUses(source, consumer.Path, consumer.Function, field)
			if err != nil {
				return fmt.Errorf("chain=%s producer=%s field=%s: %w", chain.ID, chain.ProducerType, field, err)
			}
			if uses < consumer.MinUses {
				return fmt.Errorf(
					"chain=%s producer=%s field=%s: consumer %s:%s uses field %d times, want at least %d",
					chain.ID, chain.ProducerType, field, consumer.Path, consumer.Function, uses, consumer.MinUses,
				)
			}
		}
	}
	return nil
}

func task4BSource(path string, overrides map[string][]byte) ([]byte, error) {
	if source, ok := overrides[path]; ok {
		return source, nil
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("resolve archtest source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	return source, nil
}

func mustTask4BSource(t *testing.T, path string) []byte {
	t.Helper()
	source, err := task4BSource(path, nil)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return source
}

func task4BProducerFields(chain task4BFieldChain, source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), chain.ProducerPath, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("chain=%s producer=%s field=<parse>: %w", chain.ID, chain.ProducerType, err)
	}
	structure, err := task4BStructType(file, chain.ProducerType)
	if err != nil {
		return nil, fmt.Errorf("chain=%s producer=%s field=<type>: %w", chain.ID, chain.ProducerType, err)
	}
	var fields []string
	for _, field := range structure.Fields.List {
		if chain.ProducerJSON != "" && task4BJSONFieldName(field) != chain.ProducerJSON {
			continue
		}
		for _, name := range field.Names {
			fields = append(fields, name.Name)
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("chain=%s producer=%s field=<none>: no guarded producer fields", chain.ID, chain.ProducerType)
	}
	return fields, nil
}

func task4BStructType(file *ast.File, typeName string) (*ast.StructType, error) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			structure, isStruct := typeSpecStruct(typeSpec, ok)
			if !isStruct || typeSpec.Name.Name != typeName {
				continue
			}
			return structure, nil
		}
	}
	return nil, fmt.Errorf("missing struct %s", typeName)
}

func typeSpecStruct(spec *ast.TypeSpec, ok bool) (*ast.StructType, bool) {
	if !ok {
		return nil, false
	}
	structure, isStruct := spec.Type.(*ast.StructType)
	return structure, isStruct
}

func task4BJSONFieldName(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	tag, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	return strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
}

func task4BConsumerFieldUses(source []byte, path, function, field string) (int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return 0, fmt.Errorf("parse consumer %s: %w", path, err)
	}
	root, err := task4BConsumerRoot(file, path, function)
	if err != nil {
		return 0, err
	}
	return countTask4BFieldUses(root, field), nil
}

func task4BConsumerRoot(file *ast.File, path, function string) (ast.Node, error) {
	if function == "" {
		return file, nil
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == function {
			return fn.Body, nil
		}
	}
	return nil, fmt.Errorf("consumer %s missing function %s", path, function)
}

func countTask4BFieldUses(root ast.Node, field string) int {
	uses := 0
	ast.Inspect(root, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.SelectorExpr:
			if typed.Sel.Name == field {
				uses++
			}
		case *ast.KeyValueExpr:
			if ident, ok := typed.Key.(*ast.Ident); ok && ident.Name == field {
				uses++
			}
		}
		return true
	})
	return uses
}

func addTask4BProducerField(source []byte, path, typeName, fieldName string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	structure, err := task4BStructType(file, typeName)
	if err != nil {
		return nil, err
	}
	offset := fset.Position(structure.Fields.Closing).Offset
	mutated := append([]byte(nil), source[:offset]...)
	mutated = append(mutated, []byte("\t"+fieldName+" bool\n")...)
	mutated = append(mutated, source[offset:]...)
	return mutated, nil
}

func removeTask4BConsumerFieldUse(source []byte, path, function, field string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function || fn.Body == nil {
			continue
		}
		var target *ast.Ident
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if target != nil {
				return false
			}
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == field {
				target = selector.Sel
				return false
			}
			return true
		})
		if target == nil {
			return nil, fmt.Errorf("%s:%s has no selector use of %s", path, function, field)
		}
		start := fset.Position(target.Pos()).Offset
		end := fset.Position(target.End()).Offset
		mutated := append([]byte(nil), source[:start]...)
		mutated = append(mutated, []byte("MutationRemoved")...)
		mutated = append(mutated, source[end:]...)
		return mutated, nil
	}
	return nil, fmt.Errorf("%s missing function %s", path, function)
}

func assertTask4BMutationError(t *testing.T, err error, chain task4BFieldChain, field string) {
	t.Helper()
	for _, fragment := range []string{"chain=" + chain.ID, "producer=" + chain.ProducerType, "field=" + field} {
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("mutation error = %v, want %q", err, fragment)
		}
	}
}
