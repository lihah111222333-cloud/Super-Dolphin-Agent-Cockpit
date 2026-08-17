//go:build windows

package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type assetOSLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

// acquireAssetOSLock 使用 Windows LockFileEx，让进程被终止时锁由内核自动释放。
func acquireAssetOSLock(ctx context.Context, path string) (*assetOSLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("Windows asset lock path is a symlink: %q", path)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect Windows asset lock %q: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Windows asset lock %q: %w", path, err)
	}
	lock := &assetOSLock{file: file}
	for {
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.Errno(windows.ERROR_LOCK_VIOLATION)) {
			_ = file.Close()
			return nil, fmt.Errorf("lock Windows asset lock %q: %w", path, err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, fmt.Errorf("wait for Windows asset lock %q: %w", path, ctx.Err())
		}
	}
}

// Close 解锁并关闭 Windows 文件句柄；进程终止时内核也会自动完成这一步。
func (l *assetOSLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
