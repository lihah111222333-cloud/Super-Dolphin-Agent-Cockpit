//go:build darwin || linux

package standalone

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openInstanceHandle 锁住数据目录 inode，替换普通 .lock 文件不会产生第二个 owner。
func openInstanceHandle(root *os.Root) (*os.File, error) {
	return root.Open(".")
}

// tryInstanceLock 使用非阻塞 flock 确认当前状态目录的唯一活体 owner。
func tryInstanceLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// unlockInstance 释放当前描述符持有的锁，不删除锁文件。
func unlockInstance(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

// instanceLockBusy 只将内核锁竞争映射为已运行，不吞权限或 I/O 错误。
func instanceLockBusy(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
