//go:build windows

package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestRestrictPrivateOwnerOnlyWindowsRoundTrip 验证正常 NTFS DACL 不依赖 POSIX mode bits。
func TestRestrictPrivateOwnerOnlyWindowsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := RestrictPrivateOwnerOnly(dir, 0o700); err != nil {
		t.Fatalf("RestrictPrivateOwnerOnly(dir) error = %v", err)
	}
	path := filepath.Join(dir, "lease.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := RestrictPrivateOwnerOnly(path, 0o600); err != nil {
		t.Fatalf("RestrictPrivateOwnerOnly(file) error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat fixture: %v", err)
	}
	if err := CheckPrivateOwnerOnly(path, info); err != nil {
		t.Fatalf("CheckPrivateOwnerOnly() error = %v", err)
	}
}

// TestWindowsSecurityOperationErrorPreservesCodeAndRedactsPath 验证 ACL 诊断可定位 Win32 阶段且不泄露完整路径。
func TestWindowsSecurityOperationErrorPreservesCodeAndRedactsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-cache")
	err := newWindowsSecurityOperationError(
		"read private path ACL",
		"get_named_security_info",
		path,
		windows.ERROR_ACCESS_DENIED,
	)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("error chain lost ERROR_ACCESS_DENIED: %v", err)
	}
	message := err.Error()
	for _, want := range []string{
		"windows_operation=get_named_security_info",
		"windows_error_code=5",
		RedactPath(path),
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("diagnostic error missing %q: %s", want, message)
		}
	}
	if strings.Contains(message, filepath.Dir(path)) {
		t.Fatalf("diagnostic error leaked parent path: %s", message)
	}
}

// TestCheckPrivateOwnerOnlyWindowsRejectsOtherPrincipals 验证宽泛或管理员 SID 授权仍然 fail-fast。
func TestCheckPrivateOwnerOnlyWindowsRejectsOtherPrincipals(t *testing.T) {
	for name, sidAlias := range map[string]string{
		"administrators": "BA",
		"everyone":       "WD",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lease.json")
			if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if err := setBroadPrivateOwnerOnlyTestACL(path, sidAlias); err != nil {
				t.Fatalf("set broad fixture ACL: %v", err)
			}
			if err := CheckPrivateOwnerOnly(path, nil); err == nil {
				t.Fatalf("CheckPrivateOwnerOnly() accepted %s access", name)
			}
		})
	}
}

func setBroadPrivateOwnerOnlyTestACL(path string, sidAlias string) error {
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	sddl := "O:" + userSID.String() +
		"D:P(A;;FA;;;SY)(A;;FA;;;" + userSID.String() + ")(A;;FA;;;" + sidAlias + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner,
		nil,
		dacl,
		nil,
	)
}
