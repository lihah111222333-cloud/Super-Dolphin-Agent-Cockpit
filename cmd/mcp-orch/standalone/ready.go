package standalone

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// Ready 是已完成监听的非授权发现信息；不包含 bearer token。
type Ready struct {
	Endpoint   string `json:"endpoint"`
	InstanceID string `json:"instance_id"`
}

// PublishReady 只创建调用方指定的私有发现文件，不覆盖已有文件。
func PublishReady(path string, ready Ready) (cleanup func() error, err error) {
	return publishReady(path, ready, false)
}

// PublishReadyAfterInstanceLock 在已取得单实例内核锁后发布 ready。
// 已存在且未被其他发布者持有内核锁的文件视为崩溃遗留并被接管；活跃发布者的
// ready inode 仍被锁住，因此绝不会被覆盖。调用方必须在整个 runner 生命周期内
// 保持 lock 存活。
func PublishReadyAfterInstanceLock(lock *InstanceLock, path string, ready Ready) (cleanup func() error, err error) {
	if lock == nil {
		return nil, errors.New("orch ready takeover requires the instance lock")
	}
	// Keep the capability check and publication under the InstanceLock mutex so
	// a concurrent Close cannot release ownership between those operations.
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.file == nil {
		return nil, errors.New("orch ready takeover requires the instance lock")
	}
	return publishReady(path, ready, true)
}

// publishReady 原子创建并锁定私有 ready inode，可选地接管已确认陈旧文件。
func publishReady(path string, ready Ready, takeover bool) (cleanup func() error, err error) {
	if err := validateReadyPublication(path, ready); err != nil {
		return nil, err
	}
	if takeover {
		if err := reclaimStaleReady(path); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(ready)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".orch-ready-*")
	if err != nil {
		return nil, errors.New("orch ready temporary file cannot be created")
	}
	defer func() { err = errors.Join(err, os.Remove(file.Name())) }()
	info, err := writeReadyTemp(file, raw)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := os.Link(file.Name(), path); err != nil {
		_ = unlockReady(file)
		_ = file.Close()
		return nil, errors.New("orch ready file already exists or atomic publication failed")
	}
	var cleanupOnce sync.Once
	var cleanupErr error
	return func() error {
		cleanupOnce.Do(func() {
			cleanupErr = errors.Join(removeOwnedReady(path, info), unlockReady(file), file.Close())
		})
		return cleanupErr
	}, nil
}

// validateReadyPublication 校验发现文件路径、内容及私有目录权限。
func validateReadyPublication(path string, ready Ready) error {
	if !filepath.IsAbs(path) || ready.Endpoint == "" || ready.InstanceID == "" {
		return errors.New("orch ready path must be absolute and readiness must be complete")
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return errors.New("orch ready parent does not exist")
	}
	return securefs.CheckPrivateOwnerOnly(filepath.Dir(path), parent)
}

func writeReadyTemp(file *os.File, raw []byte) (os.FileInfo, error) {
	info, statErr := file.Stat()
	_, writeErr := file.Write(raw)
	return info, errors.Join(statErr, writeErr, file.Sync(), tryReadyLock(file))
}

// reclaimStaleReady removes an existing ready file only after obtaining its
// inode lock. A busy lock proves another live publisher owns that exact path.
func reclaimStaleReady(path string) error {
	return reclaimStaleReadyWithRename(path, os.Rename)
}

// reclaimStaleReadyWithRename 用原子 rename 把待接管目录项移入私有随机目录，再
// 复核其 inode 身份后删除。rename 的目标是当次创建且不可预测的目录项，因此即使
// 原 ready 路径在检查后被替换，也只会把替换项移入隔离目录并 fail-closed，不会
// 误删替换后的新发现文件。
//
// renameFn 仅用于注入确定性的身份竞争测试；生产路径始终使用 os.Rename。
func reclaimStaleReadyWithRename(path string, renameFn func(string, string) error) error {
	entry, file, err := openStaleReady(path)
	if err != nil || entry == nil {
		return err
	}
	defer file.Close()
	if err := lockStaleReady(file); err != nil {
		return err
	}
	quarantineDir, err := os.MkdirTemp(filepath.Dir(path), ".orch-ready-reclaim-*")
	if err != nil {
		return errors.New("create stale orch ready quarantine")
	}
	quarantinePath := filepath.Join(quarantineDir, filepath.Base(path))
	cleanupQuarantine := func() error {
		// The quarantine directory must be empty before removal. Do not use
		// RemoveAll: an unexpected entry must remain recoverable.
		return os.Remove(quarantineDir)
	}
	if err := renameFn(path, quarantinePath); err != nil {
		return errors.Join(errors.New("detach stale orch ready file"), cleanupQuarantine(), unlockReady(file))
	}
	return finalizeStaleReadyTakeover(entry, file, path, quarantinePath, cleanupQuarantine)
}

