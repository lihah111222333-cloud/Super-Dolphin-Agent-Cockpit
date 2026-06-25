//go:build windows

// Package ownerperms 的 Windows 实现，通过 ICACLS 和 Windows Security API 校验并加固文件 ACL。
package ownerperms

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// ValidateOwnerIdentitySaltPermissions 校验 owner identity salt 文件不为空且 ACL 仅允许当前用户读写。
func ValidateOwnerIdentitySaltPermissions(path string, info os.FileInfo) error {
	if info.Size() == 0 {
		return fmt.Errorf("owner identity salt is empty")
	}
	return ValidateOwnerOnlyFilePermissions(path, info, "owner identity salt")
}

// SecureOwnerIdentitySaltPermissions 将 owner identity salt 文件的 ACL 加固为仅当前用户可读写。
func SecureOwnerIdentitySaltPermissions(path string) error {
	return SecureOwnerOnlyFilePermissions(path)
}

// ValidateOwnerOnlyFilePermissions 校验文件为普通文件，且 ACL 中不存在向非 owner 广播读写权限的条目。
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

// SecureOwnerOnlyFilePermissions 通过 ICACLS 移除继承，授权当前用户、SYSTEM 和 Administrators 后，
// 循环最多 32 次清除剩余的广播 ACE，确保文件 ACL 仅剩 owner-only 条目。
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

// validateCurrentUserCanReadWriteFile 通过实际打开文件验证当前用户具有读写权限。
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

// runICACLS 执行 icacls 命令对指定路径应用 ACL 操作，失败时返回包含输出的错误。
func runICACLS(path string, args ...string) error {
	cmdArgs := append([]string{path}, args...)
	cmd := exec.Command("icacls", cmdArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("secure owner-only ACL: icacls %s: %w; output=%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// currentProcessUserSID 获取当前进程用户的 Windows SID 字符串，用于 ACL 比较和授权。
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

// firstBroadReadableWritableFileACE 读取文件的 SDDL 并返回第一个向非 owner 广播读写权限的 ACE SID。
func firstBroadReadableWritableFileACE(path, currentSID string) (string, error) {
	sddl, err := fileSDDL(path)
	if err != nil {
		return "", fmt.Errorf("read owner-only file ACL: %w", err)
	}
	return firstBroadReadableWritableACE(sddl, currentSID), nil
}

// fileSDDL 获取指定文件的 SDDL 字符串（包含 DACL、Owner、Group 信息）。
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

// firstBroadReadableWritableACE 遍历 SDDL 中的 ACE，返回第一个向非 owner 广播读写权限的 SID。
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

// sddlACE 表示 SDDL 中一条访问控制条目的关键字段。
type sddlACE struct {
	kind   string // ACE 类型，如 "A"（允许）或 "D"（拒绝）
	rights string // 权限掩码或权限标识，如 "FA"、"0x1200a9"
	sid    string // 被授权的主体 SID 或缩写，如 "SY"、"BA"
}

// parseSDDLACEs 从 SDDL 字符串中解析出所有 ACE 条目列表。
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

// allowedOwnerOnlyPrincipal 判断 SID 是否为允许的 owner-only 主体（SY=SYSTEM、BA=Administrators 或当前用户）。
func allowedOwnerOnlyPrincipal(sid, currentSID string) bool {
	switch strings.ToUpper(strings.TrimSpace(sid)) {
	case "SY", "BA":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(sid), currentSID)
}

// sddlRightsReadOrWrite 判断 SDDL 权限字符串是否包含读或写权限（支持文字标记和十六进制掩码）。
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

// icaclsPrincipal 将 SID 字符串转换为 icacls 可用的主体格式（S- 开头的 SID 加 * 前缀）。
func icaclsPrincipal(sid string) string {
	sid = strings.TrimSpace(sid)
	if strings.HasPrefix(strings.ToUpper(sid), "S-") {
		return "*" + sid
	}
	return sid
}
