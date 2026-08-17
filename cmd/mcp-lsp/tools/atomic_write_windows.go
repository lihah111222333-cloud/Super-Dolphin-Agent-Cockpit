//go:build windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsReplaceFileRetryAttempts = 21
	windowsReplaceFileRetryDelay    = 50 * time.Millisecond
)

type windowsReplaceFileCall func(replaced, replacement *uint16) (uintptr, error)

// Rename 在 Windows 上保留目标文件安全描述符，并在替换后刷新结果文件。
func (osFileWriter) Rename(oldPath string, newPath string) error {
	if err := replaceFilePreservingMetadata(oldPath, newPath); err != nil {
		return err
	}
	return syncReplacedFile(newPath)
}

// replaceFilePreservingMetadata 使用 ReplaceFileW 原子替换，并拒绝忽略 DACL 合并错误。
func replaceFilePreservingMetadata(replacementPath string, replacedPath string) error {
	return replaceFilePreservingMetadataWithCall(replacementPath, replacedPath, callWindowsReplaceFile, time.Sleep)
}

// replaceFilePreservingMetadataWithCall 仅对 Win32 32/33/1175 做有界重试。
// 这些错误不会发布替换文件；其中 Microsoft 明确保证 1175 下两个文件仍保留
// 原名。1176/1177 可能已经改变文件状态，必须立即 fail-fast，禁止重试或非原子覆盖。
func replaceFilePreservingMetadataWithCall(replacementPath string, replacedPath string, call windowsReplaceFileCall, sleep func(time.Duration)) error {
	if call == nil || sleep == nil {
		return errors.New("ReplaceFileW call and retry sleeper are required")
	}
	replaced, err := windows.UTF16PtrFromString(replacedPath)
	if err != nil {
		return fmt.Errorf("encode replaced Windows path: %w", err)
	}
	replacement, err := windows.UTF16PtrFromString(replacementPath)
	if err != nil {
		return fmt.Errorf("encode replacement Windows path: %w", err)
	}
	for attempt := 1; attempt <= windowsReplaceFileRetryAttempts; attempt++ {
		result, callErr := call(replaced, replacement)
		runtime.KeepAlive(replaced)
		runtime.KeepAlive(replacement)
		if result != 0 {
			return nil
		}
		if isRetryableWindowsReplaceError(callErr) && attempt < windowsReplaceFileRetryAttempts {
			sleep(windowsReplaceFileRetryDelay)
			continue
		}
		if !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return fmt.Errorf("ReplaceFileW failed attempt=%d/%d win32=%d: %w", attempt, windowsReplaceFileRetryAttempts, windowsErrorCode(callErr), callErr)
		}
		return errors.New("ReplaceFileW failed without a Windows error")
	}
	return errors.New("ReplaceFileW exhausted retries without a result")
}

// isRetryableWindowsReplaceError 只接受发布前的短暂共享、锁定或删除占用。
func isRetryableWindowsReplaceError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_UNABLE_TO_REMOVE_REPLACED)
}

// callWindowsReplaceFile 调用系统 ReplaceFileW，并保留 GetLastError 供上层分类。
func callWindowsReplaceFile(replaced, replacement *uint16) (uintptr, error) {
	replaceFile := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")
	result, _, callErr := replaceFile.Call(
		uintptr(unsafe.Pointer(replaced)),
		uintptr(unsafe.Pointer(replacement)),
		0,
		0,
		0,
		0,
	)
	return result, callErr
}

// windowsErrorCode 提取日志可检索的 Win32 数字错误码。
func windowsErrorCode(err error) uint32 {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return uint32(errno)
	}
	return 0
}

// syncReplacedFile 刷新 ReplaceFileW 已发布的结果文件；Windows 不再尝试 Flush 目录句柄。
func syncReplacedFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open replaced Windows file for sync: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync replaced Windows file: %w", err)
	}
	return nil
}
