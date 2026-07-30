package gateprivate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const exclusiveFileLockRetryDelay = 250 * time.Millisecond

// ExclusiveFileLock 表示随进程文件描述符自动释放的跨进程排他锁。
type ExclusiveFileLock struct {
	file *os.File
}

// AcquireExclusiveFileLock 等待取得规范绝对路径上的排他锁，并允许调用方取消等待。
func AcquireExclusiveFileLock(ctx context.Context, path string) (*ExclusiveFileLock, error) {
	if ctx == nil {
		return nil, errors.New("exclusive file lock context is nil")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("exclusive file lock path must be canonical and absolute: %q", path)
	}
	if err := validateExclusiveFileLockParent(path); err != nil {
		return nil, err
	}
	file, err := openExclusiveFileLock(path)
	if err != nil {
		return nil, err
	}
	if err := waitExclusiveFileLock(ctx, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return &ExclusiveFileLock{file: file}, nil
}

func openExclusiveFileLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open exclusive file lock: %w", err)
	}
	if err := RestrictOwnerFile(path); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

// waitExclusiveFileLock 以可取消轮询等待操作系统排他锁，不在竞争时忙等。
func waitExclusiveFileLock(ctx context.Context, file *os.File) error {
	for {
		if err := tryAcquireExclusiveFileLock(file); err == nil {
			return nil
		} else if !isExclusiveFileLockBusy(err) {
			return fmt.Errorf("acquire exclusive file lock: %w", err)
		}
		timer := time.NewTimer(exclusiveFileLockRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Release 先释放跨进程锁再关闭文件描述符。
func (lock *ExclusiveFileLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := releaseExclusiveFileLock(file)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

// validateExclusiveFileLockParent 拒绝符号链接穿越和可被其他用户改写的锁目录。
func validateExclusiveFileLockParent(path string) error {
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve exclusive file lock parent: %w", err)
	}
	if resolved != parent {
		return errors.New("exclusive file lock parent must not traverse symlinks")
	}
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect exclusive file lock parent: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("exclusive file lock parent must not be writable by group or others")
	}
	return nil
}
