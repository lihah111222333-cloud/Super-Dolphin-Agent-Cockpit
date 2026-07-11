package thread

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	legacyDefaultCodexHomeEnvVar  = "CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME"
	legacyDefaultCodexHomeEnabled = "1"
	packagedCodexIdentityEnvVar   = "SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY"
)

func mustInjectDefaultCodexIdentityForStart(t *testing.T, req StartRequest) StartRequest {
	t.Helper()
	got, err := (&service{}).injectDefaultCodexIdentityForStart(req)
	if err != nil {
		t.Fatalf("injectDefaultCodexIdentityForStart() error = %v", err)
	}
	return got
}

func mustInjectDefaultCodexIdentityForResume(t *testing.T, req ResumeRequest) ResumeRequest {
	t.Helper()
	got, err := (&service{}).injectDefaultCodexIdentityForResume(req)
	if err != nil {
		t.Fatalf("injectDefaultCodexIdentityForResume() error = %v", err)
	}
	return got
}

func TestInjectDefaultCodexIdentityForStartIgnoresLegacyCodexHomeOptIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(legacyDefaultCodexHomeEnvVar, legacyDefaultCodexHomeEnabled)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex"})
	if got.Config != nil {
		t.Fatalf("legacy default identity = %#v, want nil without packaged runtime capability", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartUsesPackagedCodexHome(t *testing.T) {
	superHome := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", superHome)
	if err := os.MkdirAll(filepath.Join(superHome, "providers", "codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll app-managed codex home: %v", err)
	}
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")
	t.Setenv(packagedCodexIdentityEnvVar, legacyDefaultCodexHomeEnabled)

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex"})
	wantHome, err := contract.CanonicalizeCodexHome(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome() error = %v", err)
	}
	if got.Config["codexHome"] != wantHome ||
		got.Config["codexInstanceKey"] != defaultCodexInstanceKey ||
		got.Config["codexModelProvider"] != defaultCodexModelProvider {
		t.Fatalf("packaged default identity = %#v, want home/key/provider", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartCompletesPackagedPartialIdentity(t *testing.T) {
	superHome := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", superHome)
	if err := os.MkdirAll(filepath.Join(superHome, "providers", "codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll app-managed codex home: %v", err)
	}
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")
	t.Setenv(packagedCodexIdentityEnvVar, legacyDefaultCodexHomeEnabled)
	cfg := map[string]any{
		"codexInstanceKey":   defaultCodexInstanceKey,
		"codexModelProvider": defaultCodexModelProvider,
	}

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex", Config: cfg})
	wantHome, err := contract.CanonicalizeCodexHome(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome() error = %v", err)
	}
	if got.Config["codexHome"] != wantHome ||
		got.Config["codexInstanceKey"] != defaultCodexInstanceKey ||
		got.Config["codexModelProvider"] != defaultCodexModelProvider {
		t.Fatalf("packaged partial identity = %#v, want completed identity", got.Config)
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

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex", Config: cfg})
	if got.Config["codexInstanceKey"] != "glm" || got.Config["codexModelProvider"] != "glm-compat" {
		t.Fatalf("explicit identity overwritten: %#v", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartRequiresLegacyOptIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")
	t.Setenv(packagedCodexIdentityEnvVar, "")

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex"})
	if got.Config != nil {
		t.Fatalf("default identity without opt-in = %#v, want nil", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForResumeRequiresLegacyOptIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")
	t.Setenv(packagedCodexIdentityEnvVar, "")

	got := mustInjectDefaultCodexIdentityForResume(t, ResumeRequest{Provider: "codex"})
	if got.CodexHome != "" || got.CodexInstanceKey != "" || got.CodexModelProvider != "" {
		t.Fatalf("resume identity without opt-in = %#v, want empty identity", got)
	}
}

func TestInjectDefaultCodexIdentityForStartSkipsNonCodex(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "claude"})
	if got.Config != nil {
		t.Fatalf("non-codex config = %#v, want nil", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartDevRuntimeIgnoresPackagedLeftovers(t *testing.T) {
	leftoverHome := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	t.Setenv("CODEX_HOME", leftoverHome)
	t.Setenv(packagedCodexIdentityEnvVar, legacyDefaultCodexHomeEnabled)
	t.Setenv(legacyDefaultCodexHomeEnvVar, legacyDefaultCodexHomeEnabled)

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex"})
	if got.Config != nil {
		t.Fatalf("dev empty codex identity = %#v, want no injected codexHome/instance/modelProvider", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartPackagedRuntimeCompletesAppManagedIdentity(t *testing.T) {
	superHome := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", superHome)
	t.Setenv("CODEX_HOME", "")
	if err := os.MkdirAll(filepath.Join(superHome, "providers", "codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll app-managed codex home: %v", err)
	}
	t.Setenv(packagedCodexIdentityEnvVar, "")
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex"})
	wantHome, err := contract.CanonicalizeCodexHome(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome() error = %v", err)
	}
	if got.Config["codexHome"] != wantHome ||
		got.Config["codexInstanceKey"] != defaultCodexInstanceKey ||
		got.Config["codexModelProvider"] != defaultCodexModelProvider {
		t.Fatalf("packaged runtime identity = %#v, want app-managed home/key/provider", got.Config)
	}
}

func TestInjectDefaultCodexIdentityForStartPackagedRuntimePreservesExplicitIdentity(t *testing.T) {
	explicitHome := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", t.TempDir())
	cfg := map[string]any{
		"codexHome":          explicitHome,
		"codexInstanceKey":   "custom-instance",
		"codexModelProvider": "custom-provider",
	}

	got := mustInjectDefaultCodexIdentityForStart(t, StartRequest{Provider: "codex", Config: cfg})
	if got.Config["codexHome"] != explicitHome || got.Config["codexInstanceKey"] != "custom-instance" || got.Config["codexModelProvider"] != "custom-provider" {
		t.Fatalf("explicit identity overwritten: %#v", got.Config)
	}
}
