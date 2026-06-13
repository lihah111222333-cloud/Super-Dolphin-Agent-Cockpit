package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	consolidationLockFileName   = ".consolidation.lock"
	defaultConsolidationLockTTL = 30 * time.Minute
)

var ErrConsolidationLocked = errors.New("memory consolidation already running")

type consolidationLockRecord struct {
	PID        int    `json:"pid"`
	AcquiredAt string `json:"acquired_at,omitempty"`
}

type consolidationLockOptions struct {
	Now func() time.Time
	PID int
	TTL time.Duration
}

type consolidationLockGuard struct {
	path          string
	now           func() time.Time
	previousMtime time.Time
	hadPrevious   bool
}

// acquireConsolidationLock 处理acquireconsolidation锁。
func acquireConsolidationLock(root string, opts consolidationLockOptions) (*consolidationLockGuard, error) {
	lockPath, err := consolidationLockPath(root)
	if err != nil {
		return nil, err
	}
	nowFn, pid, ttl := resolvedConsolidationLockOptions(opts)
	now := nowFn()
	if ttl <= 0 {
		ttl = defaultConsolidationLockTTL
	}

	guard := &consolidationLockGuard{path: lockPath, now: nowFn}
	if info, statErr := os.Stat(lockPath); statErr == nil {
		guard.hadPrevious = true
		guard.previousMtime = info.ModTime()
		if !isConsolidationLockStale(info.ModTime(), now, ttl) {
			return nil, fmt.Errorf("%w: %s", ErrConsolidationLocked, lockPath)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}

	record := consolidationLockRecord{
		PID:        pid,
		AcquiredAt: now.UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomicFile(lockPath, raw, 0o644); err != nil {
		return nil, err
	}
	if err := os.Chtimes(lockPath, now, now); err != nil {
		return nil, err
	}
	return guard, nil
}

func consolidationLockPath(root string) (string, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return "", err
	}
	return ValidateMemoryWritePath(normalizedRoot, filepath.Join(normalizedRoot, consolidationLockFileName))
}

func loadConsolidationLockRecord(path string) (consolidationLockRecord, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return consolidationLockRecord{}, time.Time{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return consolidationLockRecord{}, time.Time{}, err
	}
	if len(raw) == 0 {
		return consolidationLockRecord{}, info.ModTime(), nil
	}
	var record consolidationLockRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return consolidationLockRecord{}, time.Time{}, err
	}
	return record, info.ModTime(), nil
}

func isConsolidationLockStale(modTime, now time.Time, ttl time.Duration) bool {
	if modTime.IsZero() {
		return true
	}
	if now.IsZero() {
		now = time.Now()
	}
	if ttl <= 0 {
		ttl = defaultConsolidationLockTTL
	}
	return now.Sub(modTime) >= ttl
}

// Touch 处理touch。
func (g *consolidationLockGuard) Touch() error {
	if g == nil || g.path == "" {
		return nil
	}
	now := g.now()
	if now.IsZero() {
		now = time.Now()
	}
	if err := os.Chtimes(g.path, now, now); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RollbackMtime 处理rollbackmtime。
func (g *consolidationLockGuard) RollbackMtime() error {
	if g == nil || g.path == "" {
		return nil
	}
	if g.hadPrevious {
		if err := os.Chtimes(g.path, g.previousMtime, g.previousMtime); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Release 释放锁、租约或资源。
func (g *consolidationLockGuard) Release() error {
	if g == nil || g.path == "" {
		return nil
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Complete 完成记忆。
func (g *consolidationLockGuard) Complete(committed bool) error {
	if committed {
		return g.Release()
	}
	return g.RollbackMtime()
}

func resolvedConsolidationLockOptions(opts consolidationLockOptions) (func() time.Time, int, time.Duration) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	pid := opts.PID
	if pid <= 0 {
		pid = os.Getpid()
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultConsolidationLockTTL
	}
	return now, pid, ttl
}

const (
	diskStoreLockFileName      = ".memory.lock"
	diskStoreLockRetryInterval = 25 * time.Millisecond
	diskStoreLockTimeout       = 5 * time.Second
)

// diskLockCoordinator provides process-scoped mutex coordination for
// file-level memory locks. It MUST be instantiated once (via fx) and
// shared across all callers — never as a package-level global.
type diskLockCoordinator struct {
	locks            sync.Map
	crossScopeWarned sync.Map
}

func newDiskLockCoordinator() *diskLockCoordinator {
	return &diskLockCoordinator{}
}

func (c *diskLockCoordinator) markCrossScopeSameNameWarned(name string) bool {
	if c == nil || strings.TrimSpace(name) == "" {
		return false
	}
	_, loaded := c.crossScopeWarned.LoadOrStore(name, struct{}{})
	return !loaded
}

func (c *diskLockCoordinator) withDiskStoreLock(root string, fn func() error) (err error) {
	mutexValue, _ := c.locks.LoadOrStore(root, &sync.Mutex{})
	mutex := mutexValue.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	lockedFile, err := acquireMemoryRootFileLock(root, diskStoreLockTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeMemoryRootFileLock(lockedFile); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return fn()
}
func acquireMemoryRootFileLock(root string, timeout time.Duration) (*os.File, error) {
	lockPath, err := ValidateMemoryWritePath(root, filepath.Join(root, diskStoreLockFileName))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := waitForMemoryRootFileLock(file, timeout); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// waitForMemoryRootFileLock 为记忆根目录文件锁等待记忆。
func waitForMemoryRootFileLock(file *os.File, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = diskStoreLockTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		err := tryAcquireMemoryFileLock(file)
		if err == nil {
			return nil
		}
		if !isMemoryFileLockBusy(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s", ErrMemoryLockTimeout, file.Name())
		}
		time.Sleep(diskStoreLockRetryInterval)
	}
}

func closeMemoryRootFileLock(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := releaseMemoryFileLock(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
