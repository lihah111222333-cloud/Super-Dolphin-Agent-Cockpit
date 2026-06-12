package tools

import (
	"context"
	"encoding/json"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
)

func TestMiddlewareChainAddsDeadline(t *testing.T) {
	handler := middleware.WithOutputBudget(
		"test_tool",
		wrapToolHandler("test_tool", 25*time.Millisecond, func(ctx context.Context, _ json.RawMessage) (any, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("expected deadline on wrapped context")
			}
			return map[string]any{"ok": true}, nil
		}),
		middleware.Budget{MaxBytes: 1024},
	)

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), nil); err != nil {
		t.Fatalf("wrapped handler failed: %v", err)
	}
}

func TestMiddlewareChainRecoversPanic(t *testing.T) {
	handler := middleware.WithOutputBudget(
		"test_tool",
		wrapToolHandler("test_tool", time.Second, func(context.Context, json.RawMessage) (any, error) {
			var crash func()
			crash()
			return nil, nil
		}),
		middleware.Budget{MaxBytes: 1024},
	)

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), nil); err == nil || !strings.Contains(err.Error(), "panic recovered:") {
		t.Fatalf("unexpected error: %v", err)
	}
}
