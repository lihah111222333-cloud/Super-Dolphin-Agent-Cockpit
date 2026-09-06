package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrLockBusy            = errors.New("pinned directory lock is busy")
	ErrLockClosed          = errors.New("pinned directory lock is closed")
	ErrLockIdentityChanged = errors.New("pinned directory lock identity changed")
)

// PinnedDirectoryLock 把进程锁绑定到 canonical 私有目录、目录句柄和固定哨兵身份。
type PinnedDirectoryLock struct {
	mu sync.Mutex

	rootPath     string
	root         *os.Root
	directory    *os.File
	directoryRef os.FileInfo
	sentinelName string
	sentinel     *os.File
	sentinelRef  os.FileInfo
	platform     pinnedDirectoryPlatformLock
	closed       bool
}

// AcquirePinnedDirectoryLock 在私有目录上取得非阻塞、跨进程且身份固定的独占锁。
func AcquirePinnedDirectoryLock(rootPath, sentinelName string) (*PinnedDirectoryLock, error) {
	if err := ensurePinnedDirectoryPlatformSupported(); err != nil {
		return nil, err
	}
	canonical, err := canonicalPrivateLockRoot(rootPath)
	if err != nil {
		return nil, err
	}
	if err := validatePinnedSentinelName(sentinelName); err != nil {
		return nil, err
	}

	root, directory, directoryRef, err := openPinnedDirectoryHandles(canonical)
	if err != nil {
		return nil, err
	}

	platform, sentinel, err := acquirePinnedDirectoryPlatform(canonical, root, directory, sentinelName)
	if err != nil {
		_ = directory.Close()
		_ = root.Close()
		return nil, err
	}
	sentinelRef, err := sentinel.Stat()
	if err != nil {
		_ = closePinnedDirectoryPlatform(platform, sentinel)
		_ = directory.Close()
		_ = root.Close()
		return nil, fmt.Errorf("inspect pinned sentinel %s: %s", RedactPath(sentinelName), SafeError(err))
	}

	lock := &PinnedDirectoryLock{
		rootPath:     canonical,
		root:         root,
		directory:    directory,
		directoryRef: directoryRef,
		sentinelName: sentinelName,
		sentinel:     sentinel,
		sentinelRef:  sentinelRef,
		platform:     platform,
	}
	if err := lock.validateLocked(); err != nil {
		_ = lock.closeLocked()
		return nil, err
	}
	return lock, nil
}

// openPinnedDirectoryHandles 打开 os.Root 与目录句柄，并在交付前确认二者身份一致。
func openPinnedDirectoryHandles(canonical string) (*os.Root, *os.File, os.FileInfo, error) {
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open pinned lock root %s: %s", RedactPath(canonical), SafeError(err))
	}
	directory, err := os.Open(canonical)
	if err != nil {
		_ = root.Close()
		return nil, nil, nil, fmt.Errorf("open pinned directory %s: %s", RedactPath(canonical), SafeError(err))
	}
	directoryRef, err := directory.Stat()
	if err != nil {
		_ = directory.Close()
		_ = root.Close()
		return nil, nil, nil, fmt.Errorf("inspect pinned directory handle %s: %s", RedactPath(canonical), SafeError(err))
	}
	if err := validatePinnedRootIdentity(canonical, root, directoryRef); err != nil {
		_ = directory.Close()
		_ = root.Close()
		return nil, nil, nil, err
	}
	return root, directory, directoryRef, nil
}

