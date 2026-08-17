//go:build windows

package schema

import (
	"os"
	"strings"
	"testing"
)

// TestWindowsProcessGuardSuspendsAssignsThenResumes 以 Windows 专用源码守卫锁定
// CREATE_SUSPENDED、Job 归属、恢复与失败清理的顺序；非 Windows 不编译本测试。
func TestWindowsProcessGuardSuspendsAssignsThenResumes(t *testing.T) {
	source, err := os.ReadFile("process_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	suspend := strings.Index(text, "windows.CREATE_SUSPENDED")
	assign := strings.Index(text, "windows.AssignProcessToJobObject")
	resume := strings.Index(text, "NtResumeProcess")
	if suspend < 0 || assign < 0 || resume < 0 || assign >= resume {
		t.Fatalf("Windows process guard order missing: suspend=%d assign=%d resume=%d", suspend, assign, resume)
	}
	closeOnFailure := strings.Index(text[resume:], "windows.CloseHandle(handle)")
	if closeOnFailure < 0 {
		t.Fatalf("Windows process guard order missing: suspend=%d assign=%d resume=%d close=%d", suspend, assign, resume, closeOnFailure)
	}
}
