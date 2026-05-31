package main

import (
	"strings"
	"testing"
)

func TestLSPToolManifestDescriptionsExposeShortExamples(t *testing.T) {
	want := map[string]string{
		"file":          "action=read_file pos=",
		"inspect":       "pos=internal/foo.go:42:9",
		"xref":          "pos=internal/foo.go:42:9",
		"grep":          "action=text_search",
		"structure":     "action=document_symbol",
		"edit":          "Example:",
		"completion":    "pos=internal/foo.go:42:9",
		"code_run":      "mode=project_cmd",
		"code_run_test": "test_func=TestName",
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

// TestLSPToolManifestDescriptionsPromptOpenFileForFileTool keeps the
// explicit "run open_file first" hint on the file tool. We deliberately
// dropped the per-tool repetition for inspect/xref/structure/edit/
// completion in favour of a single hint on file: every stateful action
// either implicitly opens via managerForFile or already routes through
// file.open_file, so repeating the rule on every manifest only burns
// tokens without telling the model anything new.
func TestLSPToolManifestDescriptionsPromptOpenFileForFileTool(t *testing.T) {
	for _, manifest := range lspToolManifests {
		if manifest.Name != "file" {
			continue
		}
		for _, must := range []string{"open_file", "before"} {
			if !strings.Contains(manifest.Description, must) {
				t.Fatalf("file description = %q, want open_file hint containing %q", manifest.Description, must)
			}
		}
	}
}

func TestLSPToolManifestDescriptionsSeparateDiagnosticsFromPackageScripts(t *testing.T) {
	descriptions := map[string]string{}
	for _, manifest := range lspToolManifests {
		descriptions[manifest.Name] = manifest.Description
	}
	for _, must := range []string{"LSP/type diagnostics", "exec_command", "npm run lint"} {
		if !strings.Contains(descriptions["file"], must) {
			t.Fatalf("file description = %q, want %q", descriptions["file"], must)
		}
	}
	for _, must := range []string{"only when no host exec_command is available", "npm run lint", "exec_command exists"} {
		if !strings.Contains(descriptions["code_run"], must) {
			t.Fatalf("code_run description = %q, want %q", descriptions["code_run"], must)
		}
	}
}

func TestCodeRunSchemaMarksProjectCommandAsExecCommandFallback(t *testing.T) {
	props, ok := codeRunSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("code_run schema properties type = %T", codeRunSchema["properties"])
	}
	command, ok := props["command"].(map[string]any)
	if !ok {
		t.Fatalf("code_run command schema type = %T", props["command"])
	}
	desc, _ := command["description"].(string)
	for _, must := range []string{"exec_command", "package scripts", "npm run lint"} {
		if !strings.Contains(desc, must) {
			t.Fatalf("code_run command description = %q, want %q", desc, must)
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
