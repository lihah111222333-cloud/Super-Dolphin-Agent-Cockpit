package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestEditSchemaExposesPatchDiskFieldsOnly(t *testing.T) {
	props, ok := newPatchEditSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("patch_edit schema properties type = %T", newPatchEditSchema()["properties"])
	}
	for _, field := range []string{"action", "file_path", "patch", "pos", "new_name", "only", "response_detail"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing expected field %q", field)
		}
	}
	for _, field := range []string{"line", "column", "end_line", "end_column", "edits", "new_text", "persist_to_disk", "force", "version"} {
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

func TestGrepSchemaDocumentsMultiPathCompatibility(t *testing.T) {
	props, ok := newLSPGrepSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", newLSPGrepSchema()["properties"])
	}
	path, ok := props["path"].(map[string]any)
	if !ok {
		t.Fatalf("grep path schema type = %T", props["path"])
	}
	if _, ok := path["oneOf"].([]any); !ok {
		t.Fatalf("grep path schema missing string-or-array oneOf: %#v", path)
	}
	for _, field := range []string{"paths", "file_paths"} {
		prop, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("grep %s schema type = %T", field, props[field])
		}
		if prop["type"] != "array" {
			t.Fatalf("grep %s schema type = %q, want array", field, prop["type"])
		}
	}
}
