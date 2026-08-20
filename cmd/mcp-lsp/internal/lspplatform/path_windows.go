//go:build windows

package lspplatform

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

// CanonicalDirectoryPath 通过只读目录句柄取得 Windows 最终路径。
// 这避免 filepath.EvalSymlinks 在受控子进程令牌下对普通目录误报 Access Denied，
// 同时仍解析路径别名，并拒绝最终目标本身是 reparse point。
func CanonicalDirectoryPath(path string) (string, error) {
	return canonicalExistingPath(path, true)
}

// CanonicalExistingPath 通过只读句柄解析 Windows 现存文件或目录的最终路径。
func CanonicalExistingPath(path string) (string, error) {
	return canonicalExistingPath(path, false)
}

func canonicalExistingPath(path string, requireDirectory bool) (string, error) {
	pointer, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("encode Windows directory path: %w", err)
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", securefs.WrapErrorForPath(fmt.Errorf("open Windows directory for canonicalization: %w", err), path)
	}
	defer windows.CloseHandle(handle) // 只读规范化句柄；主错误优先于关闭错误。

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", securefs.WrapErrorForPath(fmt.Errorf("inspect Windows canonical directory: %w", err), path)
	}
	if requireDirectory && info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return "", fmt.Errorf("canonical Windows path is not a directory: %s", securefs.RedactPath(path))
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("canonical Windows directory is a reparse point: %s", securefs.RedactPath(path))
	}

	buffer := make([]uint16, 512)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", securefs.WrapErrorForPath(fmt.Errorf("resolve Windows canonical directory: %w", err), path)
	}
	if length >= uint32(len(buffer)) {
		buffer = make([]uint16, length+1)
		length, err = windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", securefs.WrapErrorForPath(fmt.Errorf("resolve long Windows canonical directory: %w", err), path)
		}
	}
	resolved := windows.UTF16ToString(buffer[:length])
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
	} else {
		resolved = strings.TrimPrefix(resolved, `\\?\`)
	}
	return filepath.Clean(resolved), nil
}
