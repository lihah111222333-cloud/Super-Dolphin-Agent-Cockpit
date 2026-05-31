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
	for _, field := range []string{"patch", "version"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing runtime field %q", field)
		}
	}
	for _, field := range []string{"action", "line", "column", "end_line", "end_column", "edits", "new_name", "new_text", "only", "persist_to_disk", "force"} {
		if _, ok := props[field]; ok {
			t.Fatalf("edit schema exposes removed non-patch field %q", field)
		}
	}
	required, ok := lspEditSchema["required"].([]string)
	if !ok {
		t.Fatalf("edit schema required type = %T", lspEditSchema["required"])
	}
	if !reflect.DeepEqual(required, []string{"file_path", "patch"}) {
		t.Fatalf("edit schema required = %#v, want file_path and patch", required)
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
