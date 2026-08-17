//go:build windows

package runtimeenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveWindowsLSPProductRootUsesStableUserCacheDefault 验证 Windows 默认产品根目录不依赖 cwd 或 PATH。
func TestResolveWindowsLSPProductRootUsesStableUserCacheDefault(t *testing.T) {
	t.Setenv(superDolphinHomeEnv, "")
	t.Setenv(projectRootEnv, "")
	t.Setenv(runtimeResourcesEnv, "")
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("os.UserCacheDir(): %v", err)
	}
	want := filepath.Join(userCacheDir, "super-agent-v3", "mcp-lsp", "language-servers")
	got, err := ResolveWindowsLSPProductRoot()
	if err != nil {
		t.Fatalf("ResolveWindowsLSPProductRoot(): %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("ResolveWindowsLSPProductRoot() = %q, want %q", got, want)
	}
}

// TestPrependWindowsRuntimePathEntriesRequiresAbsoluteEntries 验证 Windows PATH 注入只接受显式绝对目录并保持顺序。
func TestPrependWindowsRuntimePathEntriesRequiresAbsoluteEntries(t *testing.T) {
	t.Setenv("PATH", "existing")
	first := t.TempDir()
	second := t.TempDir()
	if err := PrependWindowsRuntimePathEntries(first, second); err != nil {
		t.Fatalf("PrependWindowsRuntimePathEntries(): %v", err)
	}
	parts := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	if len(parts) < 3 || parts[0] != first || parts[1] != second || parts[2] != "existing" {
		t.Fatalf("PATH after PrependWindowsRuntimePathEntries() = %#v", parts)
	}
	if err := PrependWindowsRuntimePathEntries("relative"); err == nil {
		t.Fatal("PrependWindowsRuntimePathEntries(relative) error = nil")
	}
}
