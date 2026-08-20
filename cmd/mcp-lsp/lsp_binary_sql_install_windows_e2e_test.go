//go:build windows && e2e

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const windowsSQLReusableProductRootE2EEnv = "MCP_LSP_WINDOWS_SQL_E2E_REUSE_PRODUCT_ROOT"

// windowsSQLReusableProductRootForE2E 解析显式 Windows SQL E2E 产品根目录。
// 配置值必须是已存在的绝对目录；空配置交给调用方创建私有临时根目录。
func windowsSQLReusableProductRootForE2E() (string, bool, error) {
	configured := strings.TrimSpace(os.Getenv(windowsSQLReusableProductRootE2EEnv))
	if configured == "" {
		return "", false, nil
	}
	configured = filepath.Clean(configured)
	if !filepath.IsAbs(configured) {
		return "", true, fmt.Errorf("%s must be an absolute Windows SQL E2E product root: %q", windowsSQLReusableProductRootE2EEnv, configured)
	}
	info, err := os.Stat(configured)
	if err != nil {
		return "", true, fmt.Errorf("stat reusable Windows SQL E2E product root %q: %w", configured, err)
	}
	if !info.IsDir() {
		return "", true, fmt.Errorf("reusable Windows SQL E2E product root %q is not a directory", configured)
	}
	return configured, true, nil
}

// windowsSQLProductRootForE2E 选择 Windows SQL E2E 产品根目录；未配置时才创建私有临时根。
func windowsSQLProductRootForE2E(t *testing.T) string {
	t.Helper()
	productRoot, reuseProductRoot, err := windowsSQLReusableProductRootForE2E()
	if err != nil {
		t.Fatalf("resolve reusable Windows SQL E2E product root: %v", err)
	}
	if !reuseProductRoot {
		return filepath.Join(t.TempDir(), "product")
	}
	t.Logf("reusing existing Windows SQL E2E product root without cleanup: %s", productRoot)
	return productRoot
}

func installSqruffForE2EPlatform(t *testing.T) string {
	t.Helper()
	productRoot := windowsSQLProductRootForE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	result, err := windowsSqruffInstallAction(productRoot, nil)(ctx)
	if err != nil {
		t.Fatalf("install product-owned Windows GoSQLS test backend: %v", err)
	}
	return filepath.Dir(result.Path)
}
