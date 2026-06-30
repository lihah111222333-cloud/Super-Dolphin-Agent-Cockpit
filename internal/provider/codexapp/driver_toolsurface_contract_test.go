package codexapp

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

func TestCodexToolSurfaceScopeUsesRuntimeConfigAliases(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	extraDir := t.TempDir()
	scope, err := (&driver{}).codexToolSurfaceScope("agent-1", "local-thread-1", "provider-thread-1", workDir, map[string]any{
		"additional_working_directories": []any{extraDir},
		"env": map[string]any{
			"SUPER_DOLPHIN_TEST_FLAG": "1",
		},
		"auto_approve": []any{"mcp__lsp__lsp_grep"},
	})
	if err != nil {
		t.Fatalf("codexToolSurfaceScope() error = %v", err)
	}

	assertCodexToolSurfaceRoots(t, scope.WorkspaceRoots, workDir, extraDir)
	if len(scope.Manifest.Binaries) == 0 {
		t.Fatalf("Manifest.Binaries = %#v, want managed binaries", scope.Manifest.Binaries)
	}
	first := scope.Manifest.Binaries[0]
	if first.Env["SUPER_DOLPHIN_TEST_FLAG"] != "1" {
		t.Fatalf("manifest env = %#v, want SUPER_DOLPHIN_TEST_FLAG", first.Env)
	}
	if got := first.AutoApprove; len(got) != 1 || got[0] != "mcp__lsp__lsp_grep" {
		t.Fatalf("AutoApprove = %#v, want aliased auto approve", got)
	}
	var roots []string
	if err := json.Unmarshal([]byte(first.Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("decode GO_AGENT_LSP_ROOTS: %v", err)
	}
	assertCodexToolSurfaceRoots(t, roots, workDir, extraDir)
}

func TestCodexToolSurfaceScopeCarriesDisallowedTools(t *testing.T) {
	t.Parallel()

	scope, err := (&driver{}).codexToolSurfaceScope("agent-1", "local-thread-1", "provider-thread-1", t.TempDir(), map[string]any{
		"disallowed_tools": "memory_write, launch_agent, connect_tool_source",
	})
	if err != nil {
		t.Fatalf("codexToolSurfaceScope() error = %v", err)
	}

	want := []string{"memory_write", "launch_agent", "connect_tool_source"}
	if !slices.Equal(scope.DisabledTools, want) {
		t.Fatalf("DisabledTools = %#v, want %#v", scope.DisabledTools, want)
	}
}

func TestCodexToolSurfaceScopeRejectsMalformedDisallowedTools(t *testing.T) {
	t.Parallel()

	_, err := (&driver{}).codexToolSurfaceScope("agent-1", "local-thread-1", "provider-thread-1", t.TempDir(), map[string]any{
		"disallowed_tools": map[string]any{"tool": "memory_write"},
	})
	if err == nil {
		t.Fatal("codexToolSurfaceScope() error = nil, want malformed disallowed_tools failure")
	}
}

func assertCodexToolSurfaceRoots(t *testing.T, got []string, workDir, extraDir string) {
	t.Helper()

	want := []string{workDir, extraDir}
	if !maps.Equal(codexToolSurfaceStringSet(got), codexToolSurfaceStringSet(want)) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}

func codexToolSurfaceStringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
