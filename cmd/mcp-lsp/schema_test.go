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
	for _, field := range []string{"action", "file_path", "patch", "version", "pos", "new_name", "only"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing expected field %q", field)
		}
	}
	for _, field := range []string{"line", "column", "end_line", "end_column", "edits", "new_text", "persist_to_disk", "force"} {
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

func TestStructureSchemaExposesLegacyPathAlias(t *testing.T) {
	props, ok := lspStructureSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", lspStructureSchema["properties"])
	}
	if _, ok := props["path"]; !ok {
		t.Fatalf("structure schema missing legacy path alias")
	}
}
