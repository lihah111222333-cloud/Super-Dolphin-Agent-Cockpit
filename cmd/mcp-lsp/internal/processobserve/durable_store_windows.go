//go:build windows

package processobserve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const durableLockName = ".store.lock"

type secureRoot struct {
	path   string
	handle windows.Handle
	dev    uint64
	ino    uint64
}

func openDurableRoot(path string) (*secureRoot, error) {
	absolute, err := validatedWindowsDurableRootPath(path)
	if err != nil {
		return nil, err
	}
	handle, err := openWindowsDurableRootHandle(absolute)
	if err != nil {
		return nil, err
	}
	info, err := requireWindowsPrivateDirectory(handle)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return &secureRoot{
		path:   absolute,
		handle: handle,
		dev:    uint64(info.VolumeSerialNumber),
		ino:    uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
	}, nil
}

func validatedWindowsDurableRootPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("durable observation root must be absolute")
	}
	absolute := filepath.Clean(path)
	volume := filepath.VolumeName(absolute)
	if volume == "" || strings.EqualFold(absolute, volume+string(os.PathSeparator)) {
		return "", errors.New("durable observation root must be a private directory")
	}
	return absolute, nil
}

func (r *secureRoot) identity() (uint64, uint64) {
	if r == nil {
		return 0, 0
	}
	return r.dev, r.ino
}

func (r *secureRoot) close() error {
	if r == nil || r.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(r.handle)
	r.handle = windows.InvalidHandle
	return err
}

func (r *secureRoot) withStoreLock(ctx context.Context, action func(*secureRoot) error) (retErr error) {
	if r == nil || r.handle == windows.InvalidHandle {
		return ErrDurableStoreClosed
	}
	lockPath := filepath.Join(r.path, durableLockName)
	lock, err := openWindowsDurableFile(
		lockPath,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		windows.OPEN_ALWAYS,
		windowsOpenReparsePoint,
	)
	if err != nil {
		return fmt.Errorf("open durable observation lock: %w", err)
	}
	if _, err := requireWindowsPrivateRegularFile(lock, 0); err != nil {
		_ = windows.CloseHandle(lock)
		return err
	}
	var overlapped windows.Overlapped
	if err := acquireWindowsDurableLock(ctx, lock, &overlapped); err != nil {
		_ = windows.CloseHandle(lock)
		return err
	}
	defer func() {
		unlockErr := windows.UnlockFileEx(lock, 0, 1, 0, &overlapped)
		closeErr := windows.CloseHandle(lock)
		retErr = errors.Join(retErr, unlockErr, closeErr)
	}()
	return action(r)
}

func acquireWindowsDurableLock(ctx context.Context, handle windows.Handle, overlapped *windows.Overlapped) error {
	for {
		err := windows.LockFileEx(
			handle,
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			overlapped,
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return fmt.Errorf("lock durable observation store: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
