package main

import (
	"strings"
	"testing"
)

func TestLSPToolManifestDescriptionsExposeShortExamples(t *testing.T) {
	want := map[string]string{
		"file":          "Example read_file:",
		"inspect":       "pos=internal/foo.go:42:9",
		"xref":          "pos=internal/foo.go:42:9",
		"grep":          "Example text search:",
		"structure":     "Example document_symbol:",
		"edit":          "Example:",
		"completion":    "pos=internal/foo.go:42:9",
		"code_run":      "Example project_cmd:",
		"code_run_test": "Example:",
	}
	for _, manifest := range lspToolManifests {
		must, ok := want[manifest.Name]
		if !ok {
			continue
		}
		if !strings.Contains(manifest.Description, must) {
			t.Fatalf("%s description = %q, want example marker %q", manifest.Name, manifest.Description, must)
		}
	}
}

func TestLSPToolManifestDescriptionsExposeRecommendationAndReason(t *testing.T) {
	for _, manifest := range lspToolManifests {
		for _, must := range []string{"Recommended tool:", "Why:"} {
			if !strings.Contains(manifest.Description, must) {
				t.Fatalf("%s description = %q, want %q", manifest.Name, manifest.Description, must)
			}
		}
	}
}

func TestLSPToolManifestDescriptionsPromptOpenFileForStatefulActions(t *testing.T) {
	statefulTools := map[string]bool{
		"file":       true,
		"inspect":    true,
		"xref":       true,
		"structure":  true,
		"edit":       true,
		"completion": true,
	}
	for _, manifest := range lspToolManifests {
		if !statefulTools[manifest.Name] {
			continue
		}
		for _, must := range []string{"open_file", "first"} {
			if !strings.Contains(manifest.Description, must) {
				t.Fatalf("%s description = %q, want open_file-first hint containing %q", manifest.Name, manifest.Description, must)
			}
		}
	}
}

func TestPositionSchemasExposeCopyablePosExample(t *testing.T) {
	for name, schema := range map[string]schema{
		"inspect":    lspInspectSchema,
		"xref":       lspXrefSchema,
		"completion": lspCompletionSchema,
	} {
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema properties type = %T", name, schema["properties"])
		}
		pos, ok := props["pos"].(map[string]any)
		if !ok {
			t.Fatalf("%s pos schema type = %T", name, props["pos"])
		}
		desc, _ := pos["description"].(string)
		if !strings.Contains(desc, "internal/foo.go:42:9") {
			t.Fatalf("%s pos description = %q, want copyable example", name, desc)
		}
	}
}
