//go:build windows

package securefs

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// syncDirectoryWindowsOps 为 Windows 目录同步封装可测试的系统调用。
type syncDirectoryWindowsOps struct {
	open  func(string) (windows.Handle, error)
	flush func(windows.Handle) error
	close func(windows.Handle) error
}

// syncDirectory 在 Windows 平台校验目录后用可写句柄刷新目录并关闭句柄。
func syncDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return WrapErrorForPath(fmt.Errorf("stat directory for sync %s: %w", RedactPath(path), err), path)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory for sync: %s", RedactPath(path))
	}
	return syncDirectoryWithOps(path, syncDirectoryWindowsOps{
		open:  openSyncDirectory,
		flush: windows.FlushFileBuffers,
		close: windows.CloseHandle,
	})
}

// syncDirectoryWithOps 执行目录同步调用并保留 FlushFileBuffers 与 CloseHandle 的错误链。
func syncDirectoryWithOps(path string, ops syncDirectoryWindowsOps) error {
	if ops.open == nil || ops.flush == nil || ops.close == nil {
		return errors.New("sync directory operations are incomplete")
	}
	handle, err := ops.open(path)
	if err != nil {
		return WrapErrorForPath(fmt.Errorf("open directory for sync %s: %w", RedactPath(path), err), path)
	}
	flushErr := ops.flush(handle)
	closeErr := ops.close(handle)
	if err := errors.Join(flushErr, closeErr); err != nil {
		return WrapErrorForPath(fmt.Errorf("sync directory %s: %w", RedactPath(path), err), path)
	}
	return nil
}

// openSyncDirectory 以可写备份语义句柄打开目录，供 FlushFileBuffers 使用。
func openSyncDirectory(path string) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("encode directory path: %w", err)
	}
	return windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
}

func wrapWindowsPermissionError(err error, path string) error {
	var permissionErr *WindowsPermissionError
	if errors.As(err, &permissionErr) && permissionErr != nil {
		return err
	}
	code, ok := windowsErrorCode(err)
	if !ok || (code != 5 && code != 1314) {
		return err
	}
	return NewWindowsPermissionError("filesystem permission operation", path, err)
}

// CheckExistingOwnerOnly 校验 Windows 路径 ACL 只允许当前用户、Administrators 和 SYSTEM 写入。
func CheckExistingOwnerOnly(path string, info os.FileInfo) error {
	if info != nil && !info.IsDir() && info.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("SQLite path is read-only: %s", RedactPath(path))
	}
	if err := checkWindowsOwnerOnlyACL(path); err != nil {
		return fmt.Errorf("SQLite path ACL is not current-user-only: %s: %w", RedactPath(path), err)
	}
	return nil
}

// RestrictOwnerOnly 重写 DACL 后再次执行 owner-only 校验。
func RestrictOwnerOnly(path string, _ os.FileMode) error {
	if err := setWindowsOwnerOnlyACL(path); err != nil {
		return fmt.Errorf("restrict path ACL %s: %w", RedactPath(path), err)
	}
	return CheckExistingOwnerOnly(path, nil)
}

// setWindowsOwnerOnlyACL 为文件或目录设置受保护 DACL，目录会向子项继承。
func setWindowsOwnerOnlyACL(path string) error {
	userSID, err := currentUserSIDForPath(path)
	if err != nil {
		return fmt.Errorf("resolve current Windows user SID: %w", err)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return newWindowsSecurityOperationError("resolve Windows Administrators SID", "create_well_known_sid", path, err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return newWindowsSecurityOperationError("resolve Windows LocalSystem SID", "create_well_known_sid", path, err)
	}
	inheritance := uint32(0)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	} else if err != nil {
		return newWindowsSecurityOperationError("inspect owner-only path", "stat", path, err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		allowSID(userSID, windows.TRUSTEE_IS_USER, inheritance),
		allowSID(adminSID, windows.TRUSTEE_IS_ALIAS, inheritance),
		allowSID(systemSID, windows.TRUSTEE_IS_UNKNOWN, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return newWindowsSecurityOperationError("build owner-only ACL", "acl_from_entries", path, err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return newWindowsSecurityOperationError("set owner-only path ACL", "set_named_security_info", path, err)
	}
	return nil
}

// checkWindowsOwnerOnlyACL 读取现有 DACL 并确认当前用户具备写权限且没有宽泛写授权。
func checkWindowsOwnerOnlyACL(path string) error {
	userSID, err := currentUserSIDForPath(path)
	if err != nil {
		return fmt.Errorf("resolve current Windows user SID: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return newWindowsSecurityOperationError("read owner-only path ACL", "get_named_security_info", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return newWindowsSecurityOperationError("read owner-only path DACL", "read_dacl", path, err)
	}
	if dacl == nil || dacl.AceCount == 0 {
		return fmt.Errorf("DACL has no explicit allow entries")
	}
	currentUserCanWrite, err := scanDACLForUnsafeWrite(path, dacl, userSID)
	if err != nil {
		return err
	}
	if !currentUserCanWrite {
		return fmt.Errorf("DACL does not grant current user write access")
	}
	return nil
}

// scanDACLForUnsafeWrite 遍历 allow ACE，发现非 owner 宽泛写权限时立即报错。
func scanDACLForUnsafeWrite(path string, dacl *windows.ACL, userSID *windows.SID) (bool, error) {
	currentUserCanWrite := false
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return false, newWindowsSecurityOperationError("read owner-only path ACE", "get_ace", path, err)
		}
		canWrite, err := inspectAllowedACE(ace, userSID)
		if err != nil {
			return false, err
		}
		currentUserCanWrite = currentUserCanWrite || canWrite
	}
	return currentUserCanWrite, nil
}

// inspectAllowedACE 判断单条 allow ACE 是否授予当前用户写权限或暴露不安全写权限。
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

// allowSID 构造授予指定 SID 完全控制权的 ACL 条目。
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

// grantsWrite 判断访问掩码是否包含任何可修改文件或 ACL 的权限。
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

// currentUserSID 返回当前进程 token 里的用户 SID，供不需要路径上下文的测试使用。
func currentUserSID() (*windows.SID, error) {
	return currentUserSIDForPath("")
}

// currentUserSIDForPath 从当前进程 token 解析用户 SID，并把 5/1314 保留为 typed 权限错误。
// OpenCurrentProcessToken/GetTokenUser 失败时立即返回，不提权、不重试，也不改变 ACL。
func currentUserSIDForPath(path string) (*windows.SID, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, wrapCurrentUserSIDError(path, "open_current_process_token", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, wrapCurrentUserSIDError(path, "get_token_user", err)
	}
	sid, err := windows.StringToSid(user.User.Sid.String())
	if err != nil {
		return nil, wrapCurrentUserSIDError(path, "string_to_sid", err)
	}
	return sid, nil
}

// wrapCurrentUserSIDError 为当前用户 SID 解析阶段统一保留可路由的 Windows 权限错误链。
func wrapCurrentUserSIDError(path, operation string, cause error) error {
	return newWindowsSecurityOperationError("resolve current Windows user SID", operation, path, cause)
}
