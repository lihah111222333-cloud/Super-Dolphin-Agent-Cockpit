package team

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"golang.org/x/sync/errgroup"
)

func newTransitionTestService(t *testing.T) (*TeamSyncService, contract.BuildCtx) {
	t.Helper()
	withTeamMemoryRuntimeReady(t, true)
	projectRoot := t.TempDir()
	teamRoot := filepath.Join(t.TempDir(), teamMemoryRootDirName)
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", teamRoot, err)
	}
	cfg := newTestConfig(teamRoot)
	manager := NewTeamMemoryManager(cfg)
	svc := NewTeamSyncService(cfg, manager, NewTeamMemoryGuard(manager), nil, nil)
	svc.remote = &teamSyncRemoteStub{oauthReady: true}
	svc.resolveRepoSlug = func(_ context.Context, root string) (string, error) {
		return filepath.Base(root), nil
	}
	return svc, contract.BuildCtx{GitRoot: projectRoot}
}

func TestStartSessionCloseFailureRestoresLiveOldWatcher(t *testing.T) {
	svc, oldBuildCtx := newTransitionTestService(t)
	if err := svc.StartSession(context.Background(), "old-thread", oldBuildCtx); err != nil {
		t.Fatalf("StartSession(old) error = %v", err)
	}
	oldWatcher := svc.watcher
	closeErr := errors.New("final flush failed")
	svc.closeWatcher = func(ctx context.Context, watcher *teamSyncWatcher, _ bool) error {
		if err := watcher.Close(ctx, false); err != nil {
			return err
		}
		return closeErr
	}

	err := svc.StartSession(context.Background(), "new-thread", contract.BuildCtx{GitRoot: t.TempDir()})
	if !errors.Is(err, closeErr) {
		t.Fatalf("StartSession(switch) error = %v, want %v", err, closeErr)
	}
	if svc.watcher == nil || svc.watcher == oldWatcher {
		t.Fatal("rollback watcher was not recreated after old watcher stopped")
	}
	if _, ok := svc.sessions["old-thread"]; !ok {
		t.Fatal("old session was not restored")
	}
	if _, ok := svc.sessions["new-thread"]; ok {
		t.Fatal("failed new session leaked into restored runtime")
	}
	svc.closeWatcher = func(ctx context.Context, watcher *teamSyncWatcher, flush bool) error {
		return watcher.Close(ctx, flush)
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestStartSessionCloseAndOldWatcherRebuildFailuresAreJoined(t *testing.T) {
	svc, oldBuildCtx := newTransitionTestService(t)
	if err := svc.StartSession(context.Background(), "old-thread", oldBuildCtx); err != nil {
		t.Fatalf("StartSession(old) error = %v", err)
	}
	svc.mu.Lock()
	svc.state.ServerChecksums = map[string]string{"shared.md": "old"}
	svc.mu.Unlock()
	oldRoot, oldRepoSlug := svc.root, svc.repoSlug
	flushErr := errors.New("final flush failed")
	rollbackErr := errors.New("old watcher rebuild failed")
	svc.closeWatcher = func(ctx context.Context, watcher *teamSyncWatcher, _ bool) error {
		if err := watcher.Close(ctx, false); err != nil {
			return err
		}
		svc.mu.Lock()
		svc.state.ServerChecksums["shared.md"] = "mutated-before-flush-error"
		svc.mu.Unlock()
		return flushErr
	}
	svc.newWatcher = func(*TeamSyncService, string, *slog.Logger) (*teamSyncWatcher, error) {
		return nil, rollbackErr
	}

	err := svc.StartSession(context.Background(), "new-thread", contract.BuildCtx{GitRoot: t.TempDir()})
	if !errors.Is(err, flushErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("StartSession(switch) error = %v, want joined flush and rollback errors", err)
	}
	assertFailedTransitionRestored(t, svc, oldRoot, oldRepoSlug)
}

func assertFailedTransitionRestored(t *testing.T, svc *TeamSyncService, oldRoot, oldRepoSlug string) {
	t.Helper()
	if svc.root != oldRoot || svc.repoSlug != oldRepoSlug {
		t.Fatalf("restored owner = (%q,%q), want (%q,%q)", svc.root, svc.repoSlug, oldRoot, oldRepoSlug)
	}
	if svc.watcher != nil {
		t.Fatal("watcher must remain nil when old watcher rebuild fails")
	}
	if got := svc.state.ServerChecksums["shared.md"]; got != "old" {
		t.Fatalf("restored server checksum = %q, want old snapshot", got)
	}
	if _, ok := svc.sessions["old-thread"]; !ok {
		t.Fatal("old session owner was not restored")
	}
	if _, ok := svc.sessions["new-thread"]; ok {
		t.Fatal("failed new session leaked into restored owner state")
	}
}

func TestStartSessionJoinsSwitchAndRollbackWatcherFailures(t *testing.T) {
	svc, oldBuildCtx := newTransitionTestService(t)
	if err := svc.StartSession(context.Background(), "old-thread", oldBuildCtx); err != nil {
		t.Fatalf("StartSession(old) error = %v", err)
	}
	switchErr := errors.New("new watcher failed")
	rollbackErr := errors.New("rollback watcher failed")
	var calls atomic.Int32
	svc.newWatcher = func(service *TeamSyncService, root string, logger *slog.Logger) (*teamSyncWatcher, error) {
		switch calls.Add(1) {
		case 1:
			return nil, switchErr
		default:
			return nil, rollbackErr
		}
	}

	err := svc.StartSession(context.Background(), "new-thread", contract.BuildCtx{GitRoot: t.TempDir()})
	if !errors.Is(err, switchErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("StartSession(switch) error = %v, want joined switch and rollback errors", err)
	}
	if svc.watcher != nil {
		t.Fatal("watcher must remain nil when rollback recreation fails")
	}
	if _, ok := svc.sessions["old-thread"]; !ok {
		t.Fatal("old session state was not restored")
	}
}

func TestStartSessionPullFailureRestoresOldRuntime(t *testing.T) {
	svc, oldBuildCtx := newTransitionTestService(t)
	if err := svc.StartSession(context.Background(), "old-thread", oldBuildCtx); err != nil {
		t.Fatalf("StartSession(old) error = %v", err)
	}
	oldWatcher := svc.watcher
	pullErr := errors.New("remote pull failed")
	remote, ok := svc.remote.(*teamSyncRemoteStub)
	if !ok {
		t.Fatalf("remote type = %T, want *teamSyncRemoteStub", svc.remote)
	}
	remote.pullHashesFn = func(context.Context, string, string) (TeamSyncHashesResponse, error) {
		return TeamSyncHashesResponse{}, pullErr
	}

	err := svc.StartSession(context.Background(), "new-thread", contract.BuildCtx{GitRoot: t.TempDir()})
	if !errors.Is(err, pullErr) {
		t.Fatalf("StartSession(switch) error = %v, want %v", err, pullErr)
	}
	if svc.watcher == nil || svc.watcher == oldWatcher {
		t.Fatal("pull rollback did not recreate the old runtime watcher")
	}
	if _, ok := svc.sessions["old-thread"]; !ok {
		t.Fatal("old session was not restored after pull failure")
	}
	if _, ok := svc.sessions["new-thread"]; ok {
		t.Fatal("failed new session leaked after pull rollback")
	}
	remote.pullHashesFn = nil
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestStartSessionChecksumFailureRestoresOldRuntime(t *testing.T) {
	svc, oldBuildCtx := newTransitionTestService(t)
	if err := svc.StartSession(context.Background(), "old-thread", oldBuildCtx); err != nil {
		t.Fatalf("StartSession(old) error = %v", err)
	}
	oldWatcher := svc.watcher
	checksumErr := errors.New("checksum persistence failed")
	svc.refreshChecksum = func(*TeamSyncService) error { return checksumErr }

	err := svc.StartSession(context.Background(), "new-thread", contract.BuildCtx{GitRoot: t.TempDir()})
	if !errors.Is(err, checksumErr) {
		t.Fatalf("StartSession(switch) error = %v, want %v", err, checksumErr)
	}
	if svc.watcher == nil || svc.watcher == oldWatcher {
		t.Fatal("checksum rollback did not recreate the old runtime watcher")
	}
	if _, ok := svc.sessions["old-thread"]; !ok {
		t.Fatal("old session was not restored after checksum failure")
	}
	if _, ok := svc.sessions["new-thread"]; ok {
		t.Fatal("failed new session leaked after checksum rollback")
	}
	svc.refreshChecksum = func(runtime *TeamSyncService) error {
		return runtime.refreshLocalChecksumLocked()
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestStartSessionConcurrentSameRuntimeCreatesOneWatcher(t *testing.T) {
	svc, buildCtx := newTransitionTestService(t)
	baseFactory := svc.newWatcher
	var creations atomic.Int32
	svc.newWatcher = func(service *TeamSyncService, root string, logger *slog.Logger) (*teamSyncWatcher, error) {
		creations.Add(1)
		return baseFactory(service, root, logger)
	}
	group, ctx := errgroup.WithContext(context.Background())
	group.Go(func() error { return svc.StartSession(ctx, "thread-1", buildCtx) })
	group.Go(func() error { return svc.StartSession(ctx, "thread-2", buildCtx) })
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent StartSession() error = %v", err)
	}
	if got := creations.Load(); got != 1 {
		t.Fatalf("watcher creations = %d, want 1", got)
	}
	if len(svc.sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(svc.sessions))
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
