//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// runtimeServerNodeVersion 在非 Windows 构建中按显式 PATH 解析 Node；!windows
// build tag 保证该 PATH 语义不会被编入 Windows 产物。
func runtimeServerNodeVersion(overrides []string) (string, bool, error) {
	pathEnv := runtimeServerEnvValue(overrides, "PATH")
	nodePath, err := runtimeServerLookPath("node", pathEnv, runtimeServerEnvValue(overrides, "PATHEXT"))
	if err != nil {
		return "", false, fmt.Errorf("resolve Node runtime for language server: %w", err)
	}
	return runtimeServerReadNodeVersion(nodePath, pathEnv)
}

// runtimeServerValidateExecutable 在非 Windows 构建中同时要求普通文件与 execute bit。
func runtimeServerValidateExecutable(file string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%w: %s", exec.ErrNotFound, file)
	}
	return file, nil
}

// runtimeServerExecutableExtensions 在非 Windows 构建中不解释 PATHEXT。
func runtimeServerExecutableExtensions(_ string, _ string) []string {
	return []string{""}
}

// runtimeServerFileInfoIsExecutable 在非 Windows 构建中以 POSIX execute bit 判定候选。
func runtimeServerFileInfoIsExecutable(info os.FileInfo) bool {
	return info != nil && info.Mode().Perm()&0o111 != 0
}

func runtimeServerHardenPrivateDirectory(path string) error {
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("set private directory mode for %s: %w", path, err)
	}
	return nil
}

func runtimeServerValidatePrivateDirectoryPlatform(path string, info os.FileInfo) error {
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory mode for %s is %04o, want 0700", path, info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private directory owner metadata is unavailable: %s", path)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("private directory owner for %s is uid %d, want %d", path, stat.Uid, os.Geteuid())
	}
	return nil
}

func runtimeServerTryLockResourceLease(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func runtimeServerUnlockResourceLease(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func runtimeServerResourceLeaseLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
