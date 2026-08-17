//go:build !windows

package schema

import (
	"syscall"
	"testing"
)

func TestFilesystemWorkerRawErrnoIsNonWindowsNoOp(t *testing.T) {
	workerErr := classifiedWorkerError(CodeProcessExited, "raw errno", syscall.Errno(5), InitializationFailureTransient)
	if workerErr.WindowsErrorCode != 0 || workerErr.WindowsPermissionKind != "" {
		t.Fatalf("non-Windows raw errno produced permission fields: %#v", workerErr)
	}
	response := filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		Error: &filesystemWorkerError{
			Code: CodeProcessExited, Message: "typed fields are not accepted", FailureClass: InitializationFailureTransient,
			WindowsErrorCode: filesystemWorkerWindowsAccessDeniedCode, WindowsPermissionKind: filesystemWorkerWindowsAccessDeniedKind,
		},
	}
	if got := ErrorCode(filesystemWorkerResponseError(response)); got != CodeProtocolViolation {
		t.Fatalf("non-Windows permission fields error code = %q, want %q", got, CodeProtocolViolation)
	}
	if cause, err := filesystemWorkerPermissionCause(0, ""); err != nil || cause != nil {
		t.Fatalf("empty non-Windows permission cause = %v/%v", cause, err)
	}
}
