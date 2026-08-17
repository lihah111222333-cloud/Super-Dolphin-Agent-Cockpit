//go:build !windows

package multilsp

import "testing"

// TestNonWindowsRecyclerRSSLimitSelection 锁定非 Windows 构建不继承 Windows gopls RSS 阈值。
func TestNonWindowsRecyclerRSSLimitSelection(t *testing.T) {
	t.Setenv(lspGoRSSLimitEnv, "")
	if got := rssLimitBytesForLanguage("go"); got != defaultGoRSSLimitBytes {
		t.Fatalf("non-Windows gopls RSS limit=%d, want %d", got, uint64(defaultGoRSSLimitBytes))
	}
}
