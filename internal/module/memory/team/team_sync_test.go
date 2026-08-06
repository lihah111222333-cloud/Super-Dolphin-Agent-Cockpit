package team

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type teamSyncRemoteStub struct {
	mu sync.Mutex

	oauthReady bool

	pullHashesFn func(context.Context, string, string) (TeamSyncHashesResponse, error)
	pullFilesFn  func(context.Context, string, string) (TeamSyncPullResponse, error)
	pushFilesFn  func(context.Context, TeamSyncPushRequest) (TeamSyncPushResponse, error)

	pullHashesCalls int
	pullFilesCalls  int
	pushCalls       int
	pushBatchSizes  []int
}

func (s *teamSyncRemoteStub) OAuthReady(context.Context) bool {
	return s != nil && s.oauthReady
}

func (s *teamSyncRemoteStub) PullHashes(ctx context.Context, repoSlug, etag string) (TeamSyncHashesResponse, error) {
	s.mu.Lock()
	s.pullHashesCalls++
	fn := s.pullHashesFn
	s.mu.Unlock()
	if fn == nil {
		return TeamSyncHashesResponse{}, nil
	}
	return fn(ctx, repoSlug, etag)
}

func (s *teamSyncRemoteStub) PullFiles(ctx context.Context, repoSlug, cursor string) (TeamSyncPullResponse, error) {
	s.mu.Lock()
	s.pullFilesCalls++
	fn := s.pullFilesFn
	s.mu.Unlock()
	if fn == nil {
		return TeamSyncPullResponse{}, nil
	}
	return fn(ctx, repoSlug, cursor)
}

func (s *teamSyncRemoteStub) PushFiles(ctx context.Context, req TeamSyncPushRequest) (TeamSyncPushResponse, error) {
	s.mu.Lock()
	s.pushCalls++
	s.pushBatchSizes = append(s.pushBatchSizes, len(req.Uploads)+len(req.Deletes))
	fn := s.pushFilesFn
	s.mu.Unlock()
	if fn == nil {
		return TeamSyncPushResponse{}, nil
	}
	return fn(ctx, req)
}

type teamSyncInvalidatorStub struct {
	mu      sync.Mutex
	calls   int
	reasons []contract.InvalidateReason
}

func (s *teamSyncInvalidatorStub) Invalidate(_ context.Context, reason contract.InvalidateReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.reasons = append(s.reasons, reason)
	return nil
}

func TestTeamSyncKairosActiveSkipsWatcher(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := newTestConfig(filepath.Join(projectRoot, teamMemoryRootDirName))
	cfg.projectRoot = projectRoot
	manager := NewTeamMemoryManager(cfg)
	withTeamMemoryRuntimeReady(t, manager, true)
	remote := &teamSyncRemoteStub{oauthReady: true}
	invalidator := &teamSyncInvalidatorStub{}
	svc := NewTeamSyncService(cfg, manager, NewTeamMemoryGuard(manager), nil, nil)
	svc.remote = remote
	svc.invalidator = invalidator
	svc.resolveRepoSlug = func(context.Context, string) (string, error) { return "acme/repo", nil }

	buildCtx := contract.BuildCtx{GitRoot: projectRoot, SessionFlags: map[string]bool{"memory_kairos": true}}
	if err := svc.StartSession(context.Background(), "thread-1", buildCtx); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if svc.watcher != nil {
		t.Fatal("watcher started while KairosActive=true")
	}
	if remote.pullHashesCalls != 0 || remote.pullFilesCalls != 0 || remote.pushCalls != 0 {
		t.Fatalf("remote calls = (%d,%d,%d), want all zero", remote.pullHashesCalls, remote.pullFilesCalls, remote.pushCalls)
	}
	if invalidator.calls != 0 {
		t.Fatalf("Invalidate() calls = %d, want 0", invalidator.calls)
	}
}

