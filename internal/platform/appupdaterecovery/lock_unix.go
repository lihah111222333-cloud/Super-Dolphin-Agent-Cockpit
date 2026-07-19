//go:build !windows

package appupdaterecovery

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func tryAcquireTransactionFileLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func releaseTransactionFileLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func isTransactionFileLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// captureDiscardRootIdentity 通过不跟随链接的文件描述符读取 discard 根实例身份。
func captureDiscardRootIdentity(path string) (identity discardRootIdentity, err error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return discardRootIdentity{}, fmt.Errorf("open backup discard root without following links: %w", err)
	}
	defer func() {
		if closeErr := syscall.Close(fd); err == nil && closeErr != nil {
			err = fmt.Errorf("close backup discard root: %w", closeErr)
		}
	}()
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return discardRootIdentity{}, fmt.Errorf("inspect backup discard root identity: %w", err)
	}
	kind, err := discardRootKind(uint32(stat.Mode))
	if err != nil {
		return discardRootIdentity{}, err
	}
	return discardRootIdentity{VolumeID: uint64(stat.Dev), FileID: uint64(stat.Ino), Kind: kind}, nil
}

func discardRootKind(mode uint32) (string, error) {
	switch mode & syscall.S_IFMT {
	case syscall.S_IFREG:
		return discardRootKindFile, nil
	case syscall.S_IFDIR:
		return discardRootKindDirectory, nil
	default:
		return "", fmt.Errorf("backup discard root mode %#o is not a regular file or directory", mode)
	}
}
