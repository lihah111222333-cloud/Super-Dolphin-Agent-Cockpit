//go:build !windows

package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNilInstallActionPreservesLegacyCommandPathOnNonWindows(t *testing.T) {
	target := filepath.Join(t.TempDir(), "legacy-language-server")
	marker := filepath.Join(t.TempDir(), "legacy-args")
	p := NewProvider()
	p.Register("legacy", InstallerConfig{
		BinaryName: "legacy-language-server",
		InstallCmd: "/bin/sh",
		InstallArgs: []string{
			"-c",
			"printf '%s\\n' \"$@\" > \"$1\"; printf binary > \"$2\"",
			"legacy-command-argv0",
			marker,
			target,
		},
		InstalledBinaryPathResolver: func(context.Context) (string, error) {
			return target, nil
		},
		AllowInstallCommand: true,
	})

	result, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "legacy")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed() error = %v", err)
	}
	if result.Path != target || result.Status != InstallStatusInstalledPath {
		t.Fatalf("result = %#v, want legacy installed path %q", result, target)
	}
	if data, err := os.ReadFile(marker); err != nil {
		t.Fatalf("ReadFile(marker): %v", err)
	} else if string(data) != marker+"\n"+target+"\n" {
		t.Fatalf("legacy command args = %q, want marker and target paths", string(data))
	}
}
