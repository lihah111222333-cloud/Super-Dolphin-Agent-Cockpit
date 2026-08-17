//go:build windows

package schema

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

// filesystemSnapshotDirectoryWindowsOps 仅为包内测试注入系统调用，生产路径始终使用 Windows API。
type filesystemSnapshotDirectoryWindowsOps struct {
	open  func(string) (windows.Handle, error)
	flush func(windows.Handle) error
	close func(windows.Handle) error
}

// syncFilesystemSnapshotDirectory 使用可写目录句柄刷新目录元数据，保持 rename 后的耐久性边界。
func syncFilesystemSnapshotDirectory(path string) error {
	err := syncFilesystemSnapshotDirectoryWithOps(path, filesystemSnapshotDirectoryWindowsOps{
		open:  openFilesystemSnapshotDirectory,
		flush: windows.FlushFileBuffers,
		close: windows.CloseHandle,
	})
	return wrapSchemaFilesystemError(path, err)
}

// wrapSchemaFilesystemError 将 Windows 文件系统调用的真实 5/1314 提升为 securefs
// typed 错误；旧目录句柄测试 seam 仍保留原始错误，避免把句柄模式误判为 ACL。
func wrapSchemaFilesystemError(path string, err error) error {
	return securefs.WrapErrorForPath(err, path)
}

// syncFilesystemSnapshotDirectoryWithOps 保留 CreateFile、FlushFileBuffers 和 CloseHandle 的完整错误链。
func syncFilesystemSnapshotDirectoryWithOps(path string, ops filesystemSnapshotDirectoryWindowsOps) error {
	if ops.open == nil || ops.flush == nil || ops.close == nil {
		return errors.New("schema snapshot directory sync operations are incomplete")
	}
	handle, err := ops.open(path)
	if err != nil {
		return fmt.Errorf("open schema snapshot directory for fsync: %w", err)
	}
	flushErr := ops.flush(handle)
	closeErr := ops.close(handle)
	if err := errors.Join(flushErr, closeErr); err != nil {
		return fmt.Errorf("fsync schema snapshot directory: %w", err)
	}
	return nil
}

// openFilesystemSnapshotDirectory 请求目录读写句柄；FlushFileBuffers 要求 GENERIC_WRITE。
func openFilesystemSnapshotDirectory(path string) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
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
