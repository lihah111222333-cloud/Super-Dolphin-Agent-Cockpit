//go:build windows && arm64 && e2e

package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestWindowsARM64RustfmtLocalImportE2E 仅从已校验的本地官方 tar.xz payload 导入 v3
// product root；它不下载、不替代 Rust LSP 正式生命周期证明，receipt 明确标记 setup-only。
func TestWindowsARM64RustfmtLocalImportE2E(t *testing.T) {
	if os.Getenv("MCP_LSP_WINDOWS_ARM64_RUSTFMT_LOCAL_IMPORT_E2E") != "1" {
		t.Skip("set MCP_LSP_WINDOWS_ARM64_RUSTFMT_LOCAL_IMPORT_E2E=1 for local payload import")
	}
	root := os.Getenv("MCP_LSP_WINDOWS_ARM64_RUSTFMT_LOCAL_IMPORT_ROOT")
	if root == "" || !filepath.IsAbs(root) {
		t.Fatal("MCP_LSP_WINDOWS_ARM64_RUSTFMT_LOCAL_IMPORT_ROOT must be an absolute product root")
	}
	path, err := EnsureWindowsRustfmt(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("import official local Rustfmt payload: %v", err)
	}
	if err := ValidateWindowsRustfmtTools(path, WindowsHostArchARM64); err != nil {
		t.Fatal(err)
	}
	t.Logf("SETUP_NON_FORMAL_LOCAL status=NON_PASS_setup_only rustfmt=%s cargo_fmt=%s sha=%s", filepath.Base(path), filepath.Base(filepath.Join(filepath.Dir(path), "cargo-fmt.exe")), "d9e403d778e0ad95d814275b1265057478d4cde463d8bf620846056a7f00a59d")
}
