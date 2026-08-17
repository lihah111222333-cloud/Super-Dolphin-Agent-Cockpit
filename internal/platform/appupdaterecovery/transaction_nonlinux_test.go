//go:build !linux

package appupdaterecovery

import "testing"

// 非 Linux 桌面文件系统执行 discard root identity 断言；公共测试只调用这个平台契约入口。
func requireDesktopDiscardIdentitySemantics(t *testing.T) {
	t.Helper()
}
