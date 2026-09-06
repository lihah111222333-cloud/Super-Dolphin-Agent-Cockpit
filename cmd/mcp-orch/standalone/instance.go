// Package standalone 提供独立 orch 服务的启动与单实例边界，不拥有第二套编排状态。
package standalone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// ErrAlreadyRunning 表示同一数据目录已经有一个活跃的 orch owner。
var ErrAlreadyRunning = errors.New("orch instance already running")

const instanceLockName = "orch.instance.lock"

// InstanceLock 持有内核跨进程锁；PID 文件或 endpoint 文件不能替代它。
type InstanceLock struct {
	mu   sync.Mutex
	file *os.File
	root *os.Root
	// stateDir is the physical directory used to acquire this lock. Keeping
	// the resolved path with the lock lets the caller bind subsequent stores to
	// the same root instead of reopening a caller-provided alias.
	stateDir string
}

type databasePathIdentity struct {
	main, wal, shm databaseFileIdentity
}

type databaseFileIdentity struct {
	present bool
	info    os.FileInfo
}

// AcquireInstance 在监听端口、迁移数据库和启动 agent 之前取得唯一 owner。
// stateDir 必须已经存在且仅当前用户可访问；不创建隐式默认数据目录。
func AcquireInstance(stateDir string) (*InstanceLock, error) {
	canonical, err := canonicalStateDir(stateDir)
	if err != nil {
		return nil, err
	}
	root, err := openStateRoot(canonical)
	if err != nil {
		return nil, err
	}
	file, err := openInstanceHandle(root)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	if err := tryInstanceLock(file); err != nil {
		closeErr := errors.Join(file.Close(), root.Close())
		if instanceLockBusy(err) {
			return nil, errors.Join(ErrAlreadyRunning, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("lock orch instance: %w", err), closeErr)
	}
	return &InstanceLock{file: file, root: root, stateDir: canonical}, nil
}

// CanonicalStateDir 解析有效的独立服务状态路径并返回物理绝对目录；接受符号链接别名（包括 macOS 的 /var 别名），缺失、非目录或共享路径直接失败。
func CanonicalStateDir(stateDir string) (string, error) {
	return canonicalStateDir(stateDir)
}

// CanonicalStateDir 返回此锁绑定的物理状态目录；值在锁生命周期内不可变，可安全复用于数据库和其他独立存储路径。
func (l *InstanceLock) CanonicalStateDir() string {
	if l == nil {
		return ""
	}
	return l.stateDir
}

// ValidateStateDir 复核锁绑定的物理状态根仍对应配置路径且保持私有权限。
// 该检查用于 daemon 生命周期守卫；Unix 目录锁本身允许 rename，因此不能仅依赖
// WithDatabasePath 的启动期检查来覆盖整个进程生命周期。
func (l *InstanceLock) ValidateStateDir() error {
	if l == nil {
		return errors.New("orch instance state root requires the instance lock")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.root == nil {
		return errors.New("orch instance state root requires the instance lock")
	}
	return l.validateStateDirLocked()
}

// WithDatabasePath 将 SQLite 启动绑定到此锁固定的物理状态根，并在 callback
// 前后复核主库及已有 sidecar 的目录项身份。路径别名、符号链接、父目录替换
// 或主库 inode 替换都会 fail-closed；callback 返回的错误不会被身份错误覆盖。
func (l *InstanceLock) WithDatabasePath(path string, callback func() error) error {
	if l == nil {
		return errors.New("orch database path requires the instance lock")
	}
	if callback == nil {
		return errors.New("orch database path callback is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.root == nil {
		return errors.New("orch database path requires the instance lock")
	}
	expected, err := l.captureDatabasePathLocked(path)
	if err != nil {
		return err
	}
	if !expected.main.present {
		if err := l.reserveDatabasePathLocked(path); err != nil {
			return err
		}
		expected, err = l.captureDatabasePathLocked(path)
		if err != nil {
			return err
		}
	}
	callbackErr := callback()
	if identityErr := l.validateDatabasePathLocked(path, expected); identityErr != nil {
		return errors.Join(callbackErr, identityErr)
	}
	return callbackErr
}

// reserveDatabasePathLocked 原子占位缺失的主库路径，令外部进程不能在
// preflight 与 SQLite 打开之间抢先放入文件或符号链接。
func (l *InstanceLock) reserveDatabasePathLocked(path string) error {
	name := filepath.Base(path)
	file, err := l.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("orch SQLite database path changed before open")
	}
	if err := file.Close(); err != nil {
		return errors.New("close reserved orch SQLite database")
	}
	entry, err := l.root.Lstat(name)
	if err != nil || !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
		return errors.New("orch SQLite database reservation identity changed")
	}
	return securefs.CheckPrivateOwnerOnly(filepath.Join(l.stateDir, name), entry)
}

func (l *InstanceLock) captureDatabasePathLocked(path string) (databasePathIdentity, error) {
	if err := l.validateDatabaseParentLocked(path); err != nil {
		return databasePathIdentity{}, err
	}
	var identity databasePathIdentity
	var err error
	for _, target := range []struct {
		name string
		dst  *databaseFileIdentity
	}{{filepath.Base(path), &identity.main}, {filepath.Base(path) + "-wal", &identity.wal}, {filepath.Base(path) + "-shm", &identity.shm}} {
		*target.dst, err = l.captureDatabaseFileLocked(target.name)
		if err != nil {
			return databasePathIdentity{}, err
		}
	}
	return identity, nil
}

func (l *InstanceLock) validateDatabasePathLocked(path string, expected databasePathIdentity) error {
	current, err := l.captureDatabasePathLocked(path)
	if err != nil {
		return err
	}
	if err := validateDatabaseFileIdentity(expected.main, current.main, true); err != nil {
		return err
	}
	if err := validateDatabaseSidecarIdentity(path+"-wal", expected.wal, current.wal); err != nil {
		return err
	}
	return validateDatabaseSidecarIdentity(path+"-shm", expected.shm, current.shm)
}

func (l *InstanceLock) validateDatabaseParentLocked(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("orch SQLite path must be a clean absolute path")
	}
	if filepath.Dir(path) != l.stateDir {
		return errors.New("orch SQLite path must remain directly within the instance state root")
	}
	return l.validateStateDirLocked()
}

