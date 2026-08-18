//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestOrdinaryStdioToolCallUsesWindowsClassifier(t *testing.T) {
	t.Setenv("GO_AGENT_LSP_ROOT", t.TempDir())
	path := filepath.Join(t.TempDir(), "private-state.db")
	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{
		"file": func(context.Context, json.RawMessage) (any, error) {
			raw := &os.PathError{Op: "open", Path: path, Err: syscall.Errno(5)}
			return nil, securefs.WrapErrorForPath(raw, path)
		},
	})}

	result, err := handleScopedToolsCall(context.Background(), provider, "stdio", json.RawMessage(`{"name":"file","arguments":{"action":"open_file","file_path":"README.md"}}`))
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal stdio result error = %v", err)
	}
	var envelope struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope.Content) == 0 {
		t.Fatalf("stdio result decode error=%v payload=%s", err, payload)
	}
	doc, err := lineprotocol.Parse(envelope.Content[0].Text)
	if err != nil || doc.Error == nil || doc.Error.Code != "authorization_required" {
		t.Fatalf("stdio result content = %q, want line protocol authorization_required: %v", envelope.Content[0].Text, err)
	}
	if bytes.Contains([]byte(envelope.Content[0].Text), []byte(path)) {
		t.Fatalf("stdio result leaked raw path: %s", envelope.Content[0].Text)
	}
	if envelope.Meta["authorization_required"] != true || envelope.Meta["windows_error_code"] != float64(5) || envelope.Meta["windows_permission_kind"] != "access_denied" {
		t.Fatalf("stdio result _meta = %#v, want ACL metadata side channel", envelope.Meta)
	}
}
