//go:build windows && e2e

package processobserve

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestValidateWindowsPrivateACEsRejectsUnsafeDerivationE2E 验证仅可信 SID 不足以构成可安全派生的 DACL。
func TestValidateWindowsPrivateACEsRejectsUnsafeDerivationE2E(t *testing.T) {
	user, system := mustWindowsTestUserAndSystemSIDs(t)
	assertWindowsPrivateACEsRejected(
		t,
		"insufficient access mask",
		"D:P(A;OICI;FR;;;SY)(A;OICI;FR;;;"+user.String()+")",
		user,
		system,
	)
	assertWindowsPrivateACEsRejected(
		t,
		"missing inheritance flags",
		"D:P(A;;FA;;;SY)(A;;FA;;;"+user.String()+")",
		user,
		system,
	)
}

// TestCreateWindowsDurableTempRemovesCreatedFileWhenSecurityValidationFailsE2E 验证创建后校验失败不会遗留临时文件。
func TestCreateWindowsDurableTempRemovesCreatedFileWhenSecurityValidationFailsE2E(t *testing.T) {
	rootPath := t.TempDir()
	user, _ := mustWindowsTestUserAndSystemSIDs(t)
	dacl := mustWindowsTestDACL(t, "D:P(A;OICI;FA;;;"+user.String()+")")
	if err := windows.SetNamedSecurityInfo(
		rootPath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatalf("set current-user-only test DACL: %v", err)
	}

	_, _, file, err := createWindowsDurableTemp(&secureRoot{path: rootPath})
	if file != nil {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close unexpectedly returned temporary file: %v", closeErr)
		}
	}
	if err == nil {
		t.Fatal("createWindowsDurableTemp() error = nil, want post-CREATE_NEW security validation failure")
	}
	if !strings.Contains(err.Error(), "missing current-user or local-system access") {
		t.Fatalf("createWindowsDurableTemp() error = %v, want missing SYSTEM access", err)
	}

	assertNoWindowsDurableTempFiles(t, rootPath)
}

func mustWindowsTestUserAndSystemSIDs(t *testing.T) (*windows.SID, *windows.SID) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser(): %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(LocalSystem): %v", err)
	}
	return user.User.Sid, system
}

func mustWindowsTestDACL(t *testing.T, sddl string) *windows.ACL {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString(%q): %v", sddl, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("read test DACL: %v", err)
	}
	return dacl
}

func assertWindowsPrivateACEsRejected(t *testing.T, reason, sddl string, user, system *windows.SID) {
	t.Helper()
	dacl := mustWindowsTestDACL(t, sddl)
	if err := validateWindowsPrivateACEsForObject(dacl, user, system, true); err == nil {
		t.Errorf("validateWindowsPrivateACEsForObject() accepted directory DACL with %s", reason)
	}
}

func assertNoWindowsDurableTempFiles(t *testing.T, rootPath string) {
	t.Helper()
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", rootPath, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".incident-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file remained after security validation failure: %q", entry.Name())
		}
	}
}
