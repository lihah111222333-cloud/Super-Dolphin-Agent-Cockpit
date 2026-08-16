package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEditSchemaExposesPatchDiskFieldsOnly(t *testing.T) {
	props, ok := newPatchEditSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("patch_edit schema properties type = %T", newPatchEditSchema()["properties"])
	}
	for _, field := range []string{"action", "file_path", "patch", "pos", "new_name", "only"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing expected field %q", field)
		}
	}
	for _, field := range []string{"line", "column", "end_line", "end_column", "edits", "new_text", "persist_to_disk", "force", "version", "response_detail"} {
		if _, ok := props[field]; ok {
			t.Fatalf("edit schema exposes removed legacy field %q", field)
		}
	}
	required, ok := newPatchEditSchema()["required"].([]string)
	if !ok {
		t.Fatalf("patch_edit schema required type = %T", newPatchEditSchema()["required"])
	}
	if !reflect.DeepEqual(required, []string{"action"}) {
		t.Fatalf("edit schema required = %#v, want [action]", required)
	}
}

func TestStructureSchemaHidesLegacyPathAlias(t *testing.T) {
	props, ok := newLSPStructureSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", newLSPStructureSchema()["properties"])
	}
	if _, ok := props["path"]; ok {
		t.Fatalf("structure schema exposes legacy path alias")
	}
}

func TestFileSchemaExposesLanguageIDOverride(t *testing.T) {
	props, ok := newLSPFileSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("file schema properties type = %T", newLSPFileSchema()["properties"])
	}
	if _, ok := props["language_id"]; !ok {
		t.Fatalf("file schema missing language_id override used by handler")
	}
}

func TestStructureSchemaActionEnumMatchesHandlerActions(t *testing.T) {
	props, ok := newLSPStructureSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", newLSPStructureSchema()["properties"])
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("structure action schema type = %T", props["action"])
	}
	values, ok := action["enum"].([]string)
	if !ok {
		t.Fatalf("structure action enum type = %T", action["enum"])
	}
	want := []string{"document_symbol", "workspace_symbol", "folding_range", "semantic_tokens"}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("structure action enum = %#v, want %#v", values, want)
	}
	if _, ok := props["language_id"]; !ok {
		t.Fatalf("structure schema missing language_id override used by handler")
	}
}

func TestStructureSchemaCoversHandlerParameterFields(t *testing.T) {
	producerFields := structureParameterJSONFields(t)
	props, ok := newLSPStructureSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", newLSPStructureSchema()["properties"])
	}
	consumerFields := make([]string, 0, len(props))
	for field := range props {
		if field != "work_dir" { // work_dir is consumed by the common scoped transport before handler decoding.
			consumerFields = append(consumerFields, field)
		}
	}
	sort.Strings(producerFields)
	sort.Strings(consumerFields)
	if !reflect.DeepEqual(producerFields, consumerFields) {
		t.Fatalf("structure parameter/schema fields drifted: producer=%v consumer=%v", producerFields, consumerFields)
	}
}

func TestPatchEditSchemaCoversHandlerParameterFields(t *testing.T) {
	producerFields := namedStructJSONFields(t, filepath.Join("tools", "tool_edit.go"), "EditRequest")
	producerFields = removeReasonedSchemaExemptions(t, producerFields, map[string]string{
		"version": "internal LSP document version used by direct handler tests; intentionally hidden from the public tool schema",
	})
	props, ok := newPatchEditSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("patch_edit schema properties type = %T", newPatchEditSchema()["properties"])
	}
	consumerFields := make([]string, 0, len(props))
	for field := range props {
		if field != "work_dir" {
			consumerFields = append(consumerFields, field)
		}
	}
	sort.Strings(producerFields)
	sort.Strings(consumerFields)
	if !reflect.DeepEqual(producerFields, consumerFields) {
		t.Fatalf("patch_edit parameter/schema fields drifted: producer=%v consumer=%v", producerFields, consumerFields)
	}
}

func structureParameterJSONFields(t *testing.T) []string {
	t.Helper()
	return namedStructJSONFields(t, filepath.Join("tools", "tool_structure.go"), "structureParams")
}

func namedStructJSONFields(t *testing.T, path string, name string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	structType := namedStructType(t, parsed, name)
	fields := make([]string, 0, len(structType.Fields.List))
	for _, field := range structType.Fields.List {
		if field.Tag == nil {
			t.Fatalf("structureParams field without json tag at %s", parsed.Name.Name)
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			t.Fatalf("decode structureParams field tag %s: %v", field.Tag.Value, err)
		}
		name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("structureParams field has invalid json tag %q", tag)
		}
		fields = append(fields, name)
	}
	return fields
}

