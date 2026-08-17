//go:build !windows

package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

type assetOSLock struct {
	file *os.File
}

// acquireAssetOSLock 使用 flock 提供跨进程锁；进程退出时内核自动释放锁。
func acquireAssetOSLock(ctx context.Context, path string) (*assetOSLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("asset lock path is a symlink: %q", path)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect asset lock %q: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open asset lock %q: %w", path, err)
	}
	lock := &assetOSLock{file: file}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock asset %q: %w", path, err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, fmt.Errorf("wait for asset lock %q: %w", path, ctx.Err())
		}
	}
}

// Close 释放 flock 并关闭文件；进程终止时内核也会自动释放 flock。
func (l *assetOSLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
