//go:build !windows

package multilsp

import (
	"strings"
	"testing"
)

// assertAtomicReplacementOutcome 是公共快照测试的非 Windows 断言；
// POSIX 允许替换已打开路径，读取阶段必须拒绝路径内容被替换。
func assertAtomicReplacementOutcome(t *testing.T, renameErr error, _ documentSnapshot, err error) {
	t.Helper()
	if renameErr != nil {
		t.Fatalf("atomic replace target: %v", renameErr)
	}
	if err == nil || !strings.Contains(err.Error(), "path was replaced") {
		t.Fatalf("read snapshot error = %v, want atomic path replacement rejection", err)
	}
}
