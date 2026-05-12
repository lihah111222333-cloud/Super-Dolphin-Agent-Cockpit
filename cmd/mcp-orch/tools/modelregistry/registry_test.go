package modelregistry

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	providers := registry.ListProviders()
	if len(providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(providers))
	}
	if providers[0].Provider != "env-provider" || providers[0].Models[0] != "env-model" {
		t.Fatalf("providers = %+v", providers)
	}
}

func TestFileRegistryLogsReloadErrorAndKeepsProviders(t *testing.T) {
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

	providers := registry.ListProviders()
	if len(providers) != 1 || providers[0].Provider != "codex" {
		t.Fatalf("ListProviders() after corrupt yaml = %+v", providers)
	}
	if !strings.Contains(logs.String(), "model registry reload failed; keeping previous providers") {
		t.Fatalf("ListProviders() logs = %q, want reload warning", logs.String())
	}

	logs.Reset()
	got, ok := registry.LookupProvider("codex")
	if !ok {
		t.Fatal("LookupProvider(codex) after corrupt yaml ok = false")
	}
	if len(got.Models) != 1 || got.Models[0] != "gpt-5" {
		t.Fatalf("LookupProvider(codex) after corrupt yaml = %+v", got)
	}
	if !strings.Contains(logs.String(), "model registry reload failed; keeping previous providers") {
		t.Fatalf("LookupProvider() logs = %q, want reload warning", logs.String())
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