func removeReasonedSchemaExemptions(t *testing.T, fields []string, exemptions map[string]string) []string {
	t.Helper()
	remaining := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(exemptions))
	for _, field := range fields {
		reason, exempt := exemptions[field]
		if !exempt {
			remaining = append(remaining, field)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("schema field exemption %q has empty reason", field)
		}
		seen[field] = struct{}{}
	}
	for field := range exemptions {
		if _, ok := seen[field]; !ok {
			t.Fatalf("stale schema field exemption %q", field)
		}
	}
	return remaining
}

func namedStructType(t *testing.T, parsed *ast.File, name string) *ast.StructType {
	t.Helper()
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != name {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is %T, want struct", name, typeSpec.Type)
			}
			return structType
		}
	}
	t.Fatalf("type %s not found", name)
	return nil
}

func TestGrepSchemaDocumentsSmartCaseOverride(t *testing.T) {
	props, ok := newLSPGrepSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", newLSPGrepSchema()["properties"])
	}
	caseSensitive, ok := props["case_sensitive"].(map[string]any)
	if !ok {
		t.Fatalf("grep case_sensitive schema type = %T", props["case_sensitive"])
	}
	if got := caseSensitive["description"]; got != "Override smart-case (default: sensitive when query has uppercase, insensitive otherwise)" {
		t.Fatalf("grep case_sensitive description = %q", got)
	}
}

func TestGrepSchemaUsesCanonicalPathsOnly(t *testing.T) {
	props, ok := newLSPGrepSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", newLSPGrepSchema()["properties"])
	}
	paths, ok := props["paths"].(map[string]any)
	if !ok || paths["type"] != "array" {
		t.Fatalf("grep paths schema = %#v, want array", props["paths"])
	}
	for _, removed := range []string{"path", "file_paths"} {
		if _, ok := props[removed]; ok {
			t.Fatalf("grep schema exposes removed field %q", removed)
		}
	}
}

func TestGrepCanonicalPathsSourceGuard(t *testing.T) {
	targets := map[string][]string{
		filepath.Join("tools", "tool_grep.go"): {
			`json:"path"`, `json:"file_paths`, "decodeGrepPath", "grepPathInput",
			"UnmarshalJSON",
		},
		filepath.Join("tools", "factory_bindings.go"): {
			"case *grepToolInput", `allowed["file_paths"]`,
		},
		filepath.Join("tools", "tool_grep_log.go"): {
			`"path", input.Path`,
		},
		"tools.go": {
			"action=text_search", " path=internal",
		},
		filepath.Join("middleware", "budget_hints.go"): {
			"action=text_search", " path=<path>",
		},
	}
	for path, forbidden := range targets {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(source), fragment) {
				t.Fatalf("%s retains removed grep compatibility fragment %q", path, fragment)
			}
		}
	}
}

