package main

import (
	"strings"
	"testing"
)

func TestLSPToolManifestDescriptionsExposeShortExamples(t *testing.T) {
	want := map[string][]string{
		"file": {
			`{"action":"read_file","pos":"internal/foo.go:42","limit":40}`,
			`{"action":"diagnostics","file_path":"internal/foo.go"}`,
		},
		"inspect": {
			`{"action":"definition","pos":"internal/foo.go:42:9"}`,
		},
		"xref": {
			`{"action":"references","pos":"internal/foo.go:42:9"}`,
		},
		"grep": {
			`{"action":"text_search","query":"targetName","paths":["internal"],"glob":"*.go"}`,
		},
		"structure": {
			`{"action":"document_symbol","file_path":"internal/foo.go"}`,
			`{"action":"workspace_symbol","query":"Handler","workspace_language":"go"}`,
		},
		"patch_edit": {
			`{"action":"format","file_path":"internal/foo.go"}`,
		},
		"completion": {
			`{"pos":"internal/foo.go:42:9"}`,
		},
	}
	for _, manifest := range newLSPToolManifests() {
		examples, ok := want[manifest.Name]
		if !ok {
			continue
		}
		for _, example := range examples {
			if !strings.Contains(manifest.Description, example) {
				t.Fatalf("%s description = %q, want JSON object example %q", manifest.Name, manifest.Description, example)
			}
		}
		if strings.Contains(manifest.Description, "action=") || strings.Contains(manifest.Description, "pos=internal/foo.go") {
			t.Fatalf("%s description retains legacy key=value example: %q", manifest.Name, manifest.Description)
		}
	}
}

func TestLSPToolManifestDescriptionsSeparateDiagnosticsFromPackageScripts(t *testing.T) {
	descriptions := map[string]string{}
	for _, manifest := range newLSPToolManifests() {
		descriptions[manifest.Name] = manifest.Description
	}
	if !strings.Contains(descriptions["file"], "fetch diagnostics") {
		t.Fatalf("file description = %q, want diagnostics access", descriptions["file"])
	}
	for _, must := range []string{"Diagnostics are an action on this tool", `{"action":"diagnostics","file_path":"internal/foo.go"}`} {
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

func TestPatchEditManifestAndSchemaDocumentExactSectionAnchors(t *testing.T) {
	var manifestDescription string
	for _, manifest := range newLSPToolManifests() {
		if manifest.Name == "patch_edit" {
			manifestDescription = manifest.Description
			break
		}
	}
	for _, must := range []string{"multi-section edits", "exact", "read-only anchor"} {
		if !strings.Contains(manifestDescription, must) {
			t.Fatalf("patch_edit manifest description = %q, want %q", manifestDescription, must)
		}
	}

	props, ok := newPatchEditSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("patch_edit schema properties type = %T", newPatchEditSchema()["properties"])
	}
	patch, ok := props["patch"].(map[string]any)
	if !ok {
		t.Fatalf("patch_edit patch schema type = %T", props["patch"])
	}
	description, _ := patch["description"].(string)
	if description != "Patch body for replace_range. Supports multi-section edits." {
		t.Fatalf("patch_edit patch description = %q", description)
	}
}

func TestPatchEditManifestProvidesCopyableReplaceRangeGuidance(t *testing.T) {
	var description string
	for _, manifest := range newLSPToolManifests() {
		if manifest.Name == "patch_edit" {
			description = manifest.Description
			break
		}
	}
	for _, must := range []string{
		`{"action":"replace_range","file_path":"internal/foo.go","patch":"`,
		`"work_dir":"/absolute/workspace"}`,
		"only optional scope field is work_dir",
		"Do not pass an isolated *** End Patch",
		"empty context lines still need a leading space",
	} {
		if !strings.Contains(description, must) {
			t.Fatalf("patch_edit description = %q, want copyable guidance %q", description, must)
		}
	}
}

func TestLSPFileSchemaExposesDirectDiagnosticsCallShape(t *testing.T) {
	props, ok := newLSPFileSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("file schema properties type = %T", newLSPFileSchema()["properties"])
	}
	for name, must := range map[string]string{
		"action":     "Use diagnostics on this file tool",
		"file_path":  "diagnostics",
		"file_paths": "diagnostics",
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
	props, ok := newLSPFileSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("file schema properties type = %T", newLSPFileSchema()["properties"])
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
	props, ok := newLSPStructureSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", newLSPStructureSchema()["properties"])
	}
	for name, must := range map[string]string{
		"file_path":          "For workspace_symbol, pass exactly one of file_path or workspace_language",
		"query":              "Required for workspace_symbol",
		"workspace_language": "Pass exactly one of workspace_language or file_path",
		"match_mode":         "exact is the default",
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
	for _, manifest := range newLSPToolManifests() {
		descriptions[manifest.Name] = manifest.Description
	}
	for _, must := range []string{
		`{"action":"document_symbol","file_path":"internal/foo.go"}`,
		`{"action":"workspace_symbol","query":"Handler","workspace_language":"go"}`,
	} {
		if !strings.Contains(descriptions["structure"], must) {
			t.Fatalf("structure description = %q, want action variant %q", descriptions["structure"], must)
		}
	}
}

func TestLSPGrepSchemaExposesOnlyCanonicalPathsArray(t *testing.T) {
	props, ok := newLSPGrepSchema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("grep schema properties type = %T", newLSPGrepSchema()["properties"])
	}
	paths, ok := props["paths"].(map[string]any)
	if !ok || paths["type"] != "array" {
		t.Fatalf("grep paths schema = %#v, want string array", props["paths"])
	}
	for _, removed := range []string{"path", "file_paths"} {
		if _, ok := props[removed]; ok {
			t.Fatalf("grep schema exposes removed field %q", removed)
		}
	}
}

func TestLSPToolSchemasExposeExplicitWorkDir(t *testing.T) {
	schemas := map[string]schema{
		"file":       newLSPFileSchema(),
		"inspect":    newLSPInspectSchema(),
		"xref":       newLSPXrefSchema(),
		"grep":       newLSPGrepSchema(),
		"structure":  newLSPStructureSchema(),
		"patch_edit": newPatchEditSchema(),
		"completion": newLSPCompletionSchema(),
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
		"inspect":    newLSPInspectSchema(),
		"xref":       newLSPXrefSchema(),
		"completion": newLSPCompletionSchema(),
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
