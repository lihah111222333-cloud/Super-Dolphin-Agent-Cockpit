package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestLSPToolManifestIsThreeSemanticToolsOnly(t *testing.T) {
	manifests := newLSPToolManifests()
	got := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		got = append(got, manifest.Name)
	}
	want := []string{"structure", "xref", "diagnostics"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public LSP tools = %#v, want %#v", got, want)
	}
	for _, removed := range []string{"edit", "patch_edit", "file", "read_file", "inspect", "grep", "completion"} {
		for _, name := range got {
			if name == removed {
				t.Fatalf("public LSP tools retain removed tool %q", removed)
			}
		}
	}
}

func TestLSPToolManifestDescriptionsExposeCanonicalExamples(t *testing.T) {
	want := map[string][]string{
		"structure":   {`{"action":"document_symbol","file_path":"internal/foo.go"}`, `{"action":"workspace_symbol","query":"Handler","workspace_language":"go"}`},
		"xref":        {`{"action":"references","pos":"internal/foo.go:42:9"}`},
		"diagnostics": {`{"file_path":"internal/foo.go"}`, `{"file_paths":["internal/foo.go","internal/bar.go"]}`},
	}
	for _, manifest := range newLSPToolManifests() {
		for _, example := range want[manifest.Name] {
			if !strings.Contains(manifest.Description, example) {
				t.Fatalf("%s description = %q, want %q", manifest.Name, manifest.Description, example)
			}
		}
	}
}

func TestLSPToolSchemasExposeExplicitWorkDir(t *testing.T) {
	for name, toolSchema := range map[string]schema{
		"structure":   newLSPStructureSchema(),
		"xref":        newLSPXrefSchema(),
		"diagnostics": newLSPDiagnosticsSchema(),
	} {
		properties, ok := toolSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s properties type = %T", name, toolSchema["properties"])
		}
		workDir, ok := properties["work_dir"].(map[string]any)
		if !ok {
			t.Fatalf("%s work_dir missing", name)
		}
		description, _ := workDir["description"].(string)
		if !strings.Contains(description, "trusted workspace root") {
			t.Fatalf("%s work_dir description = %q", name, description)
		}
	}
}
