//go:build windows

package securefs

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CheckPrivateOwnerOnly 校验 Windows 路径只向当前用户与 LocalSystem 授权。
func CheckPrivateOwnerOnly(path string, info os.FileInfo) error {
	if info == nil {
		var err error
		info, err = os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect private path %s: %s", RedactPath(path), SafeError(err))
		}
	}
	if err := checkWindowsPrivatePathAttributes(path); err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read private path ACL %s: %s", RedactPath(path), SafeErrorForPath(err, path))
	}
	return checkWindowsPrivateDescriptor(path, info, descriptor)
}

// RestrictPrivateOwnerOnly 设置当前用户与 LocalSystem 专用的受保护 DACL 并重新校验。
func RestrictPrivateOwnerOnly(path string, _ os.FileMode) error {
	if err := checkWindowsPrivatePathAttributes(path); err != nil {
		return err
	}
	userSID, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	inheritance := ""
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private path %s: %s", RedactPath(path), SafeError(err))
	}
	if info.IsDir() {
		inheritance = "OICI"
	}
	sddl := "O:" + userSID.String() +
		"D:P(A;" + inheritance + ";FA;;;SY)(A;" + inheritance + ";FA;;;" + userSID.String() + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build private path ACL: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read private path ACL owner: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private path DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("set private path ACL %s: %s", RedactPath(path), SafeErrorForPath(err, path))
	}
	return CheckPrivateOwnerOnly(path, info)
}

func checkWindowsPrivatePathAttributes(path string) error {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode private Windows path %s: %w", RedactPath(path), err)
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil {
		return fmt.Errorf("read private path attributes %s: %s", RedactPath(path), SafeErrorForPath(err, path))
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("private path is a reparse point: %s", RedactPath(path))
	}
	return nil
}

// checkWindowsPrivateDescriptor 校验 owner、目录保护位和严格授权 SID 集合。
func checkWindowsPrivateDescriptor(path string, info os.FileInfo, descriptor *windows.SECURITY_DESCRIPTOR) error {
	userSID, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	if err := checkWindowsPrivateOwner(path, descriptor, userSID); err != nil {
		return err
	}
	if err := checkWindowsPrivateDirectoryControl(path, info, descriptor); err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private path DACL %s: %w", RedactPath(path), err)
	}
	if dacl == nil {
		return fmt.Errorf("private path DACL is unavailable: %s", RedactPath(path))
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve Windows LocalSystem SID: %w", err)
	}
	return checkWindowsPrivateACEs(path, dacl, userSID, systemSID)
}

// checkWindowsPrivateOwner 要求私有路径 owner 与当前进程用户一致。
func checkWindowsPrivateOwner(path string, descriptor *windows.SECURITY_DESCRIPTOR, userSID *windows.SID) error {
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read private path owner %s: %w", RedactPath(path), err)
	}
	if owner == nil || !owner.Equals(userSID) {
		return fmt.Errorf("private path owner does not match current user: %s", RedactPath(path))
	}
	return nil
}

// checkWindowsPrivateDirectoryControl 要求目录使用受保护 DACL，文件可继承已验证父目录的严格 ACL。
func checkWindowsPrivateDirectoryControl(path string, info os.FileInfo, descriptor *windows.SECURITY_DESCRIPTOR) error {
	if !info.IsDir() {
		return nil
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("read private directory DACL control %s: %w", RedactPath(path), err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("private directory DACL is not protected: %s", RedactPath(path))
	}
	return nil
}

func checkWindowsPrivateACEs(path string, dacl *windows.ACL, userSID, systemSID *windows.SID) error {
	var userAccess windows.ACCESS_MASK
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return fmt.Errorf("read private path ACE %s: %w", RedactPath(path), err)
		}
		access, err := windowsPrivateUserAccess(path, ace, userSID, systemSID)
		if err != nil {
			return err
		}
		userAccess |= access
	}
	const requiredUserAccess windows.ACCESS_MASK = windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_WRITE |
		windows.DELETE
	if userAccess&requiredUserAccess != requiredUserAccess {
		return fmt.Errorf("private path does not grant current user read/write access: %s", RedactPath(path))
	}
	return nil
}

// windowsPrivateUserAccess 校验单条 ACE，并返回其中授予当前用户的权限。
func windowsPrivateUserAccess(
	path string,
	ace *windows.ACCESS_ALLOWED_ACE,
	userSID, systemSID *windows.SID,
) (windows.ACCESS_MASK, error) {
	if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return 0, fmt.Errorf("private path has unsupported ACE type: %s", RedactPath(path))
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if sid.Equals(userSID) {
		return ace.Mask, nil
	}
	if sid.Equals(systemSID) {
		return 0, nil
	}
	return 0, fmt.Errorf("private path grants access to another SID: %s", RedactPath(path))
}
