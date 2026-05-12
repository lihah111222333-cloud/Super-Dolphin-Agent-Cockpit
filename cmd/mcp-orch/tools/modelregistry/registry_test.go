package modelregistry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileRegistryLoadsYAML(t *testing.T) {
	path := writeModelsFile(t, `providers:
  - provider: claude
    models:
      - opus
      - sonnet
  - provider: codex
    models:
      - gpt-5
`)
	registry, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("NewFileRegistry() error = %v", err)
	}
	providers := registry.ListProviders()
	if len(providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(providers))
	}
	if providers[0].Provider != "claude" || providers[0].Models[1] != "sonnet" {
		t.Fatalf("claude provider = %+v", providers[0])
	}
}

func TestFileRegistryLookupProvider(t *testing.T) {
	path := writeModelsFile(t, `providers:
  - provider: codex
    models:
      - gpt-5
      - o3
`)
	registry, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("NewFileRegistry() error = %v", err)
	}
	got, ok := registry.LookupProvider("codex")
	if !ok {
		t.Fatal("LookupProvider(codex) ok = false")
	}
	if len(got.Models) != 2 || got.Models[1] != "o3" {
		t.Fatalf("LookupProvider(codex) = %+v", got)
	}
	if _, ok := registry.LookupProvider("claude"); ok {
		t.Fatal("LookupProvider(claude) ok = true, want false")
	}
}

func TestFileRegistryReloadsYAMLChanges(t *testing.T) {
	path := writeModelsFile(t, `providers:
  - provider: codex
    models:
      - gpt-5
`)
	registry, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("NewFileRegistry() error = %v", err)
	}
	writeModelsPath(t, path, `providers:
  - provider: codex
    models:
      - gpt-5
      - gpt-5-codex
  - provider: claude
    models:
      - opus
`)
	providers := registry.ListProviders()
	if len(providers) != 2 {
		t.Fatalf("providers count after reload = %d, want 2", len(providers))
	}
	codex, ok := registry.LookupProvider("codex")
	if !ok {
		t.Fatal("LookupProvider(codex) after reload ok = false")
	}
	if len(codex.Models) != 2 || codex.Models[1] != "gpt-5-codex" {
		t.Fatalf("codex after reload = %+v", codex)
	}
}

func writeModelsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	writeModelsPath(t, path, content)
	return path
}

func writeModelsPath(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