func TestLSPToolSchemasRejectActionSpecificInvalidArguments(t *testing.T) {
	tests := []struct {
		name       string
		toolSchema schema
		arguments  map[string]any
	}{
		{name: "file/open_file missing file_path", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "open_file"}},
		{name: "file/read_file missing locator", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "read_file"}},
		{name: "file/read_file conflicting locators", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "read_file", "pos": "a.go:1", "file_paths": []any{"a.go"}}},
		{name: "file/diagnostics missing locator", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "diagnostics"}},
		{name: "file/diagnostics conflicting locators", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "diagnostics", "file_path": "a.go", "file_paths": []any{"a.go"}}},
		{name: "inspect missing pos", toolSchema: newLSPInspectSchema(), arguments: map[string]any{"action": "definition"}},
		{name: "xref call hierarchy wrong direction", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "call_hierarchy", "pos": "a.go:1:1", "direction": "supertypes"}},
		{name: "xref type hierarchy wrong direction", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "type_hierarchy", "pos": "a.go:1:1", "direction": "incoming"}},
		{name: "xref references rejects direction", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "references", "pos": "a.go:1:1", "direction": "both"}},
		{name: "xref hierarchy rejects include_declaration", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "call_hierarchy", "pos": "a.go:1:1", "include_declaration": true}},
		{name: "grep missing query", toolSchema: newLSPGrepSchema(), arguments: map[string]any{"action": "text_search", "paths": []any{"internal"}}},
		{name: "grep ast missing query", toolSchema: newLSPGrepSchema(), arguments: map[string]any{"action": "ast_search", "paths": []any{"internal"}}},
		{name: "grep text rejects ast language", toolSchema: newLSPGrepSchema(), arguments: map[string]any{"action": "text_search", "query": "needle", "ast_language": "go"}},
		{name: "grep ast rejects regex", toolSchema: newLSPGrepSchema(), arguments: map[string]any{"action": "ast_search", "query": "func $F()", "regex": true}},
		{name: "structure/document_symbol missing file_path", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "document_symbol"}},
		{name: "structure/workspace_symbol missing query", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "workspace_language": "go"}},
		{name: "structure/workspace_symbol missing locator", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler"}},
		{name: "structure/workspace_symbol conflicting locators", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "file_path": "a.go", "workspace_language": "go"}},
		{name: "structure/workspace_symbol workspace language rejects override", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "workspace_language": "go", "language_id": "go"}},
		{name: "patch_edit/replace_range missing patch", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "replace_range", "file_path": "a.go"}},
		{name: "patch_edit/rename missing new_name", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "rename", "pos": "a.go:1:1"}},
		{name: "patch_edit/code_action missing pos", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "code_action"}},
		{name: "patch_edit/format missing file_path", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "format"}},
		{name: "completion missing pos", toolSchema: newLSPCompletionSchema(), arguments: map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateToolArguments(t, test.toolSchema, test.arguments); err == nil {
				t.Fatalf("schema accepted invalid arguments %#v", test.arguments)
			}
		})
	}
}

func TestLSPToolSchemasAcceptCanonicalActionArguments(t *testing.T) {
	tests := []struct {
		name       string
		toolSchema schema
		arguments  map[string]any
	}{
		{name: "file/open_file", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "open_file", "file_path": "a.go"}},
		{name: "file/read_file", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "read_file", "pos": "a.go:1"}},
		{name: "file/diagnostics", toolSchema: newLSPFileSchema(), arguments: map[string]any{"action": "diagnostics", "file_path": "a.go"}},
		{name: "inspect/hover", toolSchema: newLSPInspectSchema(), arguments: map[string]any{"action": "hover", "pos": "a.go:1:1"}},
		{name: "inspect/definition", toolSchema: newLSPInspectSchema(), arguments: map[string]any{"action": "definition", "pos": "a.go:1:1"}},
		{name: "inspect/implementation", toolSchema: newLSPInspectSchema(), arguments: map[string]any{"action": "implementation", "pos": "a.go:1:1"}},
		{name: "inspect/type_definition", toolSchema: newLSPInspectSchema(), arguments: map[string]any{"action": "type_definition", "pos": "a.go:1:1"}},
		{name: "inspect/signature_help", toolSchema: newLSPInspectSchema(), arguments: map[string]any{"action": "signature_help", "pos": "a.go:1:1"}},
		{name: "xref/references", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "references", "pos": "a.go:1:1"}},
		{name: "xref/call_hierarchy", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "call_hierarchy", "pos": "a.go:1:1", "direction": "both"}},
		{name: "xref/type_hierarchy", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "type_hierarchy", "pos": "a.go:1:1", "direction": "both"}},
		{name: "grep/text_search", toolSchema: newLSPGrepSchema(), arguments: map[string]any{"action": "text_search", "query": "needle", "paths": []any{"internal"}, "glob": "*.go"}},
		{name: "grep/ast_search inferred language", toolSchema: newLSPGrepSchema(), arguments: map[string]any{"action": "ast_search", "query": "func $F()", "paths": []any{"internal"}, "glob": "*.go"}},
		{name: "structure/document_symbol", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "document_symbol", "file_path": "a.go"}},
		{name: "structure/workspace_symbol", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "workspace_language": "go"}},
		{name: "structure/folding_range", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "folding_range", "file_path": "a.go"}},
		{name: "structure/semantic_tokens", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "semantic_tokens", "file_path": "a.go"}},
		{name: "patch_edit/replace_range", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "replace_range", "file_path": "a.go", "patch": "@@"}},
		{name: "patch_edit/rename", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "rename", "pos": "a.go:1:1", "new_name": "renamed"}},
		{name: "patch_edit/code_action", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "code_action", "pos": "a.go:1:1", "only": []any{"quickfix"}}},
		{name: "patch_edit/format", toolSchema: newPatchEditSchema(), arguments: map[string]any{"action": "format", "file_path": "a.go"}},
		{name: "completion", toolSchema: newLSPCompletionSchema(), arguments: map[string]any{"pos": "a.go:1:1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateToolArguments(t, test.toolSchema, test.arguments); err != nil {
				t.Fatalf("schema rejected canonical arguments %#v: %v", test.arguments, err)
			}
		})
	}
}

