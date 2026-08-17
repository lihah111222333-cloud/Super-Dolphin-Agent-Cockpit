//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

// runtimeServerNodeVersion 在 Windows 构建中只接受锁定 Node 的绝对路径；
// windows build tag 保证 PATH fallback 不会进入该平台产物。
func runtimeServerNodeVersion(overrides []string) (string, bool, error) {
	nodePath := runtimeServerEnvValue(overrides, runtimeServerWindowsNodeExecutableEnv)
	if strings.TrimSpace(nodePath) == "" {
		return "", false, fmt.Errorf("Windows locked Node executable path %s is required", runtimeServerWindowsNodeExecutableEnv)
	}
	if !filepath.IsAbs(nodePath) {
		return "", false, fmt.Errorf("Windows locked Node executable path must be absolute: %q", nodePath)
	}
	validated, err := runtimeServerValidateExecutable(nodePath)
	if err != nil {
		return "", false, fmt.Errorf("validate Windows locked Node executable %q: %w", nodePath, err)
	}
	return runtimeServerReadNodeVersion(validated, "")
}

// runtimeServerValidateExecutable 按 Windows 文件存在性校验可执行候选；Windows
// 不使用 POSIX execute bits，目录仍然严格拒绝。
func runtimeServerValidateExecutable(file string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: %s", exec.ErrNotFound, file)
	}
	return file, nil
}

// runtimeServerExecutableExtensions 按 Windows PATHEXT 枚举候选；空 PATHEXT 使用
// Windows 标准扩展集合，显式扩展名不再追加候选。
func runtimeServerExecutableExtensions(file, pathExt string) []string {
	if filepath.Ext(file) != "" {
		return []string{""}
	}
	extensions := filepath.SplitList(pathExt)
	if len(extensions) == 0 {
		return []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	return extensions
}

// runtimeServerFileInfoIsExecutable 表示 Windows 候选已经由文件类型检查通过；
// execute-bit 不是 Windows 可执行性事实。
func runtimeServerFileInfoIsExecutable(_ os.FileInfo) bool {
	return true
}

// runtimeServerHardenPrivateDirectory 为 Windows 共享 runtime cache 设置受保护 DACL；
// 目录生命周期失败必须阻断，不能以 POSIX mode 或宽泛 ACL 静默放行。
func runtimeServerHardenPrivateDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Windows private runtime directory %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Windows private runtime path is not a directory: %q", path)
	}
	if err := securefs.RestrictPrivateOwnerOnly(path, info.Mode()); err != nil {
		return fmt.Errorf("set Windows private runtime directory ACL %s: %w", securefs.RedactPath(path), err)
	}
	return nil
}

// runtimeServerValidatePrivateDirectoryPlatform 校验 Windows runtime cache 的现有 DACL；
// ACL 证据缺失或出现非 owner 写权限时直接失败。
func runtimeServerValidatePrivateDirectoryPlatform(path string, info os.FileInfo) error {
	if info == nil || !info.IsDir() {
		return fmt.Errorf("Windows private runtime path is not a directory: %q", path)
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return err
	}
	return nil
}

// runtimeServerTryLockResourceLease 使用 Windows LockFileEx 获取进程级非阻塞排他锁；
// Windows 进程退出时内核自动释放句柄，锁冲突原样交给上层重试。
func runtimeServerTryLockResourceLease(file *os.File) error {
	if file == nil {
		return errors.New("Windows resource lease file is nil")
	}
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
}

// runtimeServerUnlockResourceLease 释放 Windows resource lease 的排他锁；文件句柄仍由
// 调用方关闭，失败必须返回而不能吞掉生命周期错误。
func runtimeServerUnlockResourceLease(file *os.File) error {
	if file == nil {
		return errors.New("Windows resource lease file is nil")
	}
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

// runtimeServerResourceLeaseLockBusy 识别 Windows LockFileEx 的锁冲突，其他错误不作
// 可重试降级，确保 ACL/句柄/权限失败保持 fail-fast。
func runtimeServerResourceLeaseLockBusy(err error) bool {
	return errors.Is(err, syscall.Errno(windows.ERROR_LOCK_VIOLATION))
}
