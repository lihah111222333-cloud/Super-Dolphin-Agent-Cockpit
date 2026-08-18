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

// TestMcpLSPBinaryPatchEditReplaceRangePreservesCRLFWindowsE2E 锁定 CRLF 文件在补丁携带 LF 时不得产生混合换行。
func TestMcpLSPBinaryPatchEditReplaceRangePreservesCRLFWindowsE2E(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("alpha\r\nbeta\r\nomega\r\n"), 0o600); err != nil {
		t.Fatalf("write CRLF fixture: %v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	response := client.callTool(t, "patch_edit", map[string]any{
		"action":    "replace_range",
		"file_path": target,
		"patch":     "@@\n-beta\n+BETA\n+GAMMA\n",
	})
	if response.Result.IsError {
		t.Fatalf("patch_edit returned MCP error: text=%q structured=%s stderr=%s",
			response.Result.ContentText(), response.Result.StructuredContent, client.stderrString())
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read patched CRLF file: %v", err)
	}
	if got, want := string(raw), "alpha\r\nBETA\r\nGAMMA\r\nomega\r\n"; got != want {
		t.Fatalf("patched content = %q, want all-CRLF %q", got, want)
	}
	requireCRLFOnly(t, raw)
}

// TestMcpLSPBinaryPatchEditReplaceRangeRepairsMixedLineEndingsWindowsE2E 覆盖 Windows 源文件的真实 MCP patch_edit 写入，禁止保留或生成混合 CRLF/LF。
func TestMcpLSPBinaryPatchEditReplaceRangeRepairsMixedLineEndingsWindowsE2E(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("alpha\r\nbeta\nomega\r\n"), 0o600); err != nil {
		t.Fatalf("write mixed-line-ending fixture: %v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	response := client.callTool(t, "patch_edit", map[string]any{
		"action":    "replace_range",
		"file_path": target,
		"patch":     "@@\n-beta\n+BETA\n+GAMMA\n",
	})
	if response.Result.IsError {
		t.Fatalf("patch_edit returned MCP error: text=%q structured=%s stderr=%s",
			response.Result.ContentText(), response.Result.StructuredContent, client.stderrString())
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read patched Windows file: %v", err)
	}
	if got, want := string(raw), "alpha\r\nBETA\r\nGAMMA\r\nomega\r\n"; got != want {
		t.Fatalf("patched content = %q, want all-CRLF %q", got, want)
	}
	requireCRLFOnly(t, raw)
}

// requireCRLFOnly 明确拒绝 CRLF 文件里的任何孤立 CR 或 LF。
func requireCRLFOnly(t *testing.T, raw []byte) {
	t.Helper()
	for index, value := range raw {
		switch value {
		case '\r':
			if index+1 >= len(raw) || raw[index+1] != '\n' {
				t.Fatalf("CRLF file contains a lone CR at byte %d: %q", index, raw)
			}
		case '\n':
			if index == 0 || raw[index-1] != '\r' {
				t.Fatalf("CRLF file contains a lone LF at byte %d: %q", index, raw)
			}
		}
	}
}
