//go:build linux

package appupdaterecovery

import "testing"

// Linux 的桌面文件系统不提供该测试所需的 discard root identity 语义，明确跳过而非运行时选择。
func requireDesktopDiscardIdentitySemantics(t *testing.T) {
	t.Helper()
	t.Skip("discard root identity is enforced by the supported desktop filesystem implementations")
}
