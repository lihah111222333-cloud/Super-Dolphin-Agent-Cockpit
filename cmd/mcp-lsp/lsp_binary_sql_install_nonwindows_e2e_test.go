//go:build !windows && e2e

package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func installSqruffForE2EPlatform(t *testing.T) string {
	t.Helper()
	venv := filepath.Join(t.TempDir(), "venv")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	create := exec.CommandContext(ctx, "python3", "-m", "venv", venv)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create sqruff test venv: %v\n%s", err, out)
	}
	binDir := sqruffVenvBinDirForE2E(venv)
	install := exec.CommandContext(ctx, filepath.Join(binDir, mcpLSPExecutableFileName("pip")), "install", "sqruff=="+sqruffInstallVersion)
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install sqruff test backend: %v\n%s", err, out)
	}
	return binDir
}
