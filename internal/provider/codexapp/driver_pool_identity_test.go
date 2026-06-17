package codexapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestWithDefaultCodexIdentityUsesLegacyModelProviderForCanonicalDefault(t *testing.T) {
	t.Parallel()
	got, err := withDefaultCodexIdentity(map[string]any{"modelProvider": " openai "}, t.TempDir(), localCodexModelProvider)
	if err != nil {
		t.Fatalf("withDefaultCodexIdentity returned error: %v", err)
	}
	if got[contract.CodexModelProviderKey] != "openai" {
		t.Fatalf("%s = %q, want openai", contract.CodexModelProviderKey, got[contract.CodexModelProviderKey])
	}
}

func TestWithDefaultCodexIdentityUsesLocalProviderForDevDefaultHome(t *testing.T) {
	t.Parallel()
	got, err := withDefaultCodexIdentity(map[string]any{"modelProvider": " codex "}, t.TempDir(), localCodexModelProvider)
	if err != nil {
		t.Fatalf("withDefaultCodexIdentity returned error: %v", err)
	}
	if got[contract.CodexModelProviderKey] != "openai" {
		t.Fatalf("%s = %q, want openai", contract.CodexModelProviderKey, got[contract.CodexModelProviderKey])
	}
}

func TestWithDefaultCodexIdentityUsesCodexConfigModelProvider(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`model_provider = "winvm-relay"`+"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	got, err := withDefaultCodexIdentity(map[string]any{}, home, localCodexModelProvider)
	if err != nil {
		t.Fatalf("withDefaultCodexIdentity returned error: %v", err)
	}
	if got[contract.CodexModelProviderKey] != "winvm-relay" {
		t.Fatalf("%s = %q, want winvm-relay", contract.CodexModelProviderKey, got[contract.CodexModelProviderKey])
	}
}

func TestWithDefaultCodexIdentityPreservesCanonicalModelProviderBeforeCodexConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = \n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	got, err := withDefaultCodexIdentity(map[string]any{contract.CodexModelProviderKey: "explicit-relay"}, home, localCodexModelProvider)
	if err != nil {
		t.Fatalf("withDefaultCodexIdentity returned error: %v", err)
	}
	if got[contract.CodexModelProviderKey] != "explicit-relay" {
		t.Fatalf("%s = %q, want explicit-relay", contract.CodexModelProviderKey, got[contract.CodexModelProviderKey])
	}
}

func TestWithDefaultCodexIdentityDoesNotReadCodexConfigForRelayFallback(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = \n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	got, err := withDefaultCodexIdentity(map[string]any{}, home, defaultCodexModelProvider)
	if err != nil {
		t.Fatalf("withDefaultCodexIdentity returned error: %v", err)
	}
	if got[contract.CodexModelProviderKey] != defaultCodexModelProvider {
		t.Fatalf("%s = %q, want %s", contract.CodexModelProviderKey, got[contract.CodexModelProviderKey], defaultCodexModelProvider)
	}
}
