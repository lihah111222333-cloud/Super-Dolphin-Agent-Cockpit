//go:build windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Rename 在 Windows 上保留目标文件安全描述符，并在替换后刷新结果文件。
func (osFileWriter) Rename(oldPath string, newPath string) error {
	if err := replaceFilePreservingMetadata(oldPath, newPath); err != nil {
		return err
	}
	return syncReplacedFile(newPath)
}

// replaceFilePreservingMetadata 使用 ReplaceFileW 原子替换，并拒绝忽略 DACL 合并错误。
func replaceFilePreservingMetadata(replacementPath string, replacedPath string) error {
	replaced, err := windows.UTF16PtrFromString(replacedPath)
	if err != nil {
		return fmt.Errorf("encode replaced Windows path: %w", err)
	}
	replacement, err := windows.UTF16PtrFromString(replacementPath)
	if err != nil {
		return fmt.Errorf("encode replacement Windows path: %w", err)
	}
	replaceFile := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")
	result, _, callErr := replaceFile.Call(
		uintptr(unsafe.Pointer(replaced)),
		uintptr(unsafe.Pointer(replacement)),
		0,
		0,
		0,
		0,
	)
	runtime.KeepAlive(replaced)
	runtime.KeepAlive(replacement)
	if result != 0 {
		return nil
	}
	if !errors.Is(callErr, windows.ERROR_SUCCESS) {
		return fmt.Errorf("ReplaceFileW: %w", callErr)
	}
	return errors.New("ReplaceFileW failed without a Windows error")
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
