package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	cleanup := func() {}
	if strings.TrimSpace(os.Getenv(toolPayloadLogDirEnv)) == "" {
		dir, err := os.MkdirTemp("", "mcp-tool-payload-test-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "create tool payload temp dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.Setenv(toolPayloadLogDirEnv, dir); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", toolPayloadLogDirEnv, err)
			os.Exit(1)
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestToolPayloadLogPersistsCompleteStdioArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(toolPayloadLogDirEnv, dir)
	patch := " import (\n+\t\"fmt\"\n )"
	req := toolCallRequest(t, "req-payload", "edit", map[string]any{
		"file_path": "internal/foo.go",
		"patch":     patch,
	})
	var output bytes.Buffer
	provider := captureToolProvider{call: func(_ context.Context, _ string, args json.RawMessage) (any, error) {
		if !bytes.Contains(args, []byte(`"patch"`)) {
			t.Fatalf("CallTool() args = %s, want patch payload", args)
		}
		return map[string]any{"success": true}, nil
	}}

	server := NewServer("mcp-lsp", "dev", NewStdioTransport(bytes.NewReader(req), &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	request := findToolPayloadSnapshot(t, dir, "request", "stdio", "edit")
	assertCompleteStdioRequestSnapshot(t, request, patch)
	result := findToolPayloadSnapshot(t, dir, "result", "stdio", "edit")
	var decoded map[string]bool
	if err := json.Unmarshal(result.Result, &decoded); err != nil {
		t.Fatalf("decode result snapshot: %v", err)
	}
	if !decoded["success"] {
		t.Fatalf("result snapshot = %s, want raw tool result payload", result.Result)
	}
}

func assertCompleteStdioRequestSnapshot(t *testing.T, request toolPayloadSnapshot, patch string) {
	t.Helper()
	if request.Deprecated {
		t.Fatalf("request.Deprecated = true, want false")
	}
	if request.Server != "mcp-lsp" || request.ReqID != "req-payload" {
		t.Fatalf("request identity = %#v, want mcp-lsp/req-payload", request)
	}
	var args struct {
		FilePath string `json:"file_path"`
		Patch    string `json:"patch"`
	}
	if err := json.Unmarshal(request.Arguments, &args); err != nil {
		t.Fatalf("decode request arguments: %v", err)
	}
	if args.FilePath != "internal/foo.go" || args.Patch != patch {
		t.Fatalf("request arguments = %#v, want exact file_path and patch", args)
	}
	if request.RawArgsLen <= len(patch) {
		t.Fatalf("RawArgsLen = %d, want full JSON args length > patch length %d", request.RawArgsLen, len(patch))
	}
}

func TestToolPayloadLogMarksHTTPTransportDeprecated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(toolPayloadLogDirEnv, dir)
	server := NewHTTPServer("mcp-lsp", "dev", testToolProvider{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(toolCallRequest(t, 12, "edit", map[string]any{
		"patch": "+http legacy path\n",
	})))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	request := findToolPayloadSnapshot(t, dir, "request", "http", "edit")
	if !request.Deprecated {
		t.Fatalf("HTTP request snapshot Deprecated = false, want true")
	}
	result := findToolPayloadSnapshot(t, dir, "result", "http", "edit")
	if !result.Deprecated {
		t.Fatalf("HTTP result snapshot Deprecated = false, want true")
	}
}

func toolCallRequest(t *testing.T, id any, name string, args map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	return raw
}

func findToolPayloadSnapshot(t *testing.T, dir, stage, transport, tool string) toolPayloadSnapshot {
	t.Helper()
	for _, snapshot := range readToolPayloadSnapshots(t, dir) {
		if snapshot.Stage == stage && snapshot.Transport == transport && snapshot.Tool == tool {
			return snapshot
		}
	}
	t.Fatalf("missing %s/%s/%s snapshot in %s", stage, transport, tool, dir)
	return toolPayloadSnapshot{}
}

func readToolPayloadSnapshots(t *testing.T, dir string) []toolPayloadSnapshot {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read payload dir: %v", err)
	}
	snapshots := make([]toolPayloadSnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read payload snapshot %s: %v", entry.Name(), err)
		}
		var snapshot toolPayloadSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatalf("decode payload snapshot %s: %v", entry.Name(), err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}
