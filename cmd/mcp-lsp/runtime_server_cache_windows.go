//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// runtimeServerHardenPrivateDirectory 设置仅当前用户与 LocalSystem 可访问的受保护 DACL。
func runtimeServerHardenPrivateDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	sddl := "O:" + user.User.Sid.String() +
		"D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build private Windows directory ACL: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read private Windows directory ACL owner: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private Windows directory DACL: %w", err)
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
		return fmt.Errorf("set private Windows directory ACL for %s: %w", path, err)
	}
	return nil
}

func runtimeServerValidatePrivateDirectoryPlatform(path string, _ os.FileInfo) error {
	attributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
	if err != nil {
		return fmt.Errorf("read Windows directory attributes for %s: %w", path, err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("private Windows directory must not be a reparse point: %s", path)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read Windows directory ACL for %s: %w", path, err)
	}
	return runtimeServerValidateWindowsDescriptor(path, descriptor)
}

// runtimeServerValidateWindowsDescriptor 校验 owner、DACL 保护位及允许的 SID 集合。
func runtimeServerValidateWindowsDescriptor(path string, descriptor *windows.SECURITY_DESCRIPTOR) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return fmt.Errorf("private Windows directory owner does not match current user: %s", path)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("private Windows directory DACL is not protected: %s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("private Windows directory DACL is unavailable: %s", path)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve Windows local-system SID: %w", err)
	}
	return runtimeServerValidateWindowsACEs(path, dacl, user.User.Sid, system)
}

// runtimeServerValidateWindowsACEs 拒绝当前用户和 LocalSystem 之外的任何授权 ACE。
func runtimeServerValidateWindowsACEs(path string, dacl *windows.ACL, user, system *windows.SID) error {
	userAllowed := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return fmt.Errorf("read Windows directory ACE for %s: %w", path, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("private Windows directory has unsupported ACE type: %s", path)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user):
			userAllowed = true
		case sid.Equals(system):
		default:
			return fmt.Errorf("private Windows directory grants access to another SID: %s", path)
		}
	}
	if !userAllowed {
		return fmt.Errorf("private Windows directory does not grant current user access: %s", path)
	}
	return nil
}

func runtimeServerTryLockResourceLease(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
}

func runtimeServerUnlockResourceLease(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func runtimeServerResourceLeaseLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
