package main

import "testing"

func TestPublicToolLanguageParameterSchemaNames(t *testing.T) {
	for name, toolSchema := range map[string]schema{
		"structure":   newLSPStructureSchema(),
		"xref":        newLSPXrefSchema(),
		"diagnostics": newLSPDiagnosticsSchema(),
	} {
		properties, ok := toolSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s properties type = %T", name, toolSchema["properties"])
		}
		if _, ok := properties["language"]; ok {
			t.Fatalf("%s exposes removed language alias", name)
		}
		if _, ok := properties["language_id"]; !ok {
			t.Fatalf("%s missing language_id", name)
		}
	}
	structure := newLSPStructureSchema()["properties"].(map[string]any)
	if _, ok := structure["workspace_language"]; !ok {
		t.Fatal("structure missing workspace_language selector")
	}
	for name, toolSchema := range map[string]schema{"xref": newLSPXrefSchema(), "diagnostics": newLSPDiagnosticsSchema()} {
		properties := toolSchema["properties"].(map[string]any)
		if _, ok := properties["workspace_language"]; ok {
			t.Fatalf("%s unexpectedly exposes workspace_language", name)
		}
	}
}
