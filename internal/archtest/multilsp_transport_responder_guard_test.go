package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPTransportResponderOwnedByWaitGroup 守住 multilsp transport 的 responder 生命周期。
// server request responder 不能用 fire-and-forget goroutine 启动；所有派发路径都必须经由
// spawnResponder 先登记 responderWG，再让 Close/stopWithError 等待在途响应结束。
//
// 禁止形态：
//   - 直接 `go t.respondToServerRequest(`，会绕过 responderWG。
//   - 直接 `go respondToServerRequest(`，会把 responder 提升为包级裸 goroutine。
//
// 同时要求 transport_conn.go 保留 drainResponders 调用，避免重构时丢掉关闭前等待。
func TestMultiLSPTransportResponderOwnedByWaitGroup(t *testing.T) {
	const dir = "../../cmd/mcp-lsp/multilsp"

	forbidden := []string{
		"go t.respondToServerRequest(",
		"go respondToServerRequest(",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	drainFound := false
	for _, e := range entries {
		if !isResponderProductionGoEntry(e) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		text := readResponderTransportGuardFile(t, path)
		assertNoForbiddenResponderSpawn(t, path, text, forbidden)
		if responderFileHasDrain(text) {
			drainFound = true
		}
	}
	if !drainFound {
		t.Errorf("cmd/mcp-lsp/multilsp: expected at least one drainResponders( call to remain wired into the transport lifecycle")
	}
}

func isResponderProductionGoEntry(e os.DirEntry) bool {
	name := e.Name()
	return !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func readResponderTransportGuardFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertNoForbiddenResponderSpawn(t *testing.T, path, text string, forbidden []string) {
	t.Helper()
	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden responder spawn literal %q present (P22 P2 LSP-S3: route through spawnResponder so responderWG can drain)", path, tok)
		}
	}
}

func responderFileHasDrain(text string) bool {
	return strings.Contains(text, "drainResponders(")
}
