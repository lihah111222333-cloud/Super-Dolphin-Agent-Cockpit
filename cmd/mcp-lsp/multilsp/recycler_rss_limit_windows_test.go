//go:build windows

package multilsp

import "testing"

// TestWindowsRecyclerRSSLimitSelection 锁定 Windows 构建选用 Windows gopls RSS 阈值。
func TestWindowsRecyclerRSSLimitSelection(t *testing.T) {
	t.Setenv(lspGoRSSLimitEnv, "")
	if got := rssLimitBytesForLanguage("go"); got != defaultGoWindowsRSSLimitBytes {
		t.Fatalf("Windows gopls RSS limit=%d, want %d", got, uint64(defaultGoWindowsRSSLimitBytes))
	}
}
