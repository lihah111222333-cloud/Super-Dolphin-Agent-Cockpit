package prompt

import (
	"context"
	"strings"
	"testing"
)

// TestBoundary_DynamicLivesInUncachedTail_MCPInstructions 固定动态危险内容的缓存边界。
// MCP instructions 必须进入 Boundary.UncachedTail，不能污染 CachedPrefix，
// 这样 MCP server 连接变化只会影响动态尾部，不会破坏稳定 system prompt 前缀。
func TestBoundary_DynamicLivesInUncachedTail_MCPInstructions(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	cwd := t.TempDir()
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
		Language: "English",
		Model:    "claude-sonnet-4",
		MCPSnapshot: MCPSnapshot{
			Servers: []string{"alpha"},
			Instructions: map[string]string{
				"alpha": "MCP-ALPHA-BOUNDARY-MARKER",
			},
		},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.Boundary == nil {
		t.Fatal("Boundary missing; expected static/dynamic split metadata")
	}
	if strings.Contains(assembly.Boundary.CachedPrefix, "MCP-ALPHA-BOUNDARY-MARKER") {
		t.Fatalf("CachedPrefix leaked DANGEROUS MCP content; cache would miss on every MCP change:\n%s",
			assembly.Boundary.CachedPrefix)
	}
	if !strings.Contains(assembly.Boundary.UncachedTail, "MCP-ALPHA-BOUNDARY-MARKER") {
		t.Fatalf("UncachedTail missing MCP content; expected it to land in tail:\n%s",
			assembly.Boundary.UncachedTail)
	}
	if !strings.Contains(assembly.Boundary.CachedPrefix, "You are Claude Code") {
		t.Fatalf("CachedPrefix missing stable identity header:\n%s",
			assembly.Boundary.CachedPrefix)
	}
}

// TestBoundary_RuntimeExtrasExcludesStaticSections 防止 static section 被镜像到 runtimeExtras。
// static 内容已经在可缓存 system prompt 前缀中，若再次进入 synthetic user meta，
// 会导致每 turn 重复发送完整静态正文并破坏 prompt cache。
func TestBoundary_RuntimeExtrasExcludesStaticSections(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
		Language: "English",
		Model:    "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	extras := assembly.UserContext["runtimeExtras"]
	if extras == "" {
		return
	}
	// 泄漏时 runtimeExtras 会带上 static section 正文中的固定短语。
	leakMarkers := []string{
		"You are Claude Code, Anthropic's official CLI", // identity
		"System constraints:",
		"Engineering principles:",
		"Executing actions with care:",
		"Tool preferences:",
		"Tone and style:",
		"Output efficiency:",
	}
	for _, marker := range leakMarkers {
		if strings.Contains(extras, marker) {
			t.Fatalf("runtimeExtras leaks static section %q; static content must stay in cacheable prefix only:\nextras=%s", marker, extras)
		}
	}
}
