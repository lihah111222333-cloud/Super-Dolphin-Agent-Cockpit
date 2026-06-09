package codexapp

import (
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
