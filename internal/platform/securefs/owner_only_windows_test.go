//go:build windows

package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSyncDirectoryWindows(t *testing.T) {
	dir := t.TempDir()
	if err := SyncDirectory(dir); err != nil {
		t.Fatalf("SyncDirectory(dir) error = %v", err)
	}
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SyncDirectory(filePath); err == nil {
		t.Fatal("SyncDirectory(file) error = nil, want non-directory error")
	}
}

func TestSyncDirectoryWindowsExecutesFlushAndClose(t *testing.T) {
	var opened, flushed, closed bool
	wantErr := windows.ERROR_ACCESS_DENIED
	err := syncDirectoryWithOps("ignored", syncDirectoryWindowsOps{
		open: func(string) (windows.Handle, error) {
			opened = true
			return windows.Handle(1), nil
		},
		flush: func(windows.Handle) error {
			flushed = true
			return wantErr
		},
		close: func(windows.Handle) error {
			closed = true
			return nil
		},
	})
	if !opened || !flushed || !closed {
		t.Fatalf("syncDirectoryWithOps() calls = open:%v flush:%v close:%v, want all true", opened, flushed, closed)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("syncDirectoryWithOps() error = %v, want flush error %v in chain", err, wantErr)
	}
}

func TestSyncDirectoryWindowsPromotesPermissionErrorsFromSyncOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	tests := []struct {
		name      string
		operation string
		code      uint32
	}{
		{name: "open_access_denied", operation: "open", code: 5},
		{name: "open_privilege_not_held", operation: "open", code: 1314},
		{name: "flush_access_denied", operation: "flush", code: 5},
		{name: "flush_privilege_not_held", operation: "flush", code: 1314},
		{name: "close_access_denied", operation: "close", code: 5},
		{name: "close_privilege_not_held", operation: "close", code: 1314},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			raw := syscall.Errno(testCase.code)
			err := syncDirectoryWithOps(path, syncDirectoryWindowsOps{
				open: func(string) (windows.Handle, error) {
					if testCase.operation == "open" {
						return windows.InvalidHandle, raw
					}
					return windows.Handle(1), nil
				},
				flush: func(windows.Handle) error {
					if testCase.operation == "flush" {
						return raw
					}
					return nil
				},
				close: func(windows.Handle) error {
					if testCase.operation == "close" {
						return raw
					}
					return nil
				},
			})
			var permissionErr *WindowsPermissionError
			if !errors.As(err, &permissionErr) || permissionErr == nil {
				t.Fatalf("syncDirectoryWithOps() error = %v, want WindowsPermissionError", err)
			}
			if got := permissionErr.Win32Code(); got != testCase.code {
				t.Fatalf("WindowsPermissionError code = %d, want %d", got, testCase.code)
			}
			if containsSecurefsPathToken(err.Error(), path, filepath.Dir(path)) {
				t.Fatalf("syncDirectoryWithOps() leaked raw path: %q", err.Error())
			}
		})
	}
}

func TestSyncDirectoryWindowsKeepsNonPermissionErrorChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	sentinel := errors.New("flush failed")
	err := syncDirectoryWithOps(path, syncDirectoryWindowsOps{
		open: func(string) (windows.Handle, error) {
			return windows.Handle(1), nil
		},
		flush: func(windows.Handle) error {
			return sentinel
		},
		close: func(windows.Handle) error {
			return nil
		},
	})
	var permissionErr *WindowsPermissionError
	if errors.As(err, &permissionErr) {
		t.Fatalf("syncDirectoryWithOps() promoted non-permission error: %#v", permissionErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("syncDirectoryWithOps() error = %v, want sentinel in chain", err)
	}
	if containsSecurefsPathToken(err.Error(), path, filepath.Dir(path)) {
		t.Fatalf("syncDirectoryWithOps() leaked raw path: %q", err.Error())
	}
}

func TestWrapErrorForPathPromotesWindowsPermissionCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	for _, code := range []uint32{5, 1314} {
		t.Run(fmt.Sprintf("windows_code_%d", code), func(t *testing.T) {
			raw := &os.PathError{Op: "open", Path: path, Err: syscall.Errno(code)}
			wrapped := WrapErrorForPath(raw, path)

			var permissionErr *WindowsPermissionError
			if !errors.As(wrapped, &permissionErr) || permissionErr == nil {
				t.Fatalf("WrapErrorForPath() did not promote Windows code %d: %v", code, wrapped)
			}
			if got := permissionErr.Win32Code(); got != code {
				t.Fatalf("promoted Windows code = %d, want %d", got, code)
			}
			if got := wrapped.Error(); containsSecurefsPathToken(got, path, filepath.Dir(path)) {
				t.Fatalf("promoted error leaked path: %q", got)
			}
		})
	}
}

func TestWrapErrorForPathDoesNotReplaceTypedWindowsPermissionError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	typed := NewWindowsPermissionError("ACL check", path, syscall.Errno(5))
	wrapped := WrapErrorForPath(typed, path)
	var permissionErr *WindowsPermissionError
	if !errors.As(wrapped, &permissionErr) || permissionErr == nil {
		t.Fatalf("typed Windows permission error was lost: %v", wrapped)
	}
	if permissionErr != typed.(*WindowsPermissionError) {
		t.Fatalf("WrapErrorForPath() replaced an existing typed error")
	}
}

func TestRestrictOwnerOnlyKeepsCurrentUserWritable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RestrictOwnerOnly(dir, 0o700); err != nil {
		t.Fatalf("RestrictOwnerOnly(dir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("restricted directory is not writable by current user: %v", err)
	}

	dbPath := filepath.Join(dir, "super-dolphin.db")
	if err := os.WriteFile(dbPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestrictOwnerOnly(dbPath, 0o600); err != nil {
		t.Fatalf("RestrictOwnerOnly(file) error = %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("restricted file is not writable by current user: %v", err)
	}
}

func TestCheckExistingOwnerOnlyRejectsBroadWriteACEWithRedactedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "super-dolphin.db")
	if err := os.WriteFile(path, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	userSID, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		allowSID(userSID, windows.TRUSTEE_IS_USER, 0),
		allowSID(usersSID, windows.TRUSTEE_IS_ALIAS, 0),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	err = CheckExistingOwnerOnly(path, nil)
	if err == nil {
		t.Fatal("CheckExistingOwnerOnly() error = nil, want broad write ACE rejection")
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("CheckExistingOwnerOnly() leaked raw path: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:super-dolphin.db>") {
		t.Fatalf("CheckExistingOwnerOnly() error = %v, want redacted path", err)
	}
}
