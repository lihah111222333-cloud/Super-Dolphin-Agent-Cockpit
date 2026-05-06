package thread

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestInjectDefaultCodexIdentityForStartUsesCodexHomeWhenOptedIn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	t.Setenv(legacyDefaultCodexHomeEnvVar, legacyDefaultCodexHomeEnabled)

	got := (&service{}).injectDefaultCodexIdentityForStart(StartRequest{Provider: "codex"})
	wantHome, err := contract.CanonicalizeCodexHome(dir)
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome() error = %v", err)
	}
	if got.Config["codexHome"] != wantHome ||
		got.Config["codexInstanceKey"] != defaultCodexInstanceKey ||
		got.Config["codexModelProvider"] != defaultCodexModelProvider {
		t.Fatalf("default identity = %#v, want home/key/provider", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartPreservesExplicitIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	cfg := map[string]any{
		"codexHome":          dir,
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	}

	got := (&service{}).injectDefaultCodexIdentityForStart(StartRequest{Provider: "codex", Config: cfg})
	if got.Config["codexInstanceKey"] != "glm" || got.Config["codexModelProvider"] != "glm-compat" {
		t.Fatalf("explicit identity overwritten: %#v", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartRequiresLegacyOptIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")

	got := (&service{}).injectDefaultCodexIdentityForStart(StartRequest{Provider: "codex"})
	if got.Config != nil {
		t.Fatalf("default identity without opt-in = %#v, want nil", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForResumeRequiresLegacyOptIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")

	got := (&service{}).injectDefaultCodexIdentityForResume(ResumeRequest{Provider: "codex"})
	if got.CodexHome != "" || got.CodexInstanceKey != "" || got.CodexModelProvider != "" {
		t.Fatalf("resume identity without opt-in = %#v, want empty identity", got)
	}
}

func TestInjectDefaultCodexIdentityForStartSkipsNonCodex(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	got := (&service{}).injectDefaultCodexIdentityForStart(StartRequest{Provider: "claude"})
	if got.Config != nil {
		t.Fatalf("non-codex config = %#v, want nil", got.Config)
	}
}
