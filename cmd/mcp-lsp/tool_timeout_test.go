package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
)

type scopedToolCallResult struct {
	result any
	err    error
}

func TestHandleScopedToolsCallTimesOutBlockedToolHandler(t *testing.T) {
	t.Setenv("GO_AGENT_LSP_ROOT", t.TempDir())
	release := make(chan struct{})
	started := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	t.Cleanup(func() { close(release) })

	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "inspect"},
		Handler: ToolHandler(middleware.Timeout(25 * time.Millisecond)(func(context.Context, json.RawMessage) (any, error) {
			close(started)
			<-release
			return map[string]any{"ok": true}, nil
		})),
	}}
	done := callScopedToolInBackground(goroutines, defs)

	waitForBlockedHandlerToStart(t, started)
	got := waitForScopedToolCall(t, done)
	if got.err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", got.err)
	}
	assertTimeoutToolResult(t, got.result)
}

func callScopedToolInBackground(goroutines *testGoroutineGroup, defs []toolDefinition) <-chan scopedToolCallResult {
	done := make(chan scopedToolCallResult, 1)
	goroutines.Go(func() {
		result, err := handleScopedToolsCall(
			context.Background(),
			registryToolProvider{defs: defs},
			"lsp",
			json.RawMessage(`{"name":"inspect","arguments":{}}`),
		)
		done <- scopedToolCallResult{result: result, err: err}
	})
	return done
}

func waitForBlockedHandlerToStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blocked handler did not start")
	}
}

func waitForScopedToolCall(t *testing.T, done <-chan scopedToolCallResult) scopedToolCallResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(150 * time.Millisecond):
		t.Fatal("timeout middleware did not return while handler was blocked")
	}
	return scopedToolCallResult{}
}

func assertTimeoutToolResult(t *testing.T, result any) {
	t.Helper()
	text := requirePlainTextToolResult(t, result, true)
	for _, want := range []string{"lsp_timeout", "retryable=1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeout content = %q, want %q", text, want)
		}
	}
}
