//go:build !windows

package memory

import (
	"errors"
	"os"
	"syscall"
)

// tryAcquireMemoryFileLock 在 Unix 平台尝试获取非阻塞排他文件锁，锁被占用时由调用方转换为记忆根忙碌错误。
func tryAcquireMemoryFileLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// releaseMemoryFileLock 释放当前进程持有的文件锁；释放失败必须向上传递，避免误以为写入边界已解锁。
func releaseMemoryFileLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

// isMemoryFileLockBusy 判断系统错误是否表示锁已被其他进程持有，用于把平台错误收敛为统一并发写保护语义。
func isMemoryFileLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