// finalizeStaleReadyTakeover 复核隔离 inode，成功后删除陈旧文件，否则尝试无覆盖恢复。
func finalizeStaleReadyTakeover(entry os.FileInfo, file *os.File, path, quarantinePath string, cleanupQuarantine func() error) error {
	moved, statErr := os.Lstat(quarantinePath)
	if statErr != nil || !moved.Mode().IsRegular() || !os.SameFile(entry, moved) {
		restoreErr := restoreQuarantinedReady(quarantinePath, path)
		if restoreErr != nil {
			return errors.Join(errors.New("orch ready file identity changed"), restoreErr, unlockReady(file))
		}
		return errors.Join(errors.New("orch ready file identity changed"), cleanupQuarantine(), unlockReady(file))
	}
	if err := os.Remove(quarantinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(errors.New("remove stale orch ready file"), unlockReady(file))
	}
	return errors.Join(cleanupQuarantine(), unlockReady(file))
}

// openStaleReady 打开待接管文件并验证路径 inode 身份不变。
func openStaleReady(path string) (os.FileInfo, *os.File, error) {
	entry, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, errors.New("inspect existing orch ready file")
	}
	if !entry.Mode().IsRegular() {
		return nil, nil, errors.New("existing orch ready file must be a regular file")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, errors.New("inspect existing orch ready file")
	}
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(entry, actual) {
		_ = file.Close()
		return nil, nil, errors.New("orch ready file identity changed")
	}
	return entry, file, nil
}

func lockStaleReady(file *os.File) error {
	if err := tryReadyLock(file); err != nil {
		if readyLockBusy(err) {
			return errors.New("orch ready file is owned by a live publisher")
		}
		return errors.New("lock existing orch ready file")
	}
	return nil
}

// restoreQuarantinedReady restores only when the original name is absent. If
// another entry already occupies it, the quarantined inode is intentionally
// retained for recovery rather than overwritten or removed.
func restoreQuarantinedReady(quarantinePath, path string) error {
	if err := os.Link(quarantinePath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("orch ready replacement appeared during takeover")
		}
		return errors.New("restore quarantined orch ready file")
	}
	if err := os.Remove(quarantinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("remove quarantined orch ready link")
	}
	return nil
}

// removeOwnedReady 仅移除本次创建的 inode，避免删除被其他实例替换的发现信息。
func removeOwnedReady(path string, expected os.FileInfo) error {
	return removeOwnedReadyWithRename(path, expected, os.Rename)
}

// removeOwnedReadyWithRename 只清理本次发布 inode，并以隔离 rename 防止替换竞态。
func removeOwnedReadyWithRename(path string, expected os.FileInfo, renameFn func(string, string) error) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(expected, current) {
		return errors.New("orch ready file identity changed; refusing cleanup")
	}
	quarantineDir, err := os.MkdirTemp(filepath.Dir(path), ".orch-ready-cleanup-*")
	if err != nil {
		return errors.New("create orch ready cleanup quarantine")
	}
	quarantinePath := filepath.Join(quarantineDir, filepath.Base(path))
	cleanupQuarantine := func() error { return os.Remove(quarantineDir) }
	if err := renameFn(path, quarantinePath); err != nil {
		return errors.Join(errors.New("detach owned orch ready file"), cleanupQuarantine())
	}
	if err := verifyOwnedReadyMove(quarantinePath, path, expected); err != nil {
		return err
	}
	if err := os.Remove(quarantinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("remove owned orch ready file")
	}
	return cleanupQuarantine()
}

func verifyOwnedReadyMove(quarantinePath, path string, expected os.FileInfo) error {
	moved, statErr := os.Lstat(quarantinePath)
	if statErr == nil && moved.Mode().IsRegular() && os.SameFile(expected, moved) {
		return nil
	}
	restoreErr := restoreQuarantinedReady(quarantinePath, path)
	if restoreErr != nil {
		return errors.Join(errors.New("orch ready file identity changed; refusing cleanup"), restoreErr)
	}
	return errors.Join(errors.New("orch ready file identity changed; refusing cleanup"), removeQuarantineDir(filepath.Dir(quarantinePath)))
}

func removeQuarantineDir(path string) error {
	return os.Remove(path)
}
