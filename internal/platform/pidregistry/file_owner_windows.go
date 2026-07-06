//go:build windows

package pidregistry

import (
	"os"

	"golang.org/x/sys/windows"
)

func registryFileOwnedByCurrentUser(path string, _ os.FileInfo) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	return windows.EqualSid(owner, user.User.Sid)
}
