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
