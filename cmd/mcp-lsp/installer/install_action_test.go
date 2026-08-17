package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallActionRejectsCommandFieldsBeforeInvocation(t *testing.T) {
	called := false
	p := NewProvider()
	p.Register("action", InstallerConfig{
		BinaryName: "action-binary",
		InstallCmd: "legacy-command",
		InstallAction: func(context.Context) (InstallResult, error) {
			called = true
			return InstallResult{}, nil
		},
		AllowInstallCommand: true,
	})

	_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "action")
	if err == nil || !strings.Contains(err.Error(), "cannot combine InstallAction with command-based install fields") {
		t.Fatalf("EnsureInstalledDetailed() error = %v, want InstallAction exclusivity error", err)
	}
	if called {
		t.Fatal("InstallAction was invoked despite mutually exclusive command fields")
	}
}

func TestInstallActionRequiresAbsolutePathAndMatchingIdentity(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		p := NewProvider()
		p.Register("relative", InstallerConfig{
			BinaryName: "relative-action-binary",
			InstallAction: func(context.Context) (InstallResult, error) {
				return InstallResult{Path: "relative-binary"}, nil
			},
			AllowInstallCommand: true,
		})

		_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "relative")
		if err == nil || !strings.Contains(err.Error(), "non-absolute binary path") {
			t.Fatalf("EnsureInstalledDetailed() error = %v, want non-absolute path error", err)
		}
	})

	t.Run("mismatched language", func(t *testing.T) {
		binaryPath := filepath.Join(t.TempDir(), "action-binary")
		if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
			t.Fatalf("WriteFile(binary): %v", err)
		}
		p := NewProvider()
		p.Register("expected", InstallerConfig{
			BinaryName: "action-binary",
			InstallAction: func(context.Context) (InstallResult, error) {
				return InstallResult{Path: binaryPath, Lang: "other-language"}, nil
			},
			AllowInstallCommand: true,
		})

		_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "expected")
		if err == nil || !strings.Contains(err.Error(), "returned language") {
			t.Fatalf("EnsureInstalledDetailed() error = %v, want returned language validation error", err)
		}
	})
}

func TestInstallActionHonorsCapabilityCheckOnlyAndTimeout(t *testing.T) {
	t.Run("capability and check-only", func(t *testing.T) {
		binaryPath := filepath.Join(t.TempDir(), "action-binary")
		calls := 0
		p := NewProvider()
		p.Register("capability", InstallerConfig{
			BinaryName: "action-binary",
			InstalledBinaryPathResolver: func(context.Context) (string, error) {
				return binaryPath, nil
			},
			InstallAction: func(context.Context) (InstallResult, error) {
				calls++
				if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
					return InstallResult{}, err
				}
				return InstallResult{Path: binaryPath}, nil
			},
			AllowInstallCommand: true,
		})

		if _, err := p.EnsureInstalledDetailed(context.Background(), "capability"); err == nil {
			t.Fatal("EnsureInstalledDetailed() without capability succeeded, want missing binary error")
		}
		if calls != 0 {
			t.Fatalf("InstallAction calls without capability = %d, want 0", calls)
		}
		if _, err := p.EnsureInstalledDetailed(WithToolCallInstallCheckOnly(WithInstallCommandCapability(context.Background())), "capability"); err == nil {
			t.Fatal("check-only EnsureInstalledDetailed() succeeded, want missing binary error")
		}
		if calls != 0 {
			t.Fatalf("InstallAction calls in check-only mode = %d, want 0", calls)
		}
		result, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "capability")
		if err != nil {
			t.Fatalf("EnsureInstalledDetailed() with capability error = %v", err)
		}
		if calls != 1 || result.Path != binaryPath || result.Status != InstallStatusInstalledPath {
			t.Fatalf("action result = %#v, calls = %d, want installed path and one call", result, calls)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		p := NewProvider()
		p.Register("timeout", InstallerConfig{
			BinaryName:     "timeout-action-binary",
			InstallTimeout: 5 * time.Millisecond,
			InstallAction: func(ctx context.Context) (InstallResult, error) {
				<-ctx.Done()
				return InstallResult{}, ctx.Err()
			},
			AllowInstallCommand: true,
		})

		_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "timeout")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("EnsureInstalledDetailed() error = %v, want context deadline exceeded", err)
		}
	})
}