// Validate 重新校验路径、权限、目录句柄和哨兵句柄仍指向取得锁时的对象。
func (l *PinnedDirectoryLock) Validate() error {
	if l == nil {
		return ErrLockClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.validateLocked()
}

// Close 幂等释放内核锁与全部固定句柄，但保留哨兵文件本身。
func (l *PinnedDirectoryLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closeLocked()
}

// validateLocked 在实例互斥锁内执行完整身份校验。
func (l *PinnedDirectoryLock) validateLocked() error {
	if l.closed {
		return ErrLockClosed
	}
	if err := validatePinnedRootIdentity(l.rootPath, l.root, l.directoryRef); err != nil {
		return identityChanged("pinned root", err)
	}
	directoryInfo, err := l.directory.Stat()
	if err != nil {
		return identityChanged("pinned directory handle", err)
	}
	if !os.SameFile(directoryInfo, l.directoryRef) {
		return identityChanged("pinned directory handle", nil)
	}
	sentinelInfo, err := l.sentinel.Stat()
	if err != nil {
		return identityChanged("pinned sentinel handle", err)
	}
	if !os.SameFile(sentinelInfo, l.sentinelRef) {
		return identityChanged("pinned sentinel handle", nil)
	}
	return validatePinnedSentinelIdentity(l.rootPath, l.root, l.sentinelName, l.sentinelRef)
}

// closeLocked 在实例互斥锁内关闭资源，并确保失败后也不会再次冒充持锁状态。
func (l *PinnedDirectoryLock) closeLocked() error {
	if l.closed {
		return nil
	}
	l.closed = true
	platformErr := closePinnedDirectoryPlatform(l.platform, l.sentinel)
	directoryErr := l.directory.Close()
	rootErr := l.root.Close()
	if err := errors.Join(platformErr, directoryErr, rootErr); err != nil {
		return fmt.Errorf("close pinned directory lock %s: %w", RedactPath(l.rootPath), err)
	}
	return nil
}

// canonicalPrivateLockRoot 固定绝对 canonical 目录并在打开句柄前校验严格私有权限。
func canonicalPrivateLockRoot(rootPath string) (string, error) {
	if rootPath == "" {
		return "", errors.New("pinned lock root path is empty")
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolve pinned lock root: %s", SafeError(err))
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize pinned lock root %s: %s", RedactPath(absolute), SafeError(err))
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect pinned lock root %s: %s", RedactPath(canonical), SafeError(err))
	}
	if !info.IsDir() {
		return "", fmt.Errorf("pinned lock root is not a directory: %s", RedactPath(canonical))
	}
	if err := CheckPrivateOwnerOnly(canonical, info); err != nil {
		return "", fmt.Errorf("pinned lock root is not private: %w", err)
	}
	return canonical, nil
}

// validatePinnedRootIdentity 校验路径、os.Root 和固定目录身份一致且权限仍严格私有。
func validatePinnedRootIdentity(rootPath string, root *os.Root, directoryRef os.FileInfo) error {
	pathInfo, err := os.Lstat(rootPath)
	if err != nil {
		return fmt.Errorf("inspect pinned root path: %s", SafeErrorForPath(err, rootPath))
	}
	if !pathInfo.IsDir() || !os.SameFile(pathInfo, directoryRef) {
		return errors.New("pinned root path no longer names the held directory")
	}
	if err := CheckPrivateOwnerOnly(rootPath, pathInfo); err != nil {
		return err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect os.Root handle: %s", SafeErrorForPath(err, rootPath))
	}
	if !os.SameFile(rootInfo, directoryRef) {
		return errors.New("os.Root no longer names the held directory")
	}
	return nil
}

// validatePinnedSentinelIdentity 校验根内名称仍指向固定的私有普通文件。
func validatePinnedSentinelIdentity(
	rootPath string,
	root *os.Root,
	sentinelName string,
	sentinelRef os.FileInfo,
) error {
	entryInfo, err := root.Lstat(sentinelName)
	if err != nil {
		return identityChanged("pinned sentinel path", err)
	}
	if !entryInfo.Mode().IsRegular() || !os.SameFile(entryInfo, sentinelRef) {
		return identityChanged("pinned sentinel path", nil)
	}
	if err := CheckPrivateOwnerOnly(filepath.Join(rootPath, sentinelName), entryInfo); err != nil {
		return identityChanged("pinned sentinel permissions", err)
	}
	return nil
}

// validatePinnedSentinelName 限制哨兵为 os.Root 下的单个相对 basename。
func validatePinnedSentinelName(name string) error {
	if name == "" || name == "." || filepath.IsAbs(name) || filepath.Base(name) != name || filepath.Clean(name) != name {
		return fmt.Errorf("invalid pinned lock sentinel name: %s", RedactPath(name))
	}
	return nil
}

// identityChanged 把所有持锁后的身份或权限漂移映射为稳定错误。
func identityChanged(component string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%s: %w", component, ErrLockIdentityChanged)
	}
	return fmt.Errorf("%s: %w: %s", component, ErrLockIdentityChanged, SafeError(cause))
}
