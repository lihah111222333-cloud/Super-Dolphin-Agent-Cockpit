//go:build !windows

package similarity

import (
	"os"
	"syscall"
)

// lockIgnoredFile 获取 ignored set 的跨进程排他锁，并在持锁者结束后继续执行。
func lockIgnoredFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

// unlockIgnoredFile 释放 ignored set 的跨进程排他锁。
func unlockIgnoredFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
