package main

import (
	"runtime"
	"strings"
	"testing"
)

// skipIfSymlinkPrivilegeNotHeld 只识别 Windows 测试夹具创建符号链接时的宿主权限缺口；
// 产品 ACL 失败仍由 typed authorization_required 与宿主 ApprovalRequester 处理。
func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	t.Helper()
	if runtime.GOOS == "windows" && err != nil && strings.Contains(err.Error(), "A required privilege is not held") {
		t.Skipf("skipping symlink test without Windows symlink privilege: %v", err)
	}
}
