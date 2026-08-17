package main

import (
	"os"
	"os/exec"
	"testing"
)

// requireRealGopls 统一约束普通测试与 e2e 测试的真实 gopls 依赖，避免 build tag 改变辅助函数可见性。
func requireRealGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		if os.Getenv("MCP_LSP_REQUIRE_REAL_GOPLS_E2E") == "1" {
			t.Fatalf("gopls is required for explicitly requested real Go E2E: %v", err)
		}
		t.Skipf("real gopls E2E dependency is not present on PATH: %v", err)
	}
}
