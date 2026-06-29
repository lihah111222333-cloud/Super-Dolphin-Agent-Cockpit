package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	"github.com/fsnotify/fsnotify"
	"log/slog"
)

// teamSync watcher 的防抖和同步写入抑制窗口。
const (
	teamSyncWatcherDebounce    = 300 * time.Millisecond
	teamSyncWatcherSuppressFor = time.Second
	teamSyncWatcherMaxDirs     = 2048
	teamSyncWatcherMaxFiles    = 10000
	teamSyncWatcherMaxBytes    = 64 << 20
)

type teamSyncWatcherRootCaps struct {
	MaxDirs  int
	MaxFiles int
	MaxBytes int64
}

type teamSyncWatcherHealthSnapshot struct {
	WatchedDirs        int
	LastCapViolation   string
	LastCapViolationAt time.Time
}

type teamSyncWatcherRootStats struct {
	Dirs  int
	Files int
	Bytes int64
}

// teamSyncWatcher 监听团队记忆目录变化，并把稳定后的本地变更推送到远端。
// watched 和 suppressedPaths 由 mu 保护；closed/done 控制 loop 生命周期，Close 可选择最终 flush。
type teamSyncWatcher struct {
	service       *TeamSyncService
	logger        *slog.Logger
	root          string
	canonicalRoot string
	watcher       *fsnotify.Watcher
	debounce      time.Duration
	now           func() time.Time
	caps          teamSyncWatcherRootCaps

	mu                 sync.Mutex
	watched            map[string]struct{}
	suppressedPaths    map[string]time.Time
	lastCapViolation   string
	lastCapViolationAt time.Time
	closed             chan struct{}
	done               chan struct{}
	loopCtx            context.Context
	loopCancel         context.CancelFunc
}

// newTeamSyncWatcher 创建团队记忆目录 watcher。
// 创建时会解析 canonicalRoot 并递归注册目录；遇到符号链接或不可解析路径直接失败。
func newTeamSyncWatcher(service *TeamSyncService, root string, logger *slog.Logger) (*teamSyncWatcher, error) {
	return newTeamSyncWatcherWithCaps(service, root, logger, defaultTeamSyncWatcherRootCaps())
}

func defaultTeamSyncWatcherRootCaps() teamSyncWatcherRootCaps {
	return teamSyncWatcherRootCaps{MaxDirs: teamSyncWatcherMaxDirs, MaxFiles: teamSyncWatcherMaxFiles, MaxBytes: teamSyncWatcherMaxBytes}
}

// newTeamSyncWatcherWithCaps 创建带 root 上限的 watcher，供测试缩小 cap 验证 fail-fast。
func newTeamSyncWatcherWithCaps(service *TeamSyncService, root string, logger *slog.Logger, caps teamSyncWatcherRootCaps) (*teamSyncWatcher, error) {
	canonicalRoot, err := resolveTeamMemRealPath(root, invalidTeamMemWritePath)
	if err != nil {
		return nil, err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	loopCtx, loopCancel := context.WithCancel(context.Background())
	w := &teamSyncWatcher{
		service:         service,
		logger:          logger,
		root:            root,
		canonicalRoot:   canonicalRoot,
		watcher:         watcher,
		debounce:        teamSyncWatcherDebounce,
		now:             time.Now,
		caps:            normalizeTeamSyncWatcherRootCaps(caps),
		watched:         map[string]struct{}{},
		suppressedPaths: map[string]time.Time{},
		closed:          make(chan struct{}),
		done:            make(chan struct{}),
		loopCtx:         loopCtx,
		loopCancel:      loopCancel,
	}
	if err := w.addRecursive(root); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	return w, nil
}

func normalizeTeamSyncWatcherRootCaps(caps teamSyncWatcherRootCaps) teamSyncWatcherRootCaps {
	if caps.MaxDirs <= 0 {
		caps.MaxDirs = teamSyncWatcherMaxDirs
	}
	if caps.MaxFiles <= 0 {
		caps.MaxFiles = teamSyncWatcherMaxFiles
	}
	if caps.MaxBytes <= 0 {
		caps.MaxBytes = teamSyncWatcherMaxBytes
	}
	return caps
}

// Start 启动 watcher loop；loop 使用 safego 托管，异常会进入统一日志。
func (w *teamSyncWatcher) Start() {
	if w == nil {
		return
	}
	safego.Go(w.loopCtx, w.logger, "memory.teamSyncWatcher.loop", func(context.Context) {
		w.loop()
	})
}

// Close 关闭 watcher loop 并等待退出。
// flush 为 true 时会在 watcher 停止后推送一次本地变更，使用 WithoutCancel 避免被 loop 取消影响。
func (w *teamSyncWatcher) Close(ctx context.Context, flush bool) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	w.mu.Unlock()
	if w.loopCancel != nil {
		w.loopCancel()
	}
	select {
	case <-w.done:
	case <-contextDoneChan(ctx):
		return ctx.Err()
	}
	if flush {
		_, err := w.service.pushLocalChanges(contextWithoutWatcherCancel(ctx), TeamSyncTriggerWatcher)
		return err
	}
	return nil
}