func TestTeamSyncInitialAndRemotePullInvalidateWithoutSelfPush(t *testing.T) {
	projectRoot := t.TempDir()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	teamRoot := filepath.Join(autoRoot, teamMemoryRootDirName)
	cfg := newTestConfig(teamRoot)
	cfg.projectRoot = projectRoot
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(team root) error = %v", err)
	}
	manager := NewTeamMemoryManager(cfg)
	withTeamMemoryRuntimeReady(t, manager, true)
	guard := NewTeamMemoryGuard(manager)
	invalidator := &teamSyncInvalidatorStub{}
	remote := &teamSyncRemoteStub{oauthReady: true}
	currentContent := "# team\nversion-1\n"
	remote.pullHashesFn = func(context.Context, string, string) (TeamSyncHashesResponse, error) {
		return TeamSyncHashesResponse{Checksums: map[string]string{"shared.md": checksumContent([]byte(currentContent))}}, nil
	}
	remote.pullFilesFn = func(context.Context, string, string) (TeamSyncPullResponse, error) {
		checksum := checksumContent([]byte(currentContent))
		return TeamSyncPullResponse{
			ETag:      "etag-1",
			Checksums: map[string]string{"shared.md": checksum},
			Files:     map[string]TeamSyncFile{"shared.md": {Content: currentContent, Checksum: checksum}},
		}, nil
	}
	svc := NewTeamSyncService(cfg, manager, guard, nil, nil)
	svc.remote = remote
	svc.invalidator = invalidator
	svc.resolveRepoSlug = func(context.Context, string) (string, error) { return "acme/repo", nil }
	buildCtx := contract.BuildCtx{GitRoot: projectRoot}
	if err := svc.StartSession(context.Background(), "thread-1", buildCtx); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer func() { _ = svc.Shutdown(context.Background()) }()
	if invalidator.calls != 1 {
		t.Fatalf("Invalidate() calls after initial pull = %d, want 1", invalidator.calls)
	}
	if remote.pushCalls != 0 {
		t.Fatalf("pushCalls after initial pull = %d, want 0", remote.pushCalls)
	}
	currentContent = "# team\nversion-2\n"
	svc.mu.Lock()
	_, pullErr := svc.pullLocked(context.Background(), TeamSyncTriggerManual)
	svc.mu.Unlock()
	if pullErr != nil {
		t.Fatalf("Pull() error = %v", pullErr)
	}
	if invalidator.calls != 2 {
		t.Fatalf("Invalidate() calls after remote pull = %d, want 2", invalidator.calls)
	}
	if svc.watcher == nil || !svc.watcher.suppressed() {
		t.Fatal("watcher suppression was not activated for self-write apply")
	}
	time.Sleep(teamSyncWatcherDebounce + 100*time.Millisecond)
	if remote.pushCalls != 0 {
		t.Fatalf("pushCalls after self-write pull = %d, want 0", remote.pushCalls)
	}
}
func TestTeamSyncConflictRetryPullsLatestState(t *testing.T) {
	projectRoot := t.TempDir()
	teamRoot := filepath.Join(projectRoot, teamMemoryRootDirName)
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", teamRoot, err)
	}
	writeTeamSyncTestFile(t, filepath.Join(teamRoot, "a.md"), "A")
	cfg := newTestConfig(teamRoot)
	cfg.projectRoot = projectRoot
	manager := NewTeamMemoryManager(cfg)
	guard := NewTeamMemoryGuard(manager)
	remote := &teamSyncRemoteStub{oauthReady: true}
	conflicted := true
	remote.pullHashesFn = func(context.Context, string, string) (TeamSyncHashesResponse, error) {
		checksum := checksumContent([]byte("A"))
		return TeamSyncHashesResponse{ETag: "etag-new", Checksums: map[string]string{"a.md": checksum}}, nil
	}
	remote.pullFilesFn = func(context.Context, string, string) (TeamSyncPullResponse, error) {
		checksum := checksumContent([]byte("A"))
		return TeamSyncPullResponse{
			ETag:      "etag-new",
			Checksums: map[string]string{"a.md": checksum},
			Files:     map[string]TeamSyncFile{"a.md": {Content: "A", Checksum: checksum}},
		}, nil
	}
	remote.pushFilesFn = func(context.Context, TeamSyncPushRequest) (TeamSyncPushResponse, error) {
		if conflicted {
			conflicted = false
			return TeamSyncPushResponse{Conflict: true}, nil
		}
		return TeamSyncPushResponse{ETag: "etag-new", Applied: map[string]string{"a.md": checksumContent([]byte("A"))}}, nil
	}
	store, err := newTeamSyncStateStore(teamRoot)
	if err != nil {
		t.Fatalf("newTeamSyncStateStore() error = %v", err)
	}
	svc := NewTeamSyncService(cfg, manager, guard, nil, nil)
	svc.remote = remote
	svc.resolveRepoSlug = func(context.Context, string) (string, error) { return "acme/repo", nil }
	svc.root = teamRoot
	svc.repoSlug = "acme/repo"
	svc.stateStore = store
	svc.state = SyncState{LastKnownChecksum: "stale", ServerETag: "etag-old", ServerChecksums: map[string]string{"a.md": "old"}}

	result, err := svc.pushLocalChanges(context.Background(), TeamSyncTriggerManual)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !result.Retried {
		t.Fatal("Push().Retried = false, want true")
	}
	if remote.pullHashesCalls == 0 || remote.pullFilesCalls == 0 {
		t.Fatalf("pull calls = (%d,%d), want retry pull to run", remote.pullHashesCalls, remote.pullFilesCalls)
	}
	if remote.pushCalls == 0 {
		t.Fatal("pushCalls = 0, want conflicting push call")
	}
	if svc.state.ServerETag != "etag-new" {
		t.Fatalf("ServerETag = %q, want %q", svc.state.ServerETag, "etag-new")
	}
}

