package main

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPublicToolSchemasAcceptCanonicalArguments(t *testing.T) {
	tests := []struct {
		name       string
		toolSchema schema
		arguments  map[string]any
	}{
		{name: "structure document symbol", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "document_symbol", "file_path": "main.go"}},
		{name: "structure workspace symbol", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "workspace_language": "go"}},
		{name: "xref references", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "references", "pos": "main.go:1:1"}},
		{name: "xref call hierarchy", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "call_hierarchy", "pos": "main.go:1:1", "direction": "both"}},
		{name: "diagnostics single", toolSchema: newLSPDiagnosticsSchema(), arguments: map[string]any{"file_path": "main.go"}},
		{name: "diagnostics batch", toolSchema: newLSPDiagnosticsSchema(), arguments: map[string]any{"file_paths": []any{"main.go", "other.go"}, "language_id": "go"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateToolArguments(t, test.toolSchema, test.arguments); err != nil {
				t.Fatalf("canonical arguments rejected: %v", err)
			}
		})
	}
}

func TestPublicToolSchemasRejectLegacyAndConflictingArguments(t *testing.T) {
	tests := []struct {
		name       string
		toolSchema schema
		arguments  map[string]any
	}{
		{name: "diagnostics rejects legacy action", toolSchema: newLSPDiagnosticsSchema(), arguments: map[string]any{"action": "diagnostics", "file_path": "main.go"}},
		{name: "diagnostics rejects read_file action", toolSchema: newLSPDiagnosticsSchema(), arguments: map[string]any{"action": "read_file", "file_path": "main.go"}},
		{name: "diagnostics requires target", toolSchema: newLSPDiagnosticsSchema(), arguments: map[string]any{}},
		{name: "diagnostics rejects conflicting targets", toolSchema: newLSPDiagnosticsSchema(), arguments: map[string]any{"file_path": "main.go", "file_paths": []any{"other.go"}}},
		{name: "structure rejects workspace language with file", toolSchema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "file_path": "main.go", "workspace_language": "go"}},
		{name: "xref references rejects direction", toolSchema: newLSPXrefSchema(), arguments: map[string]any{"action": "references", "pos": "main.go:1:1", "direction": "both"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateToolArguments(t, test.toolSchema, test.arguments); err == nil {
				t.Fatal("invalid arguments unexpectedly accepted")
			}
		})
	}
}

func TestDiagnosticsSchemaContainsOnlySemanticInputs(t *testing.T) {
	properties, ok := newLSPDiagnosticsSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostics properties type = %T", newLSPDiagnosticsSchema()["properties"])
	}
	for _, want := range []string{"file_path", "file_paths", "language_id", "work_dir"} {
		if _, ok := properties[want]; !ok {
			t.Fatalf("diagnostics schema missing %q", want)
		}
	}
	for _, removed := range []string{"action", "pos", "scope", "limit", "patch", "new_name", "only"} {
		if _, ok := properties[removed]; ok {
			t.Fatalf("diagnostics schema exposes removed field %q", removed)
		}
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
