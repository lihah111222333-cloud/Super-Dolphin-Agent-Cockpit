package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/fsnotify/fsnotify"
)

const (
	teamSyncWatcherDebounce    = 300 * time.Millisecond
	teamSyncWatcherSuppressFor = time.Second
)

type teamSyncWatcher struct {
	service       *TeamSyncService
	logger        *pkglogger.Logger
	root          string
	canonicalRoot string
	watcher       *fsnotify.Watcher
	debounce      time.Duration
	now           func() time.Time

	mu              sync.Mutex
	watched         map[string]struct{}
	suppressedPaths map[string]time.Time
	closed          chan struct{}
	done            chan struct{}
	loopCtx         context.Context
	loopCancel      context.CancelFunc
}

// newTeamSyncWatcher 创建teamsyncwatcher。
func newTeamSyncWatcher(service *TeamSyncService, root string, logger *pkglogger.Logger) (*teamSyncWatcher, error) {
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

// Start 启动记忆流程。
func (w *teamSyncWatcher) Start() {
	if w == nil {
		return
	}
	safego.Go(w.loopCtx, w.logger, "memory.teamSyncWatcher.loop", func(context.Context) {
		w.loop()
	})
}

// Close 关闭记忆资源。
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

// Suppress 处理suppress。
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

func newStoppedTeamSyncTimer() *time.Timer {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	return timer
}

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

func (w *teamSyncWatcher) handleWatcherLoopError(err error, ok bool) bool {
	if ok && err != nil {
		w.warn("team sync watcher failed", "error", err)
	}
	return true
}

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

// handleEvent 处理事件。
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

// eventPath 处理事件路径。
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

func (w *teamSyncWatcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: watcher symlink path is not allowed", ErrInvalidTeamMemWritePath)
		}
		if !d.IsDir() {
			return nil
		}
		return w.addWatch(path)
	})
}

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

func (w *teamSyncWatcher) isSuppressed(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cleanupSuppressedLocked()
	expiry, ok := w.suppressedPaths[path]
	return ok && w.now().Before(expiry)
}

func (w *teamSyncWatcher) cleanupSuppressedLocked() {
	now := w.now()
	for path, expiry := range w.suppressedPaths {
		if !now.Before(expiry) {
			delete(w.suppressedPaths, path)
		}
	}
}

func (w *teamSyncWatcher) closeWatcher() {
	if w != nil && w.watcher != nil {
		_ = w.watcher.Close()
	}
}

func (w *teamSyncWatcher) warn(message string, args ...any) {
	if w != nil && w.logger != nil {
		w.logger.Warn(message, args...)
	}
}

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

func contextWithoutWatcherCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func contextDoneChan(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}
