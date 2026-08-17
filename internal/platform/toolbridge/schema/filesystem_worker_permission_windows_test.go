//go:build windows

package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

func TestFilesystemWorkerPermissionWireRoundTripWindows(t *testing.T) {
	const rawPath = `C:\secret\schema-helper.exe`
	workerErr := classifiedWorkerError(
		CodeProcessExited,
		"schema helper filesystem operation",
		securefs.WrapErrorForPath(syscall.Errno(5), rawPath),
		InitializationFailureTransient,
	)
	if workerErr.WindowsErrorCode != filesystemWorkerWindowsAccessDeniedCode ||
		workerErr.WindowsPermissionKind != filesystemWorkerWindowsAccessDeniedKind {
		t.Fatalf("permission fields = %d/%q, want 5/access_denied", workerErr.WindowsErrorCode, workerErr.WindowsPermissionKind)
	}
	if strings.Contains(workerErr.WindowsPermissionKind, rawPath) || strings.Contains(workerErr.Message, rawPath) {
		t.Fatalf("worker error leaked raw path: %#v", workerErr)
	}
	raw, err := json.Marshal(filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		Error: workerErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	var response filesystemWorkerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	got := filesystemWorkerResponseError(response)
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(got, &permissionErr) || permissionErr == nil {
		t.Fatalf("response error = %v, want typed WindowsPermissionError", got)
	}
	if permissionErr.Win32Code() != 5 || permissionErr.Path != "<redacted-path>" {
		t.Fatalf("reconstructed permission error = %#v, want code 5 and redacted path", permissionErr)
	}
}

func TestFilesystemWorkerRawErrnoDoesNotProducePermissionFieldsWindows(t *testing.T) {
	workerErr := classifiedWorkerError(CodeProcessExited, "old directory handle", syscall.Errno(5), InitializationFailureTransient)
	if workerErr.WindowsErrorCode != 0 || workerErr.WindowsPermissionKind != "" {
		t.Fatalf("raw errno produced typed fields: %#v", workerErr)
	}
}

func TestFilesystemWorkerPrivilegeNotHeldWireRoundTripWindows(t *testing.T) {
	workerErr := classifiedWorkerError(
		CodeProcessExited,
		"schema helper filesystem operation",
		securefs.WrapErrorForPath(syscall.Errno(1314), `C:\secret\schema-helper.exe`),
		InitializationFailureTransient,
	)
	raw, err := json.Marshal(filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		Error: workerErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	var response filesystemWorkerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	got := filesystemWorkerResponseError(response)
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(got, &permissionErr) || permissionErr == nil || permissionErr.Win32Code() != 1314 {
		t.Fatalf("response error = %v, want typed WindowsPermissionError code 1314", got)
	}
	if response.Error.WindowsPermissionKind != filesystemWorkerWindowsPrivilegeNotHeldKind {
		t.Fatalf("wire kind = %q, want %q", response.Error.WindowsPermissionKind, filesystemWorkerWindowsPrivilegeNotHeldKind)
	}
}

func TestFilesystemSnapshotOldHandleSeamDoesNotProduceTypedPermissionErrorWindows(t *testing.T) {
	err := syncFilesystemSnapshotDirectoryWithOps("ignored", filesystemSnapshotDirectoryWindowsOps{
		open:  func(string) (windows.Handle, error) { return windows.Handle(1), nil },
		flush: func(windows.Handle) error { return windows.ERROR_ACCESS_DENIED },
		close: func(windows.Handle) error { return nil },
	})
	var permissionErr *securefs.WindowsPermissionError
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("seam error = %v, want ERROR_ACCESS_DENIED", err)
	}
	if errors.As(err, &permissionErr) {
		t.Fatalf("old handle seam was classified as typed ACL error: %v", err)
	}
}

func TestSchemaFilesystemWrapPromotesRealWindowsPermissionWindows(t *testing.T) {
	err := wrapSchemaFilesystemError(`C:\private\schema-cache`, windows.ERROR_PRIVILEGE_NOT_HELD)
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		t.Fatalf("wrapped real permission error = %v, want typed WindowsPermissionError", err)
	}
	if permissionErr.Win32Code() != 1314 || permissionErr.PermissionKind() != securefs.WindowsPrivilegeNotHeld {
		t.Fatalf("wrapped permission error = code=%d kind=%v, want 1314/privilege_not_held", permissionErr.Win32Code(), permissionErr.PermissionKind())
	}
}
