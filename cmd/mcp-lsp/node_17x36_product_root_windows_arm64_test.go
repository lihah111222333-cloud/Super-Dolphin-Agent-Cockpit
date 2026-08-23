//go:build windows && arm64

package main

import (
	"os"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// node17x36ReusableProductRootFixture 创建带 owner-only ACL 与受控清理的复用根测试夹具。
func node17x36ReusableProductRootFixture(t *testing.T) string {
	t.Helper()
	productRoot, err := os.MkdirTemp("", "sd-node-production-windows-arm64-process-arm64-17x36-reuse-")
	if err != nil {
		t.Fatalf("create reusable product root: %v", err)
	}
	t.Cleanup(func() {
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			t.Errorf("remove reusable product root: %v", err)
		}
	})
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict reusable product root: %v", err)
	}
	return productRoot
}