func (l *InstanceLock) validateStateDirLocked() error {
	pathInfo, err := os.Lstat(l.stateDir)
	if err != nil {
		return errors.New("orch instance state root identity changed")
	}
	if err := securefs.CheckPrivateOwnerOnly(l.stateDir, pathInfo); err != nil {
		return err
	}
	rootInfo, err := l.root.Stat(".")
	if err != nil || !pathInfo.IsDir() || !os.SameFile(pathInfo, rootInfo) {
		return errors.New("orch instance state root identity changed")
	}
	return nil
}

func (l *InstanceLock) captureDatabaseFileLocked(name string) (databaseFileIdentity, error) {
	entry, err := l.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return databaseFileIdentity{}, nil
	}
	if err != nil {
		return databaseFileIdentity{}, errors.New("inspect orch SQLite path")
	}
	if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
		return databaseFileIdentity{}, errors.New("orch SQLite path must be a regular non-symlink file")
	}
	if lockEntry, lockErr := l.root.Lstat(instanceLockName); lockErr == nil && os.SameFile(lockEntry, entry) {
		return databaseFileIdentity{}, errors.New("orch SQLite path aliases the instance lock")
	} else if lockErr != nil && !errors.Is(lockErr, os.ErrNotExist) {
		return databaseFileIdentity{}, errors.New("inspect orch instance lock path")
	}
	if err := securefs.CheckPrivateOwnerOnly(filepath.Join(l.stateDir, name), entry); err != nil {
		return databaseFileIdentity{}, err
	}
	return databaseFileIdentity{present: true, info: entry}, nil
}

func validateDatabaseFileIdentity(expected, current databaseFileIdentity, allowCreate bool) error {
	if expected.present {
		if !current.present || !os.SameFile(expected.info, current.info) {
			return errors.New("orch SQLite database identity changed")
		}
		return nil
	}
	if current.present && !allowCreate {
		return errors.New("orch SQLite database appeared during startup")
	}
	return nil
}

func validateDatabaseSidecarIdentity(path string, expected, current databaseFileIdentity) error {
	if expected.present && current.present && !os.SameFile(expected.info, current.info) {
		return errors.New("orch SQLite sidecar identity changed")
	}
	return nil
}

// canonicalStateDir 校验并解析状态目录，确保后续锁与数据库使用同一物理根。
func canonicalStateDir(stateDir string) (string, error) {
	if stateDir == "" || !filepath.IsAbs(stateDir) {
		return "", errors.New("orch state directory must be an absolute path")
	}
	canonical, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		return "", fmt.Errorf("canonicalize orch state directory: %w", securefs.WrapErrorForPath(err, stateDir))
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect orch state directory: %w", securefs.WrapErrorForPath(err, canonical))
	}
	if !info.IsDir() {
		return "", errors.New("orch state directory is not a directory")
	}
	if err := securefs.CheckPrivateOwnerOnly(canonical, info); err != nil {
		return "", err
	}
	return canonical, nil
}

// openStateRoot 固定私有状态目录句柄，后续锁文件解析不能逃出该根。
func openStateRoot(stateDir string) (*os.Root, error) {
	if stateDir == "" || !filepath.IsAbs(stateDir) {
		return nil, errors.New("orch state directory must be an absolute path")
	}
	info, err := os.Stat(stateDir)
	if err != nil {
		return nil, fmt.Errorf("inspect orch state directory: %w", securefs.WrapErrorForPath(err, stateDir))
	}
	if !info.IsDir() {
		return nil, errors.New("orch state directory is not a directory")
	}
	if err := securefs.CheckPrivateOwnerOnly(stateDir, info); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		return nil, fmt.Errorf("open orch state root: %w", securefs.WrapErrorForPath(err, stateDir))
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.Join(errors.New("orch state directory identity changed"), err, root.Close())
	}
	return root, nil
}

// openInstanceLock 固定锁文件 inode，不截断、不删除锁文件，也不接受 symlink。
func openInstanceLock(root *os.Root) (*os.File, error) {
	before, err := root.Lstat(instanceLockName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect orch instance lock: %w", err)
	}
	if before != nil && !before.Mode().IsRegular() {
		return nil, errors.New("orch instance lock must be a regular file")
	}
	file, err := root.OpenFile(instanceLockName, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open orch instance lock: %w", err)
	}
	info, statErr := file.Stat()
	current, pathErr := root.Lstat(instanceLockName)
	if err := errors.Join(statErr, pathErr); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := validateLockIdentity(before, current, info); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

// validateLockIdentity 要求目录项和持有句柄始终指向同一普通文件。
func validateLockIdentity(before, current, info os.FileInfo) error {
	if !info.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return errors.New("orch instance lock identity changed")
	}
	if before != nil && !os.SameFile(before, info) {
		return errors.New("orch instance lock was replaced")
	}
	return nil
}

// Close 只释放此 handle 的锁；保留锁文件避免后续进程锁住不同 inode。
func (l *InstanceLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	root := l.root
	l.root = nil
	return errors.Join(unlockInstance(file), file.Close(), root.Close())
}
