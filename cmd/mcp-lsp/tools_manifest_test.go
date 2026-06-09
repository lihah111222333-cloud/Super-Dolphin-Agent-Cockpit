package main

import (
	"strings"
	"testing"
)

func TestLSPToolManifestDescriptionsExposeShortExamples(t *testing.T) {
	want := map[string]string{
		"file":       "action=read_file pos=",
		"inspect":    "pos=internal/foo.go:42:9",
		"xref":       "pos=internal/foo.go:42:9",
		"grep":       "action=text_search",
		"structure":  "action=document_symbol",
		"edit":       "action=replace_range",
		"completion": "pos=internal/foo.go:42:9",
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

func TestLSPToolManifestDescriptionsSeparateDiagnosticsFromPackageScripts(t *testing.T) {
	descriptions := map[string]string{}
	for _, manifest := range lspToolManifests {
		descriptions[manifest.Name] = manifest.Description
	}
	if !strings.Contains(descriptions["file"], "fetch diagnostics") {
		t.Fatalf("file description = %q, want diagnostics access", descriptions["file"])
	}
	for _, forbidden := range []string{"exec_command", "npm run lint"} {
		if strings.Contains(descriptions["file"], forbidden) {
			t.Fatalf("file description = %q, want no package-script guidance %q", descriptions["file"], forbidden)
		}
	}
}

func TestLSPToolSchemasExposeExplicitWorkDir(t *testing.T) {
	schemas := map[string]schema{
		"file":       lspFileSchema,
		"inspect":    lspInspectSchema,
		"xref":       lspXrefSchema,
		"grep":       lspGrepSchema,
		"structure":  lspStructureSchema,
		"edit":       lspEditSchema,
		"completion": lspCompletionSchema,
	}
	for name, toolSchema := range schemas {
		props, ok := toolSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema properties type = %T", name, toolSchema["properties"])
		}
		workDir, ok := props["work_dir"].(map[string]any)
		if !ok {
			t.Fatalf("%s work_dir schema missing or wrong type: %T", name, props["work_dir"])
		}
		desc, _ := workDir["description"].(string)
		for _, must := range []string{"Absolute paths", "trusted workspace root", "relative work_dir"} {
			if !strings.Contains(desc, must) {
				t.Fatalf("%s work_dir description = %q, want %q", name, desc, must)
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