// Suppress 暂时忽略指定路径的 fsnotify 事件。
// 远端拉取写盘会触发本地事件，抑制窗口避免把同步自身的写入再次推送到远端。
func (w *teamSyncWatcher) Suppress(paths ...string) {
	if w == nil || len(paths) == 0 {
		return
	}
	expiry := w.now().Add(teamSyncWatcherSuppressFor)
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, path := range paths {
		if cleaned := cleanWatchPath(path); cleaned != "" {
			w.suppressedPaths[cleaned] = expiry
		}
	}
}

// suppressed 判断当前是否仍有未过期的抑制路径。
func (w *teamSyncWatcher) suppressed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cleanupSuppressedLocked()
	return len(w.suppressedPaths) > 0
}

// loop 串行处理 watcher 事件、错误和防抖 timer。
// 退出时关闭 done 并释放底层 fsnotify watcher。
func (w *teamSyncWatcher) loop() {
	defer close(w.done)
	defer w.closeWatcher()
	timer := newStoppedTeamSyncTimer()
	var dirty bool
	for {
		if w.handleLoopIteration(timer, &dirty) {
			return
		}
	}
}

// newStoppedTeamSyncTimer 创建默认停止的 timer，避免 loop 启动后立即触发 push。
func newStoppedTeamSyncTimer() *time.Timer {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	return timer
}

// handleLoopIteration 处理 watcher loop 的单次 select，返回 true 表示 loop 应退出。
func (w *teamSyncWatcher) handleLoopIteration(timer *time.Timer, dirty *bool) bool {
	select {
	case <-w.closed:
		return true
	case err, ok := <-w.watcher.Errors:
		return w.handleWatcherLoopError(err, ok)
	case event, ok := <-w.watcher.Events:
		return w.handleWatcherLoopEvent(timer, dirty, event, ok)
	case <-timer.C:
		w.flushWatcherLoopPush(dirty)
		return false
	}
}

// handleWatcherLoopError 记录 fsnotify 错误并 fail-closed 退出 loop。
func (w *teamSyncWatcher) handleWatcherLoopError(err error, ok bool) bool {
	if ok && err != nil {
		w.warn("team sync watcher failed", "error", err)
	}
	return true
}

// handleWatcherLoopEvent 处理 fsnotify 事件，并在检测到变更时重置防抖 timer。
func (w *teamSyncWatcher) handleWatcherLoopEvent(timer *time.Timer, dirty *bool, event fsnotify.Event, ok bool) bool {
	if !ok {
		return true
	}
	changed, err := w.handleEvent(event)
	if err != nil {
		w.warn("team sync watcher fail-closed", "error", err)
		return true
	}
	if changed {
		*dirty = true
		resetTeamSyncTimer(timer, w.debounce)
	}
	return false
}