func TestTeamSync413LearnsServerMaxEntriesForNextPush(t *testing.T) {
	projectRoot := t.TempDir()
	teamRoot := filepath.Join(projectRoot, teamMemoryRootDirName)
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", teamRoot, err)
	}
	writeTeamSyncTestFile(t, filepath.Join(teamRoot, "a.md"), "A")
	writeTeamSyncTestFile(t, filepath.Join(teamRoot, "b.md"), "B")
	cfg := newTestConfig(teamRoot)
	cfg.projectRoot = projectRoot
	manager := NewTeamMemoryManager(cfg)
	guard := NewTeamMemoryGuard(manager)
	invalidator := &teamSyncInvalidatorStub{}
	remote := &teamSyncRemoteStub{oauthReady: true}
	remote.pushFilesFn = teamSync413PushFiles
	store, err := newTeamSyncStateStore(teamRoot)
	if err != nil {
		t.Fatalf("newTeamSyncStateStore() error = %v", err)
	}
	svc := NewTeamSyncService(cfg, manager, guard, nil, nil)
	svc.remote = remote
	svc.invalidator = invalidator
	svc.root = teamRoot
	svc.repoSlug = "acme/repo"
	svc.stateStore = store
	svc.state = SyncState{LastKnownChecksum: "stale"}

	first, err := svc.pushLocalChanges(context.Background(), TeamSyncTriggerManual)
	if err != nil {
		t.Fatalf("first Push() error = %v", err)
	}
	assertTeamSync413FirstPush(t, first, svc.state.ServerMaxEntries)
	second, err := svc.pushLocalChanges(context.Background(), TeamSyncTriggerManual)
	if err != nil {
		t.Fatalf("second Push() error = %v", err)
	}
	assertTeamSync413SecondPush(t, second, remote.pushBatchSizes)
}

func TestResolveTeamRepoSlugFailsWhenRemoteOriginMissing(t *testing.T) {
	projectRoot := t.TempDir()
	_, err := resolveTeamRepoSlug(context.Background(), projectRoot)
	if err == nil {
		t.Fatal("resolveTeamRepoSlug() error = nil, want missing remote.origin.url to fail fast")
	}
}

func teamSync413PushFiles(_ context.Context, req TeamSyncPushRequest) (TeamSyncPushResponse, error) {
	if len(req.Uploads) > 1 {
		return TeamSyncPushResponse{MaxEntries: 1}, nil
	}
	applied := make(map[string]string, len(req.Uploads))
	for path, content := range req.Uploads {
		applied[path] = checksumContent([]byte(content))
	}
	return TeamSyncPushResponse{ETag: "etag-next", Applied: applied}, nil
}

func assertTeamSync413FirstPush(t *testing.T, first TeamSyncPushResult, serverMaxEntries int) {
	t.Helper()
	if !first.LearnedMaxEntries {
		t.Fatal("first Push() did not learn serverMaxEntries")
	}
	if first.Applied {
		t.Fatal("first Push() applied unexpectedly")
	}
	if serverMaxEntries != 1 {
		t.Fatalf("ServerMaxEntries = %d, want 1", serverMaxEntries)
	}
}

func assertTeamSync413SecondPush(t *testing.T, second TeamSyncPushResult, pushBatchSizes []int) {
	t.Helper()
	if !second.Applied {
		t.Fatal("second Push() = not applied, want true")
	}
	if len(pushBatchSizes) < 3 {
		t.Fatalf("pushBatchSizes = %#v, want first oversized call then split calls", pushBatchSizes)
	}
	if pushBatchSizes[0] != 2 {
		t.Fatalf("first push batch size = %d, want 2", pushBatchSizes[0])
	}
	for _, size := range pushBatchSizes[1:] {
		if size > 1 {
			t.Fatalf("split push batch size = %d, want <= 1", size)
		}
	}
}

func writeTeamSyncTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
