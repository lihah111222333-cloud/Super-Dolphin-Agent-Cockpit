//go:build !windows

package tools

import (
	"os"
	"syscall"
	"testing"
)

// TestToolErrorClassifierNonWindowsNativePermissionErrorsStayUnclassified 证明共享 classifier
// 不会把非 Windows 平台碰巧同数值的 errno 5/1314 转成 Windows 授权弹窗请求。
func TestToolErrorClassifierNonWindowsNativePermissionErrorsStayUnclassified(t *testing.T) {
	for _, code := range []syscall.Errno{5, 1314} {
		err := &os.PathError{Op: "open", Path: "/private/native-path", Err: code}
		if classification, ok := ToolErrorClassifier("lsp/file", err); ok {
			t.Fatalf("native non-Windows errno %d classified as %+v", code, classification)
		}
	}
}
