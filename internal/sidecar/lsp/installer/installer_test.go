package installer

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestEnsureInstalledDetailedLogsPathResolvedBinary verifies the fast path logs
// the same resolved-binary boundary event as post-install resolution.
func TestEnsureInstalledDetailedLogsPathResolvedBinary(t *testing.T) {
	dir := t.TempDir()
	binaryName := "fake-lsp-test"
	fileName := binaryName
	if runtime.GOOS == "windows" {
		fileName += ".exe"
	}
	binaryPath := filepath.Join(dir, fileName)
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var log bytes.Buffer
	provider := &Provider{
		configs: map[string]InstallerConfig{
			"go": {
				BinaryName: binaryName,
				InstallCmd: "unused-installer",
				Language:   "go",
			},
		},
		logger: slog.New(slog.NewTextHandler(&log, nil)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := provider.EnsureInstalledDetailed(ctx, "go")
	if err != nil {
		t.Fatalf("ensure installed: %v", err)
	}
	if result.Status != InstallStatusPathFound {
		t.Fatalf("status = %q, want %q", result.Status, InstallStatusPathFound)
	}
	if result.Path != binaryPath {
		t.Fatalf("path = %q, want %q", result.Path, binaryPath)
	}
	if got := log.String(); !strings.Contains(got, "LSP binary resolved") || !strings.Contains(got, "status=path_found") {
		t.Fatalf("log output missing resolved binary status: %s", got)
	}
}
