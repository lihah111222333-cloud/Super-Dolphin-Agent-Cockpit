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

func TestToolPayloadLogRedactsArgumentsAndResultByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(toolPayloadLogDirEnv, dir)
	t.Setenv(testToolPayloadLogDebugEnv, "")
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
	requestMeta := findToolPayloadSnapshotMap(t, dir, "request", "stdio", "edit")
	assertMetadataOnlyRequestSnapshot(t, request, requestMeta)
	result := findToolPayloadSnapshot(t, dir, "result", "stdio", "edit")
	resultMeta := findToolPayloadSnapshotMap(t, dir, "result", "stdio", "edit")
	assertMetadataOnlyResultSnapshot(t, result, resultMeta)
	assertSnapshotsDoNotContain(t, readToolPayloadFiles(t, dir), patch, `"success": true`)
}

func TestToolPayloadLogDebugModeStillRedactsSecrets(t *testing.T) {
	cases := []struct {
		name      string
		transport string
		run       func(t *testing.T, dir string)
	}{
		{
			name:      "stdio",
			transport: "stdio",
			run: func(t *testing.T, dir string) {
				var output bytes.Buffer
				server := NewServer("mcp-lsp", "dev", NewStdioTransport(bytes.NewReader(secretToolCallRequest(t, "req-secret")), &output), testToolProvider{})
				if err := server.Run(context.Background()); err != nil {
					t.Fatalf("Run() error = %v", err)
				}
			},
		},
		{
			name:      "http",
			transport: "http",
			run: func(t *testing.T, dir string) {
				server := NewHTTPServer("mcp-lsp", "dev", testToolProvider{})
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(secretToolCallRequest(t, "req-secret-http")))
				server.handleMCP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(toolPayloadLogDirEnv, dir)
			t.Setenv(testToolPayloadLogDebugEnv, "1")
			tc.run(t, dir)

			request := findToolPayloadSnapshot(t, dir, "request", tc.transport, "edit")
			if len(request.Arguments) == 0 {
				t.Fatalf("debug request arguments empty, want redacted body")
			}
			result := findToolPayloadSnapshot(t, dir, "result", tc.transport, "edit")
			if len(result.Result) == 0 {
				t.Fatalf("debug result empty, want redacted body")
			}
			files := readToolPayloadFiles(t, dir)
			assertSnapshotsDoNotContain(t, files, "secret-token-value", "hunter2", "sk-live-1234567890abcdef")
			for _, want := range []string{`"token"`, `"password"`, `"api_key"`, "[REDACTED]"} {
				if !strings.Contains(files, want) {
					t.Fatalf("debug snapshots missing %q redaction evidence: %s", want, files)
				}
			}
		})
	}
}

const testToolPayloadLogDebugEnv = "GO_AGENT_TOOL_PAYLOAD_LOG_DEBUG"

func assertMetadataOnlyRequestSnapshot(t *testing.T, request toolPayloadSnapshot, meta map[string]any) {
	t.Helper()
	if request.Deprecated {
		t.Fatalf("request.Deprecated = true, want false")
	}
	if request.Server != "mcp-lsp" || request.ReqID != "req-payload" {
		t.Fatalf("request identity = %#v, want mcp-lsp/req-payload", request)
	}
	assertRequestToolMetadata(t, request)
	if len(request.Arguments) != 0 {
		t.Fatalf("request.Arguments = %s, want metadata-only snapshot", request.Arguments)
	}
	assertSnapshotStatusAndRedaction(t, meta, "request")
	if request.RawArgsLen == 0 || request.RawResultLen != 0 {
		t.Fatalf("request sizes args=%d result=%d, want args only", request.RawArgsLen, request.RawResultLen)
	}
}

func assertRequestToolMetadata(t *testing.T, request toolPayloadSnapshot) {
	t.Helper()
	if request.Tool != "edit" {
		t.Fatalf("request.Tool = %q, want edit", request.Tool)
	}
	if request.CallID != "" {
		t.Fatalf("request.CallID = %q, want empty", request.CallID)
	}
}

func assertMetadataOnlyResultSnapshot(t *testing.T, result toolPayloadSnapshot, meta map[string]any) {
	t.Helper()
	if result.Tool != "edit" {
		t.Fatalf("result.Tool = %q, want edit", result.Tool)
	}
	if len(result.Result) != 0 {
		t.Fatalf("result.Result = %s, want metadata-only snapshot", result.Result)
	}
	assertSnapshotStatusAndRedaction(t, meta, "success")
	if result.RawResultLen == 0 {
		t.Fatalf("RawResultLen = 0, want result size metadata")
	}
	if _, ok := meta["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %#v, want numeric duration metadata", meta["duration_ms"])
	}
}

func assertSnapshotStatusAndRedaction(t *testing.T, meta map[string]any, wantStatus string) {
	t.Helper()
	if meta["status"] != wantStatus {
		t.Fatalf("status = %#v, want %q in %#v", meta["status"], wantStatus, meta)
	}
	if meta["payload_redacted"] != true {
		t.Fatalf("payload_redacted = %#v, want true in %#v", meta["payload_redacted"], meta)
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

func secretToolCallRequest(t *testing.T, id any) []byte {
	t.Helper()
	return toolCallRequest(t, id, "edit", map[string]any{
		"token":    "secret-token-value",
		"password": "hunter2",
		"api_key":  "sk-live-1234567890abcdef",
		"nested": map[string]any{
			"note": "Bearer sk-live-1234567890abcdef",
		},
	})
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

func findToolPayloadSnapshotMap(t *testing.T, dir, stage, transport, tool string) map[string]any {
	t.Helper()
	for _, snapshot := range readToolPayloadSnapshotMaps(t, dir) {
		if snapshot["stage"] == stage && snapshot["transport"] == transport && snapshot["tool"] == tool {
			return snapshot
		}
	}
	t.Fatalf("missing %s/%s/%s snapshot map in %s", stage, transport, tool, dir)
	return nil
}

func readToolPayloadSnapshotMaps(t *testing.T, dir string) []map[string]any {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read payload dir: %v", err)
	}
	snapshots := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read payload snapshot %s: %v", entry.Name(), err)
		}
		var snapshot map[string]any
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatalf("decode payload snapshot %s: %v", entry.Name(), err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func readToolPayloadFiles(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read payload dir: %v", err)
	}
	var out strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read payload snapshot %s: %v", entry.Name(), err)
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.String()
}

func assertSnapshotsDoNotContain(t *testing.T, files string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(files, value) {
			t.Fatalf("payload snapshot leaked %q: %s", value, files)
		}
	}
}
