package codexapp

import (
	"path/filepath"
	"testing"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestSelectCodexProviderHomeDevEmptyUsesLocalCLIHome(t *testing.T) {
	userHome := t.TempDir()
	superHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	t.Setenv(providershared.PackagedCodexEnv, "1")
	mustCanonicalCodexHome(t, userHome)

	selection, err := selectCodexProviderHome("")
	if err != nil {
		t.Fatalf("selectCodexProviderHome() error = %v", err)
	}
	if selection.useAppManagedHome {
		t.Fatalf("useAppManagedHome = true, want false for dev empty home")
	}
	home, mirrorHome, err := ensureResolvedCodexProviderHome(selection)
	if err != nil {
		t.Fatalf("ensureResolvedCodexProviderHome() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks local codex home: %v", err)
	}
	if home != want || mirrorHome != "" {
		t.Fatalf("home, mirrorHome = %q, %q; want local CLI home %q and empty mirror home", home, mirrorHome, want)
	}
}

func TestSelectCodexProviderHomePackagedEmptyUsesAppManagedHome(t *testing.T) {
	superHome := t.TempDir()
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv(providershared.PackagedCodexEnv, "")

	selection, err := selectCodexProviderHome("")
	if err != nil {
		t.Fatalf("selectCodexProviderHome() error = %v", err)
	}
	if !selection.useAppManagedHome {
		t.Fatalf("useAppManagedHome = false, want true for packaged empty home")
	}
	home, mirrorHome, err := ensureResolvedCodexProviderHome(selection)
	if err != nil {
		t.Fatalf("ensureResolvedCodexProviderHome() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed codex home: %v", err)
	}
	if home != want || mirrorHome != want {
		t.Fatalf("home, mirrorHome = %q, %q; want app-managed %q", home, mirrorHome, want)
	}
}

func TestSelectCodexProviderHomePackagedExplicitAppManagedSelection(t *testing.T) {
	superHome := t.TempDir()
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	appHome := filepath.Join(superHome, "providers", "codex")

	selection, err := selectCodexProviderHome(appHome)
	if err != nil {
		t.Fatalf("selectCodexProviderHome() error = %v", err)
	}
	if !selection.useAppManagedHome {
		t.Fatalf("useAppManagedHome = false, want true for explicit app-managed selection in packaged mode")
	}
}
