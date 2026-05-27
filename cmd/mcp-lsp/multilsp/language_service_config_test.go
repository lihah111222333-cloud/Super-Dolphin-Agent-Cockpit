package multilsp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestLanguageAdapterRegistryUsesConfiguredRootMarkers(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, filepath.Join(root, "custom.workspace"), "marker\n")
	writeGenericTestFile(t, target, "export const value = 1\n")

	cfg := contract.LSPConfig{
		ProjectAdapters: map[string]contract.LSPProjectAdapterConfig{
			contract.LSPServiceJSTS: {RootMarkers: []string{"custom.workspace"}},
		},
	}
	registry := NewLanguageAdapterRegistryFromConfig(cfg)

	adapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing configured typescript adapter")
	}
	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "typescript",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if resolved.WorkspaceRoot != root {
		t.Fatalf("configured typescript workspace root = %q, want %q", resolved.WorkspaceRoot, root)
	}
}
