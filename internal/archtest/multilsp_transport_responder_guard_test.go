package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPTransportResponderOwnedByWaitGroup enforces P22 P2 LSP-S3:
// the multilsp transport must not spawn server-request responder
// goroutines fire-and-forget. Every dispatch path goes through
// spawnResponder, which Add(1)s the responderWG before launching the
// goroutine, so Close()/stopWithError can drainResponders() and wait
// for in-flight work before killing the process.
//
// Forbidden shapes:
//   - literal `go t.respondToServerRequest(` (the pre-S3 pattern)
//   - literal `go respondToServerRequest(` (plausible drift to a
//     package-level responder)
//
// Also asserts that transport_conn.go keeps the drainResponders(
// call so future refactors can't silently drop the drain.
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
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, tok := range forbidden {
			if strings.Contains(text, tok) {
				t.Errorf("%s: forbidden responder spawn literal %q present (P22 P2 LSP-S3: route through spawnResponder so responderWG can drain)", path, tok)
			}
		}
		if strings.Contains(text, "drainResponders(") {
			drainFound = true
		}
	}
	if !drainFound {
		t.Errorf("cmd/mcp-lsp/multilsp: expected at least one drainResponders( call to remain wired into the transport lifecycle")
	}
}