func validateToolArguments(t *testing.T, toolSchema schema, arguments map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(toolSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("normalize schema JSON: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", document); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return compiled.Validate(arguments)
}

type lspSchemaContract struct {
	toolSchema         schema
	path               string
	typeName           string
	actionFunc         string
	conditionalActions []string
	exemptions         map[string]string
}

func TestLSPToolSchemaFieldsTrackProducerDTOs(t *testing.T) {
	for name, contract := range lspSchemaContracts() {
		t.Run(name, func(t *testing.T) {
			producer := recursiveNamedStructJSONFields(t, contract.path, contract.typeName)
			producer = removeReasonedSchemaExemptions(t, producer, contract.exemptions)
			consumer := schemaPropertyNames(t, contract.toolSchema)
			consumer = removeReasonedSchemaExemptions(t, consumer, map[string]string{
				"work_dir": "common MCP transport consumes the trusted workspace root before handler DTO decoding",
			})
			sort.Strings(producer)
			sort.Strings(consumer)
			if !reflect.DeepEqual(producer, consumer) {
				t.Fatalf("parameter/schema fields drifted: producer=%v consumer=%v", producer, consumer)
			}
		})
	}
}

func TestLSPToolSchemaConditionalsTrackHandlerActions(t *testing.T) {
	for name, contract := range lspSchemaContracts() {
		if contract.actionFunc == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			handlerActions := handlerActionNames(t, contract.path, contract.actionFunc)
			schemaActions := schemaActionEnum(t, contract.toolSchema)
			conditionalActions := schemaConditionalActions(t, contract.toolSchema)
			if !reflect.DeepEqual(handlerActions, schemaActions) {
				t.Fatalf("handler/schema actions drifted: handler=%v schema=%v", handlerActions, schemaActions)
			}
			expectedConditionals := sortedStrings(contract.conditionalActions)
			if !reflect.DeepEqual(expectedConditionals, conditionalActions) {
				t.Fatalf("action-specific conditionals drifted: expected=%v actual=%v", expectedConditionals, conditionalActions)
			}
		})
	}
}

func lspSchemaContracts() map[string]lspSchemaContract {
	return map[string]lspSchemaContract{
		"file": {
			toolSchema: newLSPFileSchema(), path: filepath.Join("tools", "tool_file.go"),
			typeName: "fileToolInput", actionFunc: "handleFile",
			conditionalActions: []string{"open_file", "read_file", "diagnostics"},
			exemptions: map[string]string{
				"expand_comments": "legacy handler-only no-op retained outside this schema task",
			},
		},
		"inspect": {
			toolSchema: newLSPInspectSchema(), path: filepath.Join("tools", "tool_inspect.go"),
			typeName: "inspectParams", actionFunc: "NewInspectHandler",
		},
		"xref": {
			toolSchema: newLSPXrefSchema(), path: filepath.Join("tools", "tool_xref.go"),
			typeName: "xrefParams", actionFunc: "NewXRefHandler",
			conditionalActions: []string{"references", "call_hierarchy", "type_hierarchy"},
		},
		"grep": {
			toolSchema: newLSPGrepSchema(), path: filepath.Join("tools", "tool_grep.go"),
			typeName: "grepToolInput", actionFunc: "handleGrep",
			conditionalActions: []string{"text_search", "ast_search"},
		},
		"structure": {
			toolSchema: newLSPStructureSchema(), path: filepath.Join("tools", "tool_structure.go"),
			typeName: "structureParams", actionFunc: "NewStructureHandler",
			conditionalActions: []string{"document_symbol", "workspace_symbol", "folding_range", "semantic_tokens"},
		},
		"patch_edit": {
			toolSchema: newPatchEditSchema(), path: filepath.Join("tools", "tool_edit.go"),
			typeName: "EditRequest", actionFunc: "Handle",
			conditionalActions: []string{"replace_range", "rename", "code_action", "format"},
			exemptions: map[string]string{
				"version": "internal LSP document version used by direct handler tests",
			},
		},
		"completion": {
			toolSchema: newLSPCompletionSchema(), path: filepath.Join("tools", "tool_completion.go"),
			typeName: "completionParams",
		},
	}
}

