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
		"patch_edit": "action=replace_range",
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
	for _, must := range []string{"Diagnostics are an action on this tool", "action=diagnostics file_path=internal/foo.go"} {
		if !strings.Contains(descriptions["file"], must) {
			t.Fatalf("file description = %q, want direct diagnostics guidance %q", descriptions["file"], must)
		}
	}
	for _, forbidden := range []string{"exec_command", "npm run lint"} {
		if strings.Contains(descriptions["file"], forbidden) {
			t.Fatalf("file description = %q, want no package-script guidance %q", descriptions["file"], forbidden)
		}
	}
}

func TestLSPFileSchemaExposesDirectDiagnosticsCallShape(t *testing.T) {
	props, ok := lspFileSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("file schema properties type = %T", lspFileSchema["properties"])
	}
	for name, must := range map[string]string{
		"action":     "Use action=diagnostics on this file tool",
		"file_path":  "action=diagnostics file_path=internal/foo.go",
		"file_paths": "action=diagnostics file_paths=",
	} {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("file schema %s property type = %T", name, props[name])
		}
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, must) {
			t.Fatalf("file schema %s description = %q, want %q", name, desc, must)
		}
	}
}

func TestLSPFileSchemaLimitDescriptionMatchesReadFileDefaults(t *testing.T) {
	props, ok := lspFileSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("file schema properties type = %T", lspFileSchema["properties"])
	}
	limit, ok := props["limit"].(map[string]any)
	if !ok {
		t.Fatalf("file schema limit property type = %T", props["limit"])
	}
	desc, _ := limit["description"].(string)
	for _, must := range []string{"default 300 for function mode", "250 for line-window", "cap 2000"} {
		if !strings.Contains(desc, must) {
			t.Fatalf("file schema limit description = %q, want %q", desc, must)
		}
	}
}

func TestLSPStructureSchemaExposesWorkspaceSymbolCallShape(t *testing.T) {
	props, ok := lspStructureSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", lspStructureSchema["properties"])
	}
	for name, must := range map[string]string{
		"file_path": "For workspace_symbol, pass exactly one of file_path or language",
		"query":     "Required for workspace_symbol",
		"language":  "Pass exactly one of language or file_path",
	} {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("structure schema %s property type = %T", name, props[name])
		}
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, must) {
			t.Fatalf("structure schema %s description = %q, want %q", name, desc, must)
		}
	}
}

func TestLSPToolManifestDescriptionsExposeActionVariantsWithoutMisleadingShortcuts(t *testing.T) {
	descriptions := map[string]string{}
	for _, manifest := range lspToolManifests {
		descriptions[manifest.Name] = manifest.Description
	}
	for _, must := range []string{"action=document_symbol file_path=internal/foo.go", "action=workspace_symbol query=Handler language=go"} {
		if !strings.Contains(descriptions["structure"], must) {
			t.Fatalf("structure description = %q, want action variant %q", descriptions["structure"], must)
		}
	}
}

func TestLSPGrepSchemaPrefersPathsArrayForMultipleRoots(t *testing.T) {
	props, ok := lspGrepSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", lspGrepSchema["properties"])
	}
	for name, must := range map[string]string{
		"path":  "for multiple roots prefer paths=",
		"paths": "Prefer this over path when passing more than one root or paths containing spaces",
	} {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("grep schema %s property type = %T", name, props[name])
		}
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, must) {
			t.Fatalf("grep schema %s description = %q, want %q", name, desc, must)
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
		"patch_edit": patchEditSchema,
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
