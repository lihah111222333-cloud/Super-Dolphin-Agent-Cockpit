package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestAutoDreamStopHookNoOpWhenKairosActive(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	store := &autoDreamThreadStoreStub{
		thread: newAutoDreamRootThread(t, "thread-current", now, map[string]any{
			"threadKind":   "main",
			"sessionFlags": map[string]any{"memory_kairos": true},
		}),
		threads: autoDreamOtherRootThreads(t, now, 6),
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root, Features: MemoryFeatureFlags{Kairos: true}},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = func(context.Context, string) (string, error) {
		t.Fatal("extractFn should not be called when Kairos gate is active")
		return "", nil
	}

	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if started {
		t.Fatal("maybeScheduleAutoDream() = true, want false when KairosActive")
	}
	if got := hooks.dreamTaskSnapshot(); got.Running {
		t.Fatalf("dream task snapshot = %#v, want idle", got)
	}
}

func TestAutoDreamStopHookRequiresMinSessionsAndExcludesCurrent(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	writeExtractFixture(t, filepath.Join(root, "feedback", "keep-answers-short.md"), testMemoryEntry(
		"Keep answers short",
		"legacy",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: older guidance.",
	))
	if err := recordConsolidation(root, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("recordConsolidation() error = %v", err)
	}
	store := &autoDreamThreadStoreStub{
		thread: newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		threads: append(
			autoDreamOtherRootThreads(t, now, 4),
			*newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		),
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	calls := 0
	hooks.extractFn = func(_ context.Context, prompt string) (string, error) {
		calls++
		if prompt == "" {
			t.Fatal("auto-dream prompt should not be empty")
		}
		return `{"memories":[{"content":"Keep answers short\nWhy: default to concise responses.","type":"feedback"}]}`, nil
	}

	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream(first) error = %v", err)
	}
	if started {
		t.Fatal("maybeScheduleAutoDream(first) = true, want false with only four other sessions")
	}
	if calls != 0 {
		t.Fatalf("extractFn calls after first schedule = %d, want 0", calls)
	}

	store.threads = append(store.threads, *newAutoDreamRootThread(t, "thread-5", now.Add(-time.Minute), map[string]any{"threadKind": "main"}))
	now = now.Add(11 * time.Minute)
	started, err = hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream(second) error = %v", err)
	}
	if !started {
		t.Fatal("maybeScheduleAutoDream(second) = false, want true after five other sessions")
	}
	if err := hooks.waitDreamTask(context.Background()); err != nil {
		t.Fatalf("waitDreamTask() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("extractFn calls = %d, want 1", calls)
	}
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		t.Fatalf("loadConsolidationStamp() error = %v", err)
	}
	if got := stamp.lastSuccessTime(); got.IsZero() {
		t.Fatal("lastSuccessTime() = zero, want recorded consolidation success")
	}
}

