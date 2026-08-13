//go:build e2e && windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMcpLSPBinaryPatchEditReplaceRangeWindowsE2E 验证 Windows 本地二进制的单段与多段补丁写入。
func TestMcpLSPBinaryPatchEditReplaceRangeWindowsE2E(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\nomega\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	for _, tc := range []struct {
		name  string
		patch string
		want  string
	}{
		{name: "single patch", patch: "@@\n-beta\n+BETA\n", want: "alpha\nBETA\nomega\n"},
		{name: "multi patch", patch: "@@ alpha\n-alpha\n+ALPHA\n@@ omega\n-omega\n+OMEGA\n", want: "ALPHA\nBETA\nOMEGA\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := client.callTool(t, "patch_edit", map[string]any{
				"action":    "replace_range",
				"file_path": target,
				"patch":     tc.patch,
			})
			if response.Result.IsError {
				t.Fatalf("patch_edit returned MCP error: text=%q structured=%s stderr=%s",
					response.Result.ContentText(), response.Result.StructuredContent, client.stderrString())
			}
			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read patched file: %v", err)
			}
			if string(raw) != tc.want {
				t.Fatalf("patched content = %q, want %q", raw, tc.want)
			}
		})
	}
}
