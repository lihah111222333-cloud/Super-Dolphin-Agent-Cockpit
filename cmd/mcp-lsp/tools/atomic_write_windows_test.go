//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

// TestAtomicReplaceFileWindowsDoesNotSyncDirectory 验证 Windows 替换成功后不会因目录 FlushFileBuffers 误报失败。
func TestAtomicReplaceFileWindowsDoesNotSyncDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.ts")
	if err := os.WriteFile(path, []byte("const value = 'old';\r\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	if err := atomicReplaceFile(path, []byte("const value = 'new';\r\n"), info.Mode(), defaultFileWriter); err != nil {
		t.Fatalf("atomicReplaceFile() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if got, want := string(raw), "const value = 'new';\r\n"; got != want {
		t.Fatalf("replaced content = %q, want %q", got, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixture directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".source.ts.tmp-") {
			t.Fatalf("temporary edit file was retained: %s", entry.Name())
		}
	}
}

// TestAtomicReplaceFileWindowsPreservesPrivateDACL 验证替换不会把父目录的宽泛继承 ACL 带到目标文件。
func TestAtomicReplaceFileWindowsPreservesPrivateDACL(t *testing.T) {
	dir := t.TempDir()
	if err := setAtomicWriteBroadInheritedACL(dir); err != nil {
		t.Fatalf("set broad parent ACL: %v", err)
	}
	path := filepath.Join(dir, "private-source.ts")
	if err := os.WriteFile(path, []byte("const value = 'old';\r\n"), 0o600); err != nil {
		t.Fatalf("write private fixture: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(path, 0o600); err != nil {
		t.Fatalf("restrict private fixture: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private fixture: %v", err)
	}
	if err := atomicReplaceFile(path, []byte("const value = 'new';\r\n"), info.Mode(), defaultFileWriter); err != nil {
		t.Fatalf("atomicReplaceFile() error = %v", err)
	}
	if err := securefs.CheckPrivateOwnerOnly(path, nil); err != nil {
		t.Fatalf("atomic replacement widened target DACL: %v", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read replaced target DACL: %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read replaced target DACL control: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("atomic replacement dropped protected DACL control")
	}
}

// setAtomicWriteBroadInheritedACL 为测试父目录设置可继承的宽泛 Everyone ACL。
func setAtomicWriteBroadInheritedACL(path string) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	userSID, err := windows.StringToSid(user.User.Sid.String())
	if err != nil {
		return err
	}
	sddl := "O:" + userSID.String() +
		"D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + userSID.String() + ")(A;OICI;FA;;;WD)"
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