func TestConsolidationRollbackOnExtractFailureRestoresPreviousLockMtime(t *testing.T) {
	root := newTestMemoryRoot(t)
	cfg := &Config{Enabled: true, RootDir: root}
	writeExtractFixture(t, filepath.Join(root, "feedback", "keep-answers-short.md"), testMemoryEntry(
		"Keep answers short",
		"legacy",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: older guidance.",
	))
	lockPath, err := consolidationLockPath(root)
	if err != nil {
		t.Fatalf("consolidationLockPath() error = %v", err)
	}
	previous := time.Date(2026, 4, 14, 9, 0, 0, 0, time.UTC)
	if err := os.WriteFile(lockPath, []byte(`{"pid":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile(lock) error = %v", err)
	}
	if err := os.Chtimes(lockPath, previous, previous); err != nil {
		t.Fatalf("Chtimes(lock) error = %v", err)
	}
	consolidator := newAutoDreamConsolidator(NewMemoryExtractor(), nil)
	consolidator.cfg = cfg
	err = consolidator.consolidateWithOptions(context.Background(), root, func(context.Context, string) (string, error) {
		return "", errors.New("extract failed")
	}, consolidationRunOptions{
		cfg: cfg,
		now: func() time.Time { return previous.Add(2 * defaultConsolidationLockTTL) },
	})
	if err == nil {
		t.Fatal("consolidateWithOptions() error = nil, want extract failure")
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("Stat(lock after extract failure) error = %v", err)
	}
	if !info.ModTime().Equal(previous) {
		t.Fatalf("lock mtime after extract failure = %s, want %s", info.ModTime(), previous)
	}
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		t.Fatalf("loadConsolidationStamp() error = %v", err)
	}
	if got := stamp.lastSuccessTime(); !got.IsZero() {
		t.Fatalf("lastSuccessTime() = %s, want zero on extract failure", got)
	}
}

func TestAutoDreamKillCancelsRunningTask(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	if err := recordConsolidation(root, now.Add(-72*time.Hour)); err != nil {
		t.Fatalf("recordConsolidation() error = %v", err)
	}
	writeExtractFixture(t, filepath.Join(root, "project", "build-guidance.md"), testMemoryEntry(
		"Build guidance",
		"guarded",
		MemoryTypeProject,
		"Build guidance\nWhy: keep build commands guarded.",
	))
	store := &autoDreamThreadStoreStub{
		thread:  newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		threads: autoDreamOtherRootThreads(t, now, 5),
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	startedCall := make(chan struct{})
	hooks.extractFn = func(ctx context.Context, prompt string) (string, error) {
		close(startedCall)
		<-ctx.Done()
		return "", ctx.Err()
	}

	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if !started {
		t.Fatal("maybeScheduleAutoDream() = false, want true")
	}
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("extractFn was not invoked")
	}
	waitForDreamPhase(t, hooks, dreamTaskPhaseUpdating)
	if !hooks.killDreamTask() {
		t.Fatal("killDreamTask() = false, want true")
	}
	if err := hooks.waitDreamTask(context.Background()); err != nil {
		t.Fatalf("waitDreamTask() error = %v", err)
	}
	if got := hooks.dreamTaskSnapshot(); got.Running {
		t.Fatalf("dream task snapshot after kill = %#v, want idle", got)
	}
}

func TestAutoDreamScanThrottleSkipsRepeatedStops(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	if err := recordConsolidationScan(root, now); err != nil {
		t.Fatalf("recordConsolidationScan() error = %v", err)
	}
	store := &autoDreamThreadStoreStub{
		thread:  newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		threads: autoDreamOtherRootThreads(t, now, 6),
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now.Add(5 * time.Minute) }
	hooks.extractFn = func(context.Context, string) (string, error) {
		t.Fatal("extractFn should not be called while scan throttle is active")
		return "", nil
	}

	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if started {
		t.Fatal("maybeScheduleAutoDream() = true, want false during throttle window")
	}
}

type autoDreamThreadStoreStub struct {
	thread  *threadstore.Thread
	threads []threadstore.Thread
}

func (s *autoDreamThreadStoreStub) GetByThreadID(context.Context, string) (*threadstore.Thread, error) {
	if s == nil || s.thread == nil {
		return nil, errors.New("thread not found")
	}
	return s.thread, nil
}

func (s *autoDreamThreadStoreStub) ListAll(context.Context) ([]threadstore.Thread, error) {
	if s == nil {
		return nil, nil
	}
	out := append([]threadstore.Thread(nil), s.threads...)
	return out, nil
}

func newAutoDreamRootThread(t *testing.T, threadID string, observedAt time.Time, runtime map[string]any) *threadstore.Thread {
	t.Helper()
	finishedAt := observedAt.Unix()
	return &threadstore.Thread{
		ThreadID:       threadID,
		Cwd:            t.TempDir(),
		UpdatedAt:      observedAt.Unix(),
		FinishedAt:     &finishedAt,
		ConfigOverride: mustStoredRuntimeConfig(t, runtime),
	}
}

func autoDreamOtherRootThreads(t *testing.T, now time.Time, count int) []threadstore.Thread {
	t.Helper()
	threads := make([]threadstore.Thread, 0, count)
	for i := 0; i < count; i++ {
		observedAt := now.Add(-time.Duration(i+1) * time.Minute)
		thread := newAutoDreamRootThread(t, "thread-other-"+time.Date(2000, 1, 1, 0, 0, i+1, 0, time.UTC).Format("150405"), observedAt, map[string]any{"threadKind": "main"})
		threads = append(threads, *thread)
	}
	return threads
}

func waitForDreamPhase(t *testing.T, hooks *MemoryLifecycleHooks, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := hooks.dreamTaskSnapshot(); got.Phase == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("dream task phase did not reach %q: %#v", want, hooks.dreamTaskSnapshot())
}
