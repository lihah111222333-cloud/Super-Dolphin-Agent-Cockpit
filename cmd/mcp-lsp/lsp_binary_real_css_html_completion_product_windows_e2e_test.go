//go:build windows && e2e

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func prepareRealCSSHTMLCompletionProductRoot(t *testing.T) {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	sourceRoot := filepath.Join(repoRoot, ".build-cache", "lsp-real-system-windows-product", "cache")
	if _, err := os.Stat(sourceRoot); err != nil {
		t.Fatalf("verified CSS/HTML product cache is unavailable: %v", err)
	}
	productRoot := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("create owner-only CSS/HTML product root: %v", err)
	}
	copyRealCSSHTMLProductCache(t, sourceRoot, filepath.Join(productRoot, "cache"))
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("reassert owner-only CSS/HTML product root ACL: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
}

func copyRealCSSHTMLProductCache(t *testing.T, sourceRoot, targetRoot string) {
	t.Helper()
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		t.Fatalf("create copied CSS/HTML product cache: %v", err)
	}
	cmd := exec.Command("robocopy", sourceRoot, targetRoot, "/E", "/COPY:DAT", "/DCOPY:DAT", "/NFL", "/NDL", "/NJH", "/NJS", "/R:0", "/W:0")
	if output, err := cmd.CombinedOutput(); err != nil && cmd.ProcessState.ExitCode() > 7 {
		t.Fatalf("copy verified CSS/HTML product cache: %v; output=%s", err, output)
	}
}
