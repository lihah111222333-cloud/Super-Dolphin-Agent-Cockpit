package codexapp

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestCodexToolSurfaceScopeAddsMCPConfigHTTPBinaries(t *testing.T) {
	workDir := t.TempDir()
	scope, err := (&driver{}).codexToolSurfaceScope("agent-1", "", "", workDir, map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"my-search": map[string]any{
					"trustedServerId": "my-search",
					"transport":       "http",
					"url":             "https://example.test/mcp",
					"headers": map[string]any{
						"Authorization": "Bearer token",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("codexToolSurfaceScope() error = %v", err)
	}

	assertSurfaceManifestHTTPBinary(t, scope.Manifest, "my-search", "https://example.test/mcp", "Bearer token")
}

func assertSurfaceManifestHTTPBinary(t *testing.T, manifest dto.MCPManifest, name, url, authorization string) {
	t.Helper()
	for _, binary := range manifest.Binaries {
		if binary.Name != name {
			continue
		}
		if binary.Type != "http" || binary.URL != url || binary.Headers["Authorization"] != authorization {
			t.Fatalf("surface manifest binary %q = %#v, want HTTP url/header", name, binary)
		}
		return
	}
	t.Fatalf("surface manifest = %#v, want HTTP binary %q", manifest, name)
}

func TestCodexToolSurfaceScopeRejectsMalformedMCPConfig(t *testing.T) {
	_, err := (&driver{}).codexToolSurfaceScope("agent-1", "", "", t.TempDir(), map[string]any{
		"mcpConfig": map[string]any{},
	})
	if err == nil {
		t.Fatal("codexToolSurfaceScope() error = nil, want malformed mcpConfig failure")
	}
}
