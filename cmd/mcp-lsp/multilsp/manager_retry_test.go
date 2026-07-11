package multilsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestHoverRetriesRustAnalyzerContentModified(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"retry-hover\"\nversion = \"0.1.0\"\n")
	target := filepath.Join(root, "src", "main.rs")
	writeGenericTestFile(t, target, "fn main() {\n    let value = 1;\n}\n")

	client := &contentModifiedHoverClient{}
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	mgr.retryBaseDelay = time.Millisecond
	t.Cleanup(func() { _ = mgr.Close() })

	hover, err := mgr.Hover(ctxWithCWD(root, "agent-rust-content-modified", "thread-1"), target, protocol.Position{Line: 0, Character: 3})
	if err != nil {
		t.Fatalf("Hover() error = %v, want retry after content modified", err)
	}
	payload, err := json.Marshal(hover.Contents)
	if err != nil {
		t.Fatalf("marshal hover contents: %v", err)
	}
	if !strings.Contains(string(payload), "fn main") {
		t.Fatalf("Hover() contents = %s, want retried hover result", payload)
	}
	if got := client.hoverRequestCount(); got != 2 {
		t.Fatalf("hover request count = %d, want first failure plus one retry", got)
	}
}

type contentModifiedHoverClient struct {
	noopClient

	mu            sync.Mutex
	hoverRequests int
}

func (c *contentModifiedHoverClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != protocol.MethodHover {
		return json.RawMessage("null"), nil
	}

	c.mu.Lock()
	c.hoverRequests++
	attempt := c.hoverRequests
	c.mu.Unlock()

	if attempt == 1 {
		return nil, &responseError{Code: lspErrorContentModified, Message: "content modified"}
	}
	return json.Marshal(protocol.HoverResult{
		Contents: protocol.MarkupContent{
			Kind:  "markdown",
			Value: "```rust\nfn main()\n```",
		},
	})
}

func (c *contentModifiedHoverClient) hoverRequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hoverRequests
}
