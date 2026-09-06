package standalone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// openReparsePoint 对应 Win32 FILE_FLAG_OPEN_REPARSE_POINT，禁止跟随末端 reparse point。
const openReparsePoint uint32 = 0x00200000

// openInstanceHandle 在 Windows 持有禁止 delete-share 的 sentinel，防止持锁时替换 inode。
func openInstanceHandle(root *os.Root) (*os.File, error) {
	reference, err := openInstanceLock(root)
	if err != nil {
		return nil, err
	}
	file, reopenErr := reopenInstanceHandle(filepath.Join(root.Name(), instanceLockName), reference)
	if err := errors.Join(reopenErr, reference.Close()); err != nil {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		return nil, err
	}
	return file, nil
}

// reopenInstanceHandle 按已固定的文件身份重开独占删除权的句柄，不创建替代文件。
func reopenInstanceHandle(path string, reference *os.File) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		openReparsePoint, 0)
	if err != nil {
		return nil, fmt.Errorf("open non-replaceable orch lock: %w", err)
	}
	file := os.NewFile(uintptr(handle), path)
	actual, statErr := file.Stat()
	expected, expectedErr := reference.Stat()
	if err := errors.Join(statErr, expectedErr); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return nil, errors.Join(errors.New("orch lock identity changed while opening"), file.Close())
	}
	return file, nil
}

// tryInstanceLock 使用非阻塞独占字节锁确认 Windows 唯一服务实例。
func tryInstanceLock(file *os.File) error {
	var position windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &position)
}

// unlockInstance 释放此 handle 持有的字节锁，不删除锁文件。
func unlockInstance(file *os.File) error {
	var position windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &position)
}

// instanceLockBusy 区分锁冲突与其他 Windows 文件错误。
func instanceLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
