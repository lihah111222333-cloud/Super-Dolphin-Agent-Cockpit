package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateFilesystemWorkerPermissionFields(t *testing.T) {
	tests := []struct {
		name string
		code uint32
		kind string
		want bool
	}{
		{name: "absent", want: true},
		{name: "access denied", code: filesystemWorkerWindowsAccessDeniedCode, kind: filesystemWorkerWindowsAccessDeniedKind, want: true},
		{name: "privilege not held", code: filesystemWorkerWindowsPrivilegeNotHeldCode, kind: filesystemWorkerWindowsPrivilegeNotHeldKind, want: true},
		{name: "kind without code", kind: filesystemWorkerWindowsAccessDeniedKind},
		{name: "access denied mismatch", code: filesystemWorkerWindowsAccessDeniedCode, kind: filesystemWorkerWindowsPrivilegeNotHeldKind},
		{name: "privilege mismatch", code: filesystemWorkerWindowsPrivilegeNotHeldCode, kind: filesystemWorkerWindowsAccessDeniedKind},
		{name: "unknown code", code: 32, kind: filesystemWorkerWindowsAccessDeniedKind},
		{name: "access denied with surrounding whitespace", code: filesystemWorkerWindowsAccessDeniedCode, kind: " access_denied "},
		{name: "privilege kind uppercase", code: filesystemWorkerWindowsPrivilegeNotHeldCode, kind: "PRIVILEGE_NOT_HELD"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFilesystemWorkerPermissionFields(&filesystemWorkerError{
				WindowsErrorCode: tc.code, WindowsPermissionKind: tc.kind,
			})
			if (err == nil) != tc.want {
				t.Fatalf("validateFilesystemWorkerPermissionFields() error = %v, want valid=%t", err, tc.want)
			}
		})
	}
}

func TestFilesystemWorkerPermissionFieldsAreAbsentWhenUntyped(t *testing.T) {
	raw, err := json.Marshal(filesystemWorkerError{
		Code: CodeProcessExited, Message: "ordinary process failure", FailureClass: InitializationFailureTransient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "windows_error_code") || strings.Contains(string(raw), "windows_permission_kind") {
		t.Fatalf("untyped worker error emitted Windows permission fields: %s", raw)
	}
}
