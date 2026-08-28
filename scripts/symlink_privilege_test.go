package main

import "testing"

// skipIfSymlinkPrivilegeNotHeld 不再按宿主平台隐藏失败；调用方随后报告原始符号链接错误。
func skipIfSymlinkPrivilegeNotHeld(t *testing.T, _ error) {
	t.Helper()
}
