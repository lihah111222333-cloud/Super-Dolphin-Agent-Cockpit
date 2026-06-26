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

// ErrConsolidationLocked 表示同一 memory root 已有未过期 consolidation 锁。
var ErrConsolidationLocked = errors.New("memory consolidation already running")

// consolidationLockRecord 是写入 .consolidation.lock 的可审计记录。
type consolidationLockRecord struct {
	PID        int    `json:"pid"`
	AcquiredAt string `json:"acquired_at,omitempty"`
}

// consolidationLockOptions 提供锁获取时的时间、进程和 TTL 覆盖。
// 测试可注入 Now/PID；生产路径使用当前进程和默认 TTL。
type consolidationLockOptions struct {
	Now func() time.Time
	PID int
	TTL time.Duration
}

// consolidationLockGuard 记录本次获取锁前的文件状态。
// consolidation 失败时用它恢复旧锁 mtime 或删除新锁，成功时释放锁文件。
type consolidationLockGuard struct {
	path          string
	now           func() time.Time
	previousMtime time.Time
	hadPrevious   bool
}

// acquireConsolidationLock 在 memory root 下创建或接管过期的 consolidation 锁。
// 未过期锁会 fail-fast；成功后调用方必须通过 guard.Complete 释放或回滚锁状态。
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

// consolidationLockPath 计算并校验 consolidation 锁文件路径。
// 锁文件必须位于规范化后的 memory root 内，防止路径穿越写入。
func consolidationLockPath(root string) (string, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return "", err
	}
	return ValidateMemoryWritePath(normalizedRoot, filepath.Join(normalizedRoot, consolidationLockFileName))
}

// loadConsolidationLockRecord 读取锁文件内容和 mtime。
// 空文件兼容为“只有 mtime 的锁”，供过期判断继续使用。
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

// isConsolidationLockStale 判断锁文件是否超过 TTL。
// 零值时间和非正 TTL 使用保守默认，避免调用方传错参数导致锁永不过期。
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

// Touch 刷新当前 consolidation 锁文件 mtime。
// 锁文件被外部删除时视为无事可做，其他文件系统错误会返回给调用方。
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

// RollbackMtime 在 consolidation 未提交时恢复锁文件状态。
// 如果本次获取前已有锁则恢复旧 mtime；否则删除本次创建的新锁文件。
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

// Release 删除当前 consolidation 锁文件。
// 文件已不存在时视为成功，避免清理路径因重复调用而报错。
func (g *consolidationLockGuard) Release() error {
	if g == nil || g.path == "" {
		return nil
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Complete 根据提交结果释放或回滚 consolidation 锁。
// committed=false 时保留原有锁语义，避免失败运行清掉其他进程留下的有效锁。
func (g *consolidationLockGuard) Complete(committed bool) error {
	if committed {
		return g.Release()
	}
	return g.RollbackMtime()
}

// resolvedConsolidationLockOptions 规范化锁选项。
// 缺失字段使用生产默认值，确保所有调用路径都使用同一 TTL 和 PID 语义。
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

// diskLockCoordinator 在进程内协调 memory root 文件锁。
// 它必须由 Fx 注入并在调用方间共享，避免同一进程内多把 mutex 绕过跨进程文件锁。
type diskLockCoordinator struct {
	locks            sync.Map
	crossScopeWarned sync.Map
}

// newDiskLockCoordinator 创建磁盘锁协调器。
// 返回值应作为共享依赖传递，不要在每次写入时临时创建。
func newDiskLockCoordinator() *diskLockCoordinator {
	return &diskLockCoordinator{}
}

// markCrossScopeSameNameWarned 记录同名记忆跨作用域告警是否已发出。
// 返回 true 表示调用方应记录本次首次告警，避免日志刷屏。
func (c *diskLockCoordinator) markCrossScopeSameNameWarned(name string) bool {
	if c == nil || strings.TrimSpace(name) == "" {
		return false
	}
	_, loaded := c.crossScopeWarned.LoadOrStore(name, struct{}{})
	return !loaded
}

// withDiskStoreLock 在进程内 mutex 和跨进程文件锁保护下执行写操作。
// fn 返回前锁文件不会释放；close/unlock 错误会在 fn 成功时向上传递。
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

// acquireMemoryRootFileLock 打开 memory root 锁文件并等待独占锁。
// 获取失败会关闭已打开文件，避免调用方在错误路径泄露文件句柄。
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

// waitForMemoryRootFileLock 在 timeout 内重试获取 memory root 文件锁。
// 非 busy 错误立即返回，busy 超时会携带锁文件名，便于定位卡住的 root。
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

// closeMemoryRootFileLock 先释放文件锁再关闭文件句柄。
// unlock 错误优先返回，因为未释放锁比 close 失败更可能影响后续写入。
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
