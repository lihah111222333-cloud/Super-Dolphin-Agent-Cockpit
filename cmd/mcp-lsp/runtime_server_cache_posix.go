//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

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
