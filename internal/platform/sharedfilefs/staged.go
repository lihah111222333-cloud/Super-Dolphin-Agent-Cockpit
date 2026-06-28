package sharedfilefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// PathLocks 按 canonical 磁盘路径串行化 sharedfile 写入和删除。
// 零值可直接使用；锁只覆盖同一进程内的 store 操作，防止本地 publish/rollback 互相撤销。
type PathLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// WithPathLock 在指定路径的互斥区内执行 fn。
// path 必须是 Resolve*Abs 得到的 canonical 绝对路径；fn 的错误会原样返回给调用方。
func (l *PathLocks) WithPathLock(path string, fn func() error) error {
	if l == nil {
		return fn()
	}
	lock := l.lockFor(filepath.Clean(path))
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (l *PathLocks) lockFor(path string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = make(map[string]*sync.Mutex)
	}
	lock := l.locks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		l.locks[path] = lock
	}
	return lock
}

// StagedWrite 表示已经 fsync 到同目录临时文件、但尚未发布到最终路径的正文。
// 调用方必须在 DB 提交成功后 Publish；失败路径调用 Cleanup 只会移除自己的 temp。
type StagedWrite struct {
	finalAbs string
	tempAbs  string
}

// StageWrite 把正文写入最终文件同目录的 staging temp，不覆盖已有正式文件。
func StageWrite(absPath string, data []byte) (*StagedWrite, error) {
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sharedfilefs: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(absPath)+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("sharedfilefs: open staged tmp: %w", err)
	}
	tmpPath := tmp.Name()
	keepTmp := false
	defer func() {
		_ = tmp.Close()
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return nil, fmt.Errorf("sharedfilefs: write staged tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return nil, fmt.Errorf("sharedfilefs: fsync staged tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("sharedfilefs: close staged tmp: %w", err)
	}
	keepTmp = true
	return &StagedWrite{finalAbs: absPath, tempAbs: tmpPath}, nil
}

// Publish 在 DB 索引提交成功后把 staging temp 原子 rename 到最终路径。
func (w *StagedWrite) Publish() error {
	if w == nil || w.tempAbs == "" {
		return nil
	}
	if err := os.Rename(w.tempAbs, w.finalAbs); err != nil {
		return fmt.Errorf("sharedfilefs: publish staged %s -> %s: %w", w.tempAbs, w.finalAbs, err)
	}
	w.tempAbs = ""
	syncDirBestEffort(filepath.Dir(w.finalAbs))
	return nil
}

// Cleanup 删除尚未发布的 staging temp；只清理本 StagedWrite 自己创建的文件。
func (w *StagedWrite) Cleanup() error {
	if w == nil || w.tempAbs == "" {
		return nil
	}
	temp := w.tempAbs
	w.tempAbs = ""
	if err := os.Remove(temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sharedfilefs: cleanup staged tmp %s: %w", temp, err)
	}
	return nil
}

// StagedDelete 表示已经把最终文件移到同目录 tombstone、但尚未最终删除的操作。
// DB 删除失败时 Rollback 会把 tombstone 放回原路径，避免索引失败却正文消失。
type StagedDelete struct {
	finalAbs     string
	tombstoneAbs string
}

// StageDelete 先把现有正文 rename 到 tombstone；目标不存在时返回 no-op staged delete。
func StageDelete(absPath string) (*StagedDelete, error) {
	info, err := os.Lstat(absPath)
	if errors.Is(err, fs.ErrNotExist) {
		return &StagedDelete{finalAbs: absPath}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sharedfilefs: lstat delete target %s: %w", absPath, err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("sharedfilefs: delete target %s is not a regular file", absPath)
	}
	tombstone, err := reserveDeleteTombstone(absPath)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(absPath, tombstone); err != nil {
		return nil, fmt.Errorf("sharedfilefs: stage delete %s -> %s: %w", absPath, tombstone, err)
	}
	syncDirBestEffort(filepath.Dir(absPath))
	return &StagedDelete{finalAbs: absPath, tombstoneAbs: tombstone}, nil
}

// Commit 删除 tombstone；失败时调用方应回滚 DB 并尝试 Rollback。
func (d *StagedDelete) Commit() error {
	if d == nil || d.tombstoneAbs == "" {
		return nil
	}
	tombstone := d.tombstoneAbs
	if err := os.Remove(tombstone); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sharedfilefs: commit delete %s: %w", tombstone, err)
	}
	d.tombstoneAbs = ""
	syncDirBestEffort(filepath.Dir(d.finalAbs))
	return nil
}

// Rollback 把 tombstone 放回最终路径；若最终路径已被其他写入占用则直接报错。
func (d *StagedDelete) Rollback() error {
	if d == nil || d.tombstoneAbs == "" {
		return nil
	}
	if _, err := os.Lstat(d.finalAbs); err == nil {
		return fmt.Errorf("sharedfilefs: rollback delete target %s already exists", d.finalAbs)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sharedfilefs: lstat rollback target %s: %w", d.finalAbs, err)
	}
	if err := os.Rename(d.tombstoneAbs, d.finalAbs); err != nil {
		return fmt.Errorf("sharedfilefs: rollback delete %s -> %s: %w", d.tombstoneAbs, d.finalAbs, err)
	}
	d.tombstoneAbs = ""
	syncDirBestEffort(filepath.Dir(d.finalAbs))
	return nil
}

func reserveDeleteTombstone(absPath string) (string, error) {
	dir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(dir, filepath.Base(absPath)+".delete-*.tmp")
	if err != nil {
		return "", fmt.Errorf("sharedfilefs: reserve delete tombstone: %w", err)
	}
	tombstone := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tombstone)
		return "", fmt.Errorf("sharedfilefs: close delete tombstone: %w", closeErr)
	}
	if removeErr := os.Remove(tombstone); removeErr != nil {
		return "", fmt.Errorf("sharedfilefs: remove reserved tombstone: %w", removeErr)
	}
	return tombstone, nil
}

func syncDirBestEffort(dir string) {
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
}
