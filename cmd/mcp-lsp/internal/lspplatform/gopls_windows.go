//go:build windows

package lspplatform

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// windowsOpenReparsePoint 是 WinBase.h 中打开重解析点本身的标志。
const windowsOpenReparsePoint uint32 = 0x00200000

// GoplsServerArgs 只携带共享 daemon 的生命周期 marker；生产编排会把它
// 转换为显式 loopback endpoint，不会把 timeout 参数传给 forwarder。
func GoplsServerArgs(idleTimeout time.Duration) ([]string, error) {
	if err := ValidateGoplsIdleTimeout(idleTimeout); err != nil {
		return nil, err
	}
	return []string{"-remote.listen.timeout=" + idleTimeout.String()}, nil
}

// NormalizeGoplsForwarderArgs 移除只属于共享 daemon 的内部参数。
func NormalizeGoplsForwarderArgs(args []string) []string {
	return slices.DeleteFunc(slices.Clone(args), func(arg string) bool {
		return strings.HasPrefix(arg, "-remote=") || strings.HasPrefix(arg, "-remote.listen.timeout=")
	})
}

// GoplsUsesSharedDaemon 声明 Windows 使用产品管理的显式共享 daemon。
func GoplsUsesSharedDaemon() bool {
	return true
}

func stableDirectoryIdentity(path string, info os.FileInfo) (identity string, retErr error) {
	if info == nil || !info.IsDir() {
		return "", errors.New("canonical root filesystem identity requires a directory")
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", fmt.Errorf("encode canonical root path for filesystem identity: %w", err)
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windowsOpenReparsePoint,
		0,
	)
	if err != nil {
		return "", fmt.Errorf("open canonical root for filesystem identity: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, windows.CloseHandle(handle))
	}()
	var fileInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &fileInfo); err != nil {
		return "", fmt.Errorf("read canonical root filesystem identity: %w", err)
	}
	if fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", errors.New("canonical root filesystem identity requires a non-reparse directory")
	}
	return fmt.Sprintf(
		"volume:%08x:file:%08x%08x",
		fileInfo.VolumeSerialNumber,
		fileInfo.FileIndexHigh,
		fileInfo.FileIndexLow,
	), nil
}
