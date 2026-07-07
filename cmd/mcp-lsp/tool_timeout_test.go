package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
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
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %T, want map", result)
	}
	if payload["isError"] != true {
		t.Fatalf("isError = %#v, want true; result=%#v", payload["isError"], payload)
	}
	envelope := decodeTimeoutToolErrorEnvelope(t, payload)
	if envelope.Code != "lsp_timeout" {
		t.Fatalf("error code = %q, want lsp_timeout; envelope=%#v", envelope.Code, envelope)
	}
	if envelope.Success {
		t.Fatalf("success = true, want false; envelope=%#v", envelope)
	}
}

func decodeTimeoutToolErrorEnvelope(t *testing.T, payload map[string]any) common.ToolErrorEnvelope {
	t.Helper()
	raw, ok := payload["structuredContent"].(json.RawMessage)
	if !ok {
		t.Fatalf("structuredContent = %T, want json.RawMessage", payload["structuredContent"])
	}
	var envelope common.ToolErrorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode structuredContent: %v", err)
	}
	return envelope
}
