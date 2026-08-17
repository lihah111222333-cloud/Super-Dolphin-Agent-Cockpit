//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestHTTPRunnerInjectsWindowsToolErrorClassifier(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "http-test-token")
	t.Setenv("GO_AGENT_LSP_ROOT", t.TempDir())
	path := filepath.Join(t.TempDir(), "private-state.db")

	runner, err := newHTTPRunner(ToolHandlers{
		"file": func(context.Context, json.RawMessage) (any, error) {
			raw := syscall.Errno(5)
			return nil, securefs.WrapErrorForPath(raw, path)
		},
	}, pkglogger.NewRuntime(pkglogger.RuntimeConfig{}))
	if err != nil {
		t.Fatalf("newHTTPRunner() error = %v", err)
	}
	httpRunner, ok := runner.(*httpRunner)
	if !ok {
		t.Fatalf("newHTTPRunner() = %T, want *httpRunner", runner)
	}
	addr, stop, err := httpRunner.startServer(context.Background())
	if err != nil {
		t.Fatalf("startServer() error = %v", err)
	}
	t.Cleanup(func() {
		if err := stop(context.Background()); err != nil {
			t.Errorf("stop() error = %v", err)
		}
	})

	client := &http.Client{}
	endpoint := "http://" + addr + "/mcp"
	sessionID := postHTTPRunnerJSON(t, client, endpoint, "http-test-token", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test"}}}`)
	body := postHTTPRunnerJSONBody(t, client, endpoint, "http-test-token", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"file","arguments":{}}}`)
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("tools/call response decode error = %v; body=%s", err, body)
	}
	text := httpResponseContentText(t, response)
	doc, err := lineprotocol.Parse(text)
	if err != nil || doc.Error == nil || doc.Error.Code != "authorization_required" {
		t.Fatalf("HTTP response content = %q, want line protocol authorization_required: %v", text, err)
	}
	if strings.Contains(text, path) {
		t.Fatalf("HTTP response leaked raw path: %s", text)
	}
}

func httpResponseContentText(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("HTTP response missing result: %#v", response)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("HTTP response missing content: %#v", result)
	}
	item, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("HTTP response content item malformed: %#v", content[0])
	}
	text, ok := item["text"].(string)
	if !ok {
		t.Fatalf("HTTP response content text malformed: %#v", item)
	}
	return text
}

func postHTTPRunnerJSON(t *testing.T, client *http.Client, endpoint, token, sessionID, payload string) string {
	t.Helper()
	resp := postHTTPRunnerRequest(t, client, endpoint, token, sessionID, payload)
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close HTTP response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	receivedSessionID := resp.Header.Get("Mcp-Session-Id")
	if receivedSessionID == "" {
		t.Fatal("initialize response did not include Mcp-Session-Id")
	}
	return receivedSessionID
}

func postHTTPRunnerJSONBody(t *testing.T, client *http.Client, endpoint, token, sessionID, payload string) []byte {
	t.Helper()
	resp := postHTTPRunnerRequest(t, client, endpoint, token, sessionID, payload)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close HTTP response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	return body
}

func postHTTPRunnerRequest(t *testing.T, client *http.Client, endpoint, token, sessionID, payload string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("HTTP request construction error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP request error = %v", err)
	}
	return resp
}
