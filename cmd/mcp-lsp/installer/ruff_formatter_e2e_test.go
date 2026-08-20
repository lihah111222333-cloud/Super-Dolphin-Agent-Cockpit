//go:build e2e

package installer

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestRuffFormatterProductAssetE2E(t *testing.T) {
	root := t.TempDir()
	path, err := ResolveOrInstallRuffFormatter(context.Background(), root)
	if err != nil {
		t.Fatalf("ResolveOrInstallRuffFormatter: %v", err)
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("Ruff formatter --version: %v; output=%q", err, output)
	}
	if !strings.Contains(string(output), RuffFormatterVersion) {
		t.Fatalf("Ruff formatter version = %q, want %s", output, RuffFormatterVersion)
	}
}