// flushWatcherLoopPush 在防抖窗口结束后推送本地变更。
// 如果 loop context 已取消则跳过，Close(flush=true) 会在 loop 退出后另做最终推送。
func (w *teamSyncWatcher) flushWatcherLoopPush(dirty *bool) {
	if !*dirty {
		return
	}
	*dirty = false
	pushCtx := w.loopCtx
	if pushCtx == nil {
		pushCtx = context.Background()
	}
	if pushCtx.Err() != nil {
		return
	}
	if _, err := w.service.pushLocalChanges(pushCtx, TeamSyncTriggerWatcher); err != nil {
		w.warn("team sync watcher push failed", "error", err)
	}
}

// handleEvent 校验单个 fsnotify 事件是否代表需要推送的本地变更。
// 根目录真实路径发生漂移时 fail-closed，防止符号链接替换后继续监听错误位置。
func (w *teamSyncWatcher) handleEvent(event fsnotify.Event) (bool, error) {
	if w == nil || w.service == nil {
		return false, nil
	}
	if err := w.ensureStableRoot(); err != nil {
		return false, err
	}
	if event.Op&fsnotify.Create != 0 {
		dir, err := pathIsDir(event.Name)
		if err != nil {
			return false, err
		}
		if dir {
			return false, w.addRecursive(event.Name)
		}
	}
	cleaned, ok, err := w.eventPath(event.Name)
	if err != nil || !ok {
		return false, err
	}
	if w.isSuppressed(cleaned) {
		return false, nil
	}
	return true, nil
}

// eventPath 规范化事件路径，并过滤根外、内部状态文件和空路径事件。
func (w *teamSyncWatcher) eventPath(path string) (string, bool, error) {
	cleaned := cleanWatchPath(path)
	if cleaned == "" {
		return "", false, nil
	}
	rel, err := filepath.Rel(w.root, cleaned)
	if err != nil {
		return "", false, err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || rel == "" {
		return "", false, nil
	}
	if len(rel) >= 3 && rel[:3] == "../" {
		return "", false, nil
	}
	if shouldIgnoreTeamSyncPath(rel) {
		return cleaned, false, nil
	}
	return cleaned, true, nil
}

// detectDirty 重新扫描本地 checksum，判断是否偏离最近同步状态。
func (w *teamSyncWatcher) detectDirty() (bool, error) {
	if w == nil || w.service == nil {
		return false, nil
	}
	if err := w.ensureStableRoot(); err != nil {
		return false, err
	}
	checksum, err := w.service.scanCurrentLocalChecksum(w.root)
	if err != nil {
		return false, err
	}
	return checksum != w.service.syncedChecksum(), nil
}

// ensureStableRoot 确认团队记忆根目录没有在 watcher 生命周期内漂移。
func (w *teamSyncWatcher) ensureStableRoot() error {
	current, err := resolveTeamMemRealPath(w.root, invalidTeamMemWritePath)
	if err != nil {
		return err
	}
	if filepath.Clean(current) != filepath.Clean(w.canonicalRoot) {
		return fmt.Errorf("team sync watcher root drift detected")
	}
	return nil
}

// addRecursive 递归注册目录 watcher，并拒绝符号链接目录。
func (w *teamSyncWatcher) addRecursive(root string) error {
	stats := teamSyncWatcherRootStats{}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: watcher symlink path is not allowed", ErrInvalidTeamMemWritePath)
		}
		if !d.IsDir() {
			if err := w.countWatchedFile(path, &stats); err != nil {
				return err
			}
			return nil
		}
		stats.Dirs++
		if err := w.checkRootCaps(stats); err != nil {
			return err
		}
		return w.addWatch(path)
	})
}

func (w *teamSyncWatcher) countWatchedFile(path string, stats *teamSyncWatcherRootStats) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stats.Files++
	stats.Bytes += info.Size()
	return w.checkRootCaps(*stats)
}

