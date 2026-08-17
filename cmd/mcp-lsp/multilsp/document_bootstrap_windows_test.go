//go:build windows

package multilsp

import (
	"os"
	"strings"
	"testing"
)

// assertAtomicReplacementOutcome 是公共快照测试的 Windows 断言；
// 目标文件保持打开时，Windows 应报告权限错误并保留原始干净快照。
func assertAtomicReplacementOutcome(t *testing.T, renameErr error, snapshot documentSnapshot, err error) {
	t.Helper()
	if renameErr != nil {
		if !os.IsPermission(renameErr) {
			t.Fatalf("Windows atomic replacement error = %v, want access denied while target is open", renameErr)
		}
		if err != nil || snapshot.text != "function OldAtomic() {}\n" {
			t.Fatalf("snapshot after OS-blocked replacement = text %q err %v, want original clean snapshot", snapshot.text, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "path was replaced") {
		t.Fatalf("read snapshot error = %v, want atomic path replacement rejection", err)
	}
}
