package shared

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCodexIdentityPhase0ContractGolden(t *testing.T) {
	t.Parallel()
	realHome := t.TempDir()
	linkHome := filepath.Join(t.TempDir(), "codex-home-link")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	ident, err := ResolveCodexIdentity(map[string]any{
		codexHomeKey:          linkHome,
		codexInstanceKeyKey:   "primary",
		codexModelProviderKey: "codex",
	})
	if err != nil {
		t.Fatalf("ResolveCodexIdentity error = %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatalf("EvalSymlinks realHome: %v", err)
	}
	if ident.Home != wantHome || ident.InstanceKey != "primary" || ident.ModelProvider != "codex" {
		t.Fatalf("identity = %+v, want canonical home %q + stable tuple", ident, wantHome)
	}
}

func TestResolveCodexIdentityPhase0ContractEnvExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PHASE0_CODEX_HOME", home)
	ident, err := ResolveCodexIdentity(map[string]any{
		codexHomeKey:          "$PHASE0_CODEX_HOME",
		codexInstanceKeyKey:   "primary",
		codexModelProviderKey: "codex",
	})
	if err != nil {
		t.Fatalf("ResolveCodexIdentity error = %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks home: %v", err)
	}
	if ident.Home != wantHome {
		t.Fatalf("Home = %q, want %q", ident.Home, wantHome)
	}
}

func TestResolveCodexIdentityPhase0ContractMissingTupleUsesSentinel(t *testing.T) {
	t.Parallel()
	_, err := ResolveCodexIdentity(map[string]any{codexHomeKey: t.TempDir()})
	if !errors.Is(err, ErrCodexInstanceKeyRequired) {
		t.Fatalf("err = %v, want ErrCodexInstanceKeyRequired", err)
	}
}
