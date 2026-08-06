//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// runtimeServerStableFilesystemIdentity 只使用 canonical root 的稳定 dev+ino，
// 拒绝把 mtime/size 等可变 stat 字段纳入 cohort proof。
func runtimeServerStableFilesystemIdentity(info os.FileInfo) (string, error) {
	if info == nil {
		return "", fmt.Errorf("canonical root filesystem identity is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("canonical root filesystem identity is unsupported on this platform")
	}
	return fmt.Sprintf("dev:%d:ino:%d", stat.Dev, stat.Ino), nil
}
