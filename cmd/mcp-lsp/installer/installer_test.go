package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitPathResolversRejectExistingRelativeFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"primary-lsp", "companion-lsp"} {
		if err := os.WriteFile(name, []byte("binary"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	t.Run("installed binary", func(t *testing.T) {
		cfg := InstallerConfig{
			BinaryName: "primary-lsp",
			InstalledBinaryPathResolver: func(context.Context) (string, error) {
				return "primary-lsp", nil
			},
		}
		candidates, err := installedBinaryCandidates(context.Background(), cfg)
		if err != nil {
			t.Fatalf("installedBinaryCandidates() error = %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("installedBinaryCandidates() returned %d candidates, want 1", len(candidates))
		}
		err = validateBinaryReadinessWithExplicitPath(context.Background(), candidates[0].path, cfg, candidates[0].explicit)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "absolute") {
			t.Fatalf("relative InstalledBinaryPathResolver path error = %v, want absolute-path rejection", err)
		}
	})

	t.Run("required companion", func(t *testing.T) {
		cfg := InstallerConfig{RequiredBinaries: []RequiredBinary{{
			Name: "companion-lsp",
			PathResolver: func(context.Context) (string, error) {
				return "companion-lsp", nil
			},
		}}}
		err := validateRequiredBinaries(context.Background(), cfg)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "absolute") {
			t.Fatalf("relative RequiredBinary.PathResolver path error = %v, want absolute-path rejection", err)
		}
	})
}

func TestEnsureInstalledManagedInstallerHonorsCheckOnlyAndConfigExclusivity(t *testing.T) {
	called := false
	p := NewProvider()
	p.Register("managed", InstallerConfig{
		BinaryName:          "missing-managed-lsp",
		AllowInstallCommand: true,
		ManagedBinaryPath:   filepath.Join(t.TempDir(), "missing-managed-lsp"),
		ManagedInstall: func(context.Context) (string, error) {
			called = true
			return "", errors.New("must not run")
		},
	})
	ctx := WithToolCallInstallCheckOnly(WithInstallCommandCapability(context.Background()))
	if _, err := p.EnsureInstalledDetailed(ctx, "managed"); err == nil {
		t.Fatal("check-only managed install returned nil error")
	}
	if called {
		t.Fatal("check-only tool call executed managed installer")
	}

	p.Register("invalid", InstallerConfig{
		BinaryName:          "invalid-lsp",
		InstallCmd:          "global-installer",
		AllowInstallCommand: true,
		ManagedBinaryPath:   filepath.Join(t.TempDir(), "invalid-lsp"),
		ManagedInstall: func(context.Context) (string, error) {
			return "", nil
		},
	})
	if _, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "invalid"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("invalid dual installer error = %v, want mutual-exclusion failure", err)
	}
}

func TestConfigForLanguageReturnsDetachedSlices(t *testing.T) {
	p := NewProvider()
	p.Register("detached", InstallerConfig{
		BinaryName:      "detached-lsp",
		BinaryCheckArgs: []string{"--version"},
		InstallCmd:      "npm",
		InstallArgs:     []string{"install", "-g", "detached-lsp"},
		RequiredBinaries: []RequiredBinary{{
			Name:      "companion",
			CheckArgs: []string{"--check"},
		}},
	})

	first, ok := p.ConfigForLanguage("detached")
	if !ok {
		t.Fatal("ConfigForLanguage returned no registered configuration")
	}
	first.BinaryCheckArgs[0] = "mutated"
	first.InstallArgs[0] = "mutated"
	first.RequiredBinaries[0].CheckArgs[0] = "mutated"

	second, ok := p.ConfigForLanguage("detached")
	if !ok {
		t.Fatal("ConfigForLanguage lost registered configuration")
	}
	if second.BinaryCheckArgs[0] != "--version" || second.InstallArgs[0] != "install" || second.RequiredBinaries[0].CheckArgs[0] != "--check" {
		t.Fatalf("ConfigForLanguage leaked mutable slices: %#v", second)
	}
}
