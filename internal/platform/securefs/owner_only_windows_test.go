//go:build windows

package securefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

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
