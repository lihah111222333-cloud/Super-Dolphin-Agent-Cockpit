//go:build windows

package ownerperms

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// ValidateOwnerIdentitySaltPermissions 校验owner身份saltpermissions。
func ValidateOwnerIdentitySaltPermissions(path string, info os.FileInfo) error {
	if info.Size() == 0 {
		return fmt.Errorf("owner identity salt is empty")
	}
	return ValidateOwnerOnlyFilePermissions(path, info, "owner identity salt")
}

// SecureOwnerIdentitySaltPermissions 处理secureowner身份saltpermissions。
func SecureOwnerIdentitySaltPermissions(path string) error {
	return SecureOwnerOnlyFilePermissions(path)
}

// ValidateOwnerOnlyFilePermissions 校验owneronly文件permissions。
func ValidateOwnerOnlyFilePermissions(path string, info os.FileInfo, label string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", label)
	}
	if err := validateCurrentUserCanReadWriteFile(path, label); err != nil {
		return err
	}
	currentSID, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	sddl, err := fileSDDL(path)
	if err != nil {
		return fmt.Errorf("read %s ACL: %w", label, err)
	}
	if bad := firstBroadReadableWritableACE(sddl, currentSID); bad != "" {
		return fmt.Errorf("%s permissions ACL grants read/write to broad principal %s", label, bad)
	}
	return nil
}

// SecureOwnerOnlyFilePermissions 处理secureowneronly文件permissions。
func SecureOwnerOnlyFilePermissions(path string) error {
	currentSID, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	if err := runICACLS(path, "/inheritance:r"); err != nil {
		return err
	}
	if err := runICACLS(path, "/grant:r", "*"+currentSID+":(R,W)", "*S-1-5-18:(F)", "*S-1-5-32-544:(F)"); err != nil {
		return err
	}
	for range 32 {
		badSID, err := firstBroadReadableWritableFileACE(path, currentSID)
		if err != nil {
			return err
		}
		if badSID == "" {
			return nil
		}
		if err := runICACLS(path, "/remove:g", icaclsPrincipal(badSID)); err != nil {
			return err
		}
	}
	return fmt.Errorf("owner-only file ACL still contains broad principals after cleanup")
}

func validateCurrentUserCanReadWriteFile(path, label string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("%s is not readable/writable by current owner: %w", label, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s permission probe: %w", label, err)
	}
	return nil
}

func runICACLS(path string, args ...string) error {
	cmdArgs := append([]string{path}, args...)
	cmd := exec.Command("icacls", cmdArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("secure owner-only ACL: icacls %s: %w; output=%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func currentProcessUserSID() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("open current process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read current process user SID: %w", err)
	}
	return user.User.Sid.String(), nil
}

func firstBroadReadableWritableFileACE(path, currentSID string) (string, error) {
	sddl, err := fileSDDL(path)
	if err != nil {
		return "", fmt.Errorf("read owner-only file ACL: %w", err)
	}
	return firstBroadReadableWritableACE(sddl, currentSID), nil
}

func fileSDDL(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION,
	)
	if err != nil {
		return "", err
	}
	return sd.String(), nil
}

func firstBroadReadableWritableACE(sddl, currentSID string) string {
	for _, ace := range parseSDDLACEs(sddl) {
		if ace.kind != "A" || !sddlRightsReadOrWrite(ace.rights) {
			continue
		}
		if allowedOwnerOnlyPrincipal(ace.sid, currentSID) {
			continue
		}
		return ace.sid
	}
	return ""
}

type sddlACE struct {
	kind   string
	rights string
	sid    string
}

func parseSDDLACEs(sddl string) []sddlACE {
	var aces []sddlACE
	for {
		start := strings.IndexByte(sddl, '(')
		if start < 0 {
			return aces
		}
		sddl = sddl[start+1:]
		end := strings.IndexByte(sddl, ')')
		if end < 0 {
			return aces
		}
		raw := sddl[:end]
		sddl = sddl[end+1:]
		parts := strings.Split(raw, ";")
		if len(parts) >= 6 {
			aces = append(aces, sddlACE{kind: parts[0], rights: parts[2], sid: parts[5]})
		}
	}
}

func allowedOwnerOnlyPrincipal(sid, currentSID string) bool {
	switch strings.ToUpper(strings.TrimSpace(sid)) {
	case "SY", "BA":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(sid), currentSID)
}

func sddlRightsReadOrWrite(rights string) bool {
	upper := strings.ToUpper(strings.TrimSpace(rights))
	for _, token := range []string{"FA", "GA", "GR", "GW", "FR", "FW"} {
		if strings.Contains(upper, token) {
			return true
		}
	}
	if !strings.HasPrefix(upper, "0X") {
		return false
	}
	mask, err := strconv.ParseUint(upper[2:], 16, 32)
	if err != nil {
		return true
	}
	const readWriteMask = uint64(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.GENERIC_READ | windows.GENERIC_WRITE | windows.FILE_READ_DATA | windows.FILE_WRITE_DATA)
	return mask&readWriteMask != 0
}

func icaclsPrincipal(sid string) string {
	sid = strings.TrimSpace(sid)
	if strings.HasPrefix(strings.ToUpper(sid), "S-") {
		return "*" + sid
	}
	return sid
}
