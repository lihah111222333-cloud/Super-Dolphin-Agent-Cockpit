//go:build windows

package securefs

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func CheckExistingOwnerOnly(path string, info os.FileInfo) error {
	if info != nil && !info.IsDir() && info.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("SQLite path is read-only: %s", RedactPath(path))
	}
	if err := checkWindowsOwnerOnlyACL(path); err != nil {
		return fmt.Errorf("SQLite path ACL is not current-user-only: %s: %s", RedactPath(path), SafeErrorForPath(err, path))
	}
	return nil
}

func RestrictOwnerOnly(path string, _ os.FileMode) error {
	if err := setWindowsOwnerOnlyACL(path); err != nil {
		return fmt.Errorf("restrict path ACL %s: %s", RedactPath(path), SafeErrorForPath(err, path))
	}
	return CheckExistingOwnerOnly(path, nil)
}

func setWindowsOwnerOnlyACL(path string) error {
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	inheritance := uint32(0)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	} else if err != nil {
		return err
	}
	entries := []windows.EXPLICIT_ACCESS{
		allowSID(userSID, windows.TRUSTEE_IS_USER, inheritance),
		allowSID(adminSID, windows.TRUSTEE_IS_ALIAS, inheritance),
		allowSID(systemSID, windows.TRUSTEE_IS_UNKNOWN, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func checkWindowsOwnerOnlyACL(path string) error {
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil || dacl.AceCount == 0 {
		return fmt.Errorf("DACL has no explicit allow entries")
	}
	currentUserCanWrite, err := scanDACLForUnsafeWrite(dacl, userSID)
	if err != nil {
		return err
	}
	if !currentUserCanWrite {
		return fmt.Errorf("DACL does not grant current user write access")
	}
	return nil
}

func scanDACLForUnsafeWrite(dacl *windows.ACL, userSID *windows.SID) (bool, error) {
	currentUserCanWrite := false
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return false, err
		}
		canWrite, err := inspectAllowedACE(ace, userSID)
		if err != nil {
			return false, err
		}
		currentUserCanWrite = currentUserCanWrite || canWrite
	}
	return currentUserCanWrite, nil
}

func inspectAllowedACE(ace *windows.ACCESS_ALLOWED_ACE, userSID *windows.SID) (bool, error) {
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return false, nil
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if sid.Equals(userSID) {
		return grantsWrite(ace.Mask), nil
	}
	if sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
		sid.IsWellKnown(windows.WinLocalSystemSid) {
		return false, nil
	}
	if grantsWrite(ace.Mask) {
		return false, fmt.Errorf("DACL grants write access to broad or non-owner principal")
	}
	return false, nil
}

func allowSID(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func grantsWrite(mask windows.ACCESS_MASK) bool {
	const writeMask windows.ACCESS_MASK = windows.GENERIC_ALL |
		windows.GENERIC_WRITE |
		windows.FILE_GENERIC_WRITE |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA |
		windows.FILE_WRITE_ATTRIBUTES |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER
	return mask&writeMask != 0
}

func currentUserSID() (*windows.SID, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.StringToSid(user.User.Sid.String())
}