func recursiveNamedStructJSONFields(t *testing.T, path, typeName string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	types := namedStructTypes(parsed)
	fields := make([]string, 0)
	visited := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		structType, ok := types[name]
		if !ok {
			t.Fatalf("type %s not found in %s", name, path)
		}
		for _, field := range structType.Fields.List {
			if field.Tag == nil && len(field.Names) == 0 {
				embedded, ok := field.Type.(*ast.Ident)
				if !ok {
					t.Fatalf("%s embeds unsupported field type %T", name, field.Type)
				}
				visit(embedded.Name)
				continue
			}
			jsonName := structFieldJSONName(t, name, field)
			if jsonName != "-" {
				fields = append(fields, jsonName)
			}
		}
	}
	visit(typeName)
	return fields
}

func namedStructTypes(parsed *ast.File) map[string]*ast.StructType {
	types := make(map[string]*ast.StructType)
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if structType, ok := typeSpec.Type.(*ast.StructType); ok {
				types[typeSpec.Name.Name] = structType
			}
		}
	}
	return types
}

func structFieldJSONName(t *testing.T, typeName string, field *ast.Field) string {
	t.Helper()
	if field.Tag == nil {
		t.Fatalf("%s field without json tag", typeName)
	}
	tag, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		t.Fatalf("decode %s field tag %s: %v", typeName, field.Tag.Value, err)
	}
	name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
	if name == "" {
		t.Fatalf("%s field has empty json tag %q", typeName, tag)
	}
	return name
}

func schemaPropertyNames(t *testing.T, toolSchema schema) []string {
	t.Helper()
	props, ok := toolSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties type = %T", toolSchema["properties"])
	}
	fields := make([]string, 0, len(props))
	for name := range props {
		fields = append(fields, name)
	}
	return fields
}

func handlerActionNames(t *testing.T, path, functionName string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	function := namedFunction(t, parsed, functionName)
	actions := make(map[string]struct{})
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.KeyValueExpr:
			if _, ok := value.Value.(*ast.FuncLit); ok {
				addStringLiteral(actions, value.Key)
			}
		case *ast.CaseClause:
			for _, expression := range value.List {
				addStringLiteral(actions, expression)
			}
		}
		return true
	})
	return sortedSet(actions)
}

func namedFunction(t *testing.T, parsed *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func addStringLiteral(values map[string]struct{}, expression ast.Expr) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(literal.Value)
	if err == nil {
		values[value] = struct{}{}
	}
}

func schemaActionEnum(t *testing.T, toolSchema schema) []string {
	t.Helper()
	props, ok := toolSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties type = %T", toolSchema["properties"])
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("action schema type = %T", props["action"])
	}
	values, ok := action["enum"].([]string)
	if !ok {
		t.Fatalf("action enum type = %T", action["enum"])
	}
	return sortedStrings(values)
}

func schemaConditionalActions(t *testing.T, toolSchema schema) []string {
	t.Helper()
	if toolSchema["allOf"] == nil {
		return nil
	}
	conditions, ok := toolSchema["allOf"].([]any)
	if !ok {
		t.Fatalf("schema allOf type = %T", toolSchema["allOf"])
	}
	actions := make(map[string]struct{}, len(conditions))
	for _, rawCondition := range conditions {
		value := conditionalActionName(t, rawCondition)
		if _, exists := actions[value]; exists {
			t.Fatalf("duplicate conditional action %q", value)
		}
		actions[value] = struct{}{}
	}
	return sortedSet(actions)
}

func conditionalActionName(t *testing.T, rawCondition any) string {
	t.Helper()
	condition, ok := rawCondition.(map[string]any)
	if !ok {
		t.Fatalf("conditional type = %T", rawCondition)
	}
	ifSchema, ok := condition["if"].(map[string]any)
	if !ok {
		t.Fatalf("conditional if type = %T", condition["if"])
	}
	props, ok := ifSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("conditional properties type = %T", ifSchema["properties"])
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("conditional action type = %T", props["action"])
	}
	value, ok := action["const"].(string)
	if !ok || strings.TrimSpace(value) == "" {
		t.Fatalf("conditional action const = %#v", action["const"])
	}
	return value
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
