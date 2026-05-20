package modelregistry

import (
	"bytes"
	"log/slog"
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
	providers, err := registry.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
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
	got, ok, err := registry.LookupProvider("codex")
	if err != nil {
		t.Fatalf("LookupProvider(codex) error = %v", err)
	}
	if !ok {
		t.Fatal("LookupProvider(codex) ok = false")
	}
	if len(got.Models) != 2 || got.Models[1] != "o3" {
		t.Fatalf("LookupProvider(codex) = %+v", got)
	}
	if _, ok, err := registry.LookupProvider("claude"); err != nil {
		t.Fatalf("LookupProvider(claude) error = %v", err)
	} else if ok {
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
	providers, err := registry.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers count after reload = %d, want 2", len(providers))
	}
	codex, ok, err := registry.LookupProvider("codex")
	if err != nil {
		t.Fatalf("LookupProvider(codex) error = %v", err)
	}
	if !ok {
		t.Fatal("LookupProvider(codex) after reload ok = false")
	}
	if len(codex.Models) != 2 || codex.Models[1] != "gpt-5-codex" {
		t.Fatalf("codex after reload = %+v", codex)
	}
}

func TestNewDefaultRegistryUsesEnvOverride(t *testing.T) {
	path := writeModelsFile(t, `providers:
  - provider: env-provider
    models:
      - env-model
`)
	t.Setenv(EnvRegistryPath, path)

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	providers, err := registry.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(providers))
	}
	if providers[0].Provider != "env-provider" || providers[0].Models[0] != "env-model" {
		t.Fatalf("providers = %+v", providers)
	}
}

func TestFileRegistryReloadErrorFailsFast(t *testing.T) {
	path := writeModelsFile(t, `providers:
  - provider: codex
    models:
      - gpt-5
`)
	var logs bytes.Buffer
	registry, err := NewFileRegistry(path, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatalf("NewFileRegistry() error = %v", err)
	}
	writeModelsPath(t, path, "providers: [")

	if _, err := registry.ListProviders(); err == nil {
		t.Fatal("ListProviders() error = nil, want corrupt yaml error")
	}
	if logs.String() != "" {
		t.Fatalf("ListProviders() logs = %q, want no stale fallback warning", logs.String())
	}

	logs.Reset()
	if _, _, err := registry.LookupProvider("codex"); err == nil {
		t.Fatal("LookupProvider(codex) error = nil, want corrupt yaml error")
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
