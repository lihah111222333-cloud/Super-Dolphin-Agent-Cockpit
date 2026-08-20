//go:build windows

package pidregistry

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestTrustedRegistryFileInfoRejectsBroadWindowsACL 验证同 owner 文件向 Everyone 授权后不再可信。
func TestTrustedRegistryFileInfoRejectsBroadWindowsACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("{}"), registryFilePerm); err != nil {
		t.Fatalf("write registry fixture: %v", err)
	}
	if err := grantRegistryEveryoneAccess(path); err != nil {
		t.Fatalf("grant broad registry ACL: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat registry fixture: %v", err)
	}
	if trustedRegistryFileInfo(path, info) {
		t.Fatal("trustedRegistryFileInfo() accepted registry file accessible by Everyone")
	}
}

func grantRegistryEveryoneAccess(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sddl := "O:" + user.User.Sid.String() +
		"D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)"
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