func (w *teamSyncWatcher) checkRootCaps(stats teamSyncWatcherRootStats) error {
	caps := w.caps
	if caps.MaxDirs > 0 && stats.Dirs > caps.MaxDirs {
		return w.recordCapViolation(fmt.Sprintf("team sync watcher root cap exceeded: dirs=%d max_dirs=%d", stats.Dirs, caps.MaxDirs))
	}
	if caps.MaxFiles > 0 && stats.Files > caps.MaxFiles {
		return w.recordCapViolation(fmt.Sprintf("team sync watcher root cap exceeded: files=%d max_files=%d", stats.Files, caps.MaxFiles))
	}
	if caps.MaxBytes > 0 && stats.Bytes > caps.MaxBytes {
		return w.recordCapViolation(fmt.Sprintf("team sync watcher root cap exceeded: bytes=%d max_bytes=%d", stats.Bytes, caps.MaxBytes))
	}
	return nil
}

func (w *teamSyncWatcher) recordCapViolation(message string) error {
	w.mu.Lock()
	w.lastCapViolation = message
	w.lastCapViolationAt = w.now()
	w.mu.Unlock()
	return fmt.Errorf("%w: %s", ErrInvalidTeamMemWritePath, message)
}

// HealthSnapshot 返回 watcher 当前目录数和最近 root cap 违规。
func (w *teamSyncWatcher) HealthSnapshot() teamSyncWatcherHealthSnapshot {
	if w == nil {
		return teamSyncWatcherHealthSnapshot{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return teamSyncWatcherHealthSnapshot{
		WatchedDirs:        len(w.watched),
		LastCapViolation:   w.lastCapViolation,
		LastCapViolationAt: w.lastCapViolationAt,
	}
}

// addWatch 注册单个目录，重复路径会被去重。
func (w *teamSyncWatcher) addWatch(path string) error {
	cleaned := cleanWatchPath(path)
	if cleaned == "" {
		return nil
	}
	w.mu.Lock()
	if _, ok := w.watched[cleaned]; ok {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()
	if err := w.watcher.Add(cleaned); err != nil {
		return err
	}
	w.mu.Lock()
	w.watched[cleaned] = struct{}{}
	w.mu.Unlock()
	return nil
}

// isSuppressed 判断路径是否还在同步写入抑制窗口内。
func (w *teamSyncWatcher) isSuppressed(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cleanupSuppressedLocked()
	expiry, ok := w.suppressedPaths[path]
	return ok && w.now().Before(expiry)
}

// cleanupSuppressedLocked 清理已过期的 suppress 记录。
func (w *teamSyncWatcher) cleanupSuppressedLocked() {
	now := w.now()
	for path, expiry := range w.suppressedPaths {
		if !now.Before(expiry) {
			delete(w.suppressedPaths, path)
		}
	}
}

// closeWatcher 释放底层 fsnotify watcher，忽略重复关闭错误。
func (w *teamSyncWatcher) closeWatcher() {
	if w != nil && w.watcher != nil {
		_ = w.watcher.Close()
	}
}

// warn 通过注入 logger 记录 watcher 警告；logger 为空时静默。
func (w *teamSyncWatcher) warn(message string, args ...any) {
	if w != nil && w.logger != nil {
		w.logger.Warn(message, args...)
	}
}

// resetTeamSyncTimer 安全重置防抖 timer，并排空可能已经触发的事件。
func resetTeamSyncTimer(timer *time.Timer, duration time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

// cleanWatchPath 标准化 watcher 路径，非法路径返回空字符串。
func cleanWatchPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned, err := shared.CleanAbsolutePath(path)
	if err != nil {
		return ""
	}
	return cleaned
}

// pathIsDir 使用 Lstat 判断路径是否为目录，并拒绝符号链接。
func pathIsDir(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, err
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: watcher symlink path is not allowed", ErrInvalidTeamMemWritePath)
	}
	return info.IsDir(), nil
}

// contextWithoutWatcherCancel 返回不受 watcher loop cancel 影响的 context。
func contextWithoutWatcherCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

// contextDoneChan 返回 context 的 Done channel，nil context 返回 nil channel。
func contextDoneChan(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}
