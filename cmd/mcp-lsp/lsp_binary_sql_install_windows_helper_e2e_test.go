//go:build windows && e2e

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWindowsSQLReusableProductRootForE2EReusesExistingDirectory 验证显式 Windows
// SQL E2E 产品根目录只复用既有绝对目录，不创建临时目录或改变其内容。
func TestWindowsSQLReusableProductRootForE2EReusesExistingDirectory(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write reusable Windows SQL E2E sentinel: %v", err)
	}
	t.Setenv(windowsSQLReusableProductRootE2EEnv, root)

	got, reused, err := windowsSQLReusableProductRootForE2E()
	if err != nil {
		t.Fatalf("resolve reusable Windows SQL E2E product root: %v", err)
	}
	if got != filepath.Clean(root) || !reused {
		t.Fatalf("reusable Windows SQL E2E product root = (%q, %t), want (%q, true)", got, reused, filepath.Clean(root))
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("reusable Windows SQL E2E product root changed existing content: %v", err)
	}
}

// TestWindowsSQLReusableProductRootForE2ERejectsRelativeOrMissingDirectory 锁定显式
// 配置必须是已存在的绝对目录，避免把下载落入宿主当前目录或不存在路径。
func TestWindowsSQLReusableProductRootForE2ERejectsRelativeOrMissingDirectory(t *testing.T) {
	for name, configured := range map[string]string{
		"relative": "relative-product-root",
		"missing":  filepath.Join(t.TempDir(), "missing-product-root"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(windowsSQLReusableProductRootE2EEnv, configured)
			if got, _, err := windowsSQLReusableProductRootForE2E(); err == nil {
				t.Fatalf("resolve invalid Windows SQL E2E product root = %q, want error", got)
			}
		})
	}
}

// TestWindowsSQLProductRootForE2EFallsBackToPrivateTemp 锁定未配置复用根时仍使用
// 测试私有临时目录，避免改变非 Windows 或已有缓存路径的行为。
func TestWindowsSQLProductRootForE2EFallsBackToPrivateTemp(t *testing.T) {
	t.Setenv(windowsSQLReusableProductRootE2EEnv, "")
	if got, reused, err := windowsSQLReusableProductRootForE2E(); err != nil || got != "" || reused {
		t.Fatalf("unset reusable Windows SQL E2E product root = (%q, %t, %v), want (empty, false, nil)", got, reused, err)
	}
	root := windowsSQLProductRootForE2E(t)
	if !filepath.IsAbs(root) {
		t.Fatalf("private Windows SQL E2E product root = %q, want absolute path", root)
	}
	if info, err := os.Stat(filepath.Dir(root)); err != nil || !info.IsDir() {
		t.Fatalf("private Windows SQL E2E product root parent is not a directory: %q err=%v", root, err)
	}
}
