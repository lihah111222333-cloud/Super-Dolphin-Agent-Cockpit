package main

import (
	"reflect"
	"testing"
)

func TestEditSchemaExposesPatchDiskFieldsOnly(t *testing.T) {
	props, ok := lspEditSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("edit schema properties type = %T", lspEditSchema["properties"])
	}
	for _, field := range []string{"action", "file_path", "patch", "pos", "new_name", "only"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing expected field %q", field)
		}
	}
	for _, field := range []string{"line", "column", "end_line", "end_column", "edits", "new_text", "persist_to_disk", "force", "version"} {
		if _, ok := props[field]; ok {
			t.Fatalf("edit schema exposes removed legacy field %q", field)
		}
	}
	required, ok := lspEditSchema["required"].([]string)
	if !ok {
		t.Fatalf("edit schema required type = %T", lspEditSchema["required"])
	}
	if !reflect.DeepEqual(required, []string{"action"}) {
		t.Fatalf("edit schema required = %#v, want [action]", required)
	}
}

func TestStructureSchemaHidesLegacyPathAlias(t *testing.T) {
	props, ok := lspStructureSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", lspStructureSchema["properties"])
	}
	if _, ok := props["path"]; ok {
		t.Fatalf("structure schema exposes legacy path alias")
	}
}

func TestFileSchemaExposesLanguageIDOverride(t *testing.T) {
	props, ok := lspFileSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("file schema properties type = %T", lspFileSchema["properties"])
	}
	if _, ok := props["language_id"]; !ok {
		t.Fatalf("file schema missing language_id override used by handler")
	}
}

func TestStructureSchemaActionEnumMatchesHandlerActions(t *testing.T) {
	props, ok := lspStructureSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", lspStructureSchema["properties"])
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

func TestGrepSchemaDocumentsSmartCaseOverride(t *testing.T) {
	props, ok := lspGrepSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", lspGrepSchema["properties"])
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
	props, ok := lspGrepSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", lspGrepSchema["properties"])
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
