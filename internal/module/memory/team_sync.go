package memory

import (
	"context"
	"log/slog"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

const teamSyncGitTimeout = 4 * time.Second

type TeamSyncTrigger string

const (
	TeamSyncTriggerInitial  TeamSyncTrigger = "initial_pull"
	TeamSyncTriggerManual   TeamSyncTrigger = "manual"
	TeamSyncTriggerWatcher  TeamSyncTrigger = "watcher"
	TeamSyncTriggerConflict TeamSyncTrigger = "conflict_retry"
)

type TeamSyncPullResult struct {
	Applied     bool
	NotModified bool
	NotFound    bool
	Cleared     bool
	Paths       []string
}

type TeamSyncPushResult struct {
	Applied           bool
	Retried           bool
	LearnedMaxEntries bool
	Skipped           []TeamMemSkippedFile
	Failed            map[string]string
}

type teamSyncInvalidator interface {
	Invalidate(context.Context, promptpkg.InvalidateReason) error
}

type TeamSyncService struct {
	cfg             *Config
	manager         *TeamMemoryManager
	guard           *TeamMemoryGuard
	invalidator     teamSyncInvalidator
	logger          *slog.Logger
	remote          teamSyncRemote
	resolveRepoSlug func(context.Context, string) (string, error)

	mu         sync.Mutex
	sessions   map[string]contract.BuildCtx
	root       string
	repoSlug   string
	state      SyncState
	stateStore *teamSyncStateStore
	watcher    *teamSyncWatcher
}

type teamSyncRuntime struct {
	buildCtx contract.BuildCtx
	root     string
	repoSlug string
	state    SyncState
	store    *teamSyncStateStore
}

func NewTeamSyncService(
	cfg *Config,
	manager *TeamMemoryManager,
	guard *TeamMemoryGuard,
	invalidator contract.PromptAssemblyService,
	logger *slog.Logger,
) *TeamSyncService {
	return &TeamSyncService{
		cfg:             memoryConfig(cfg),
		manager:         manager,
		guard:           guard,
		invalidator:     invalidator,
		logger:          logger,
		remote:          newTeamSyncRemoteFromEnv(),
		resolveRepoSlug: resolveTeamRepoSlug,
		sessions:        map[string]contract.BuildCtx{},
	}
}

func (s *TeamSyncService) StartSession(ctx context.Context, threadID string, buildCtx contract.BuildCtx) error {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return nil
	}
	runtime, ok, err := s.resolveRuntime(ctx, buildCtx)
	if err != nil || !ok {
		return err
	}
	s.mu.Lock()
	if s.reuseWatcherSessionLocked(threadID, buildCtx, runtime) {
		s.mu.Unlock()
		return nil
	}
	oldWatcher := s.detachWatcherLocked()
	s.mu.Unlock()
	if oldWatcher != nil {
		_ = oldWatcher.Close(ctx, true)
	}
	watcher, err := s.startSessionWatcher(ctx, threadID, buildCtx, runtime)
	if err != nil {
		return err
	}
	watcher.Start()
	return nil
}

func (s *TeamSyncService) reuseWatcherSessionLocked(threadID string, buildCtx contract.BuildCtx, runtime teamSyncRuntime) bool {
	if s.watcher == nil || s.root != runtime.root || s.repoSlug != runtime.repoSlug {
		return false
	}
	s.sessions[threadID] = cloneBuildCtx(buildCtx)
	return true
}

func (s *TeamSyncService) detachWatcherLocked() *teamSyncWatcher {
	oldWatcher := s.watcher
	s.watcher = nil
	return oldWatcher
}

func (s *TeamSyncService) startSessionWatcher(
	ctx context.Context,
	threadID string,
	buildCtx contract.BuildCtx,
	runtime teamSyncRuntime,
) (*teamSyncWatcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyRuntimeLocked(runtime)
	s.sessions[threadID] = cloneBuildCtx(buildCtx)
	watcher, err := s.initializeSessionWatcherLocked(ctx, runtime.root)
	if err != nil {
		delete(s.sessions, threadID)
		return nil, err
	}
	s.watcher = watcher
	return watcher, nil
}

func (s *TeamSyncService) applyRuntimeLocked(runtime teamSyncRuntime) {
	s.root = runtime.root
	s.repoSlug = runtime.repoSlug
	s.stateStore = runtime.store
	s.state = runtime.state
}

func (s *TeamSyncService) initializeSessionWatcherLocked(ctx context.Context, root string) (*teamSyncWatcher, error) {
	if _, err := s.pullLocked(ctx, TeamSyncTriggerInitial); err != nil {
		return nil, err
	}
	if err := s.refreshLocalChecksumLocked(); err != nil {
		return nil, err
	}
	watcher, err := newTeamSyncWatcher(s, root, s.logger)
	if err != nil {
		return nil, err
	}
	return watcher, nil
}

func (s *TeamSyncService) StopSession(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return nil
	}
	var watcher *teamSyncWatcher
	s.mu.Lock()
	delete(s.sessions, threadID)
	if len(s.sessions) == 0 {
		watcher = s.watcher
		s.watcher = nil
	}
	s.mu.Unlock()
	if watcher != nil {
		return watcher.Close(ctx, true)
	}
	return nil
}

func (s *TeamSyncService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var watcher *teamSyncWatcher
	s.mu.Lock()
	watcher = s.watcher
	s.watcher = nil
	s.sessions = map[string]contract.BuildCtx{}
	s.mu.Unlock()
	if watcher != nil {
		return watcher.Close(ctx, true)
	}
	return nil
}

func (s *TeamSyncService) Pull(ctx context.Context) (TeamSyncPullResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pullLocked(ctx, TeamSyncTriggerManual)
}

func (s *TeamSyncService) Push(ctx context.Context) (TeamSyncPushResult, error) {
	return s.pushLocalChanges(ctx, TeamSyncTriggerManual)
}

func (s *TeamSyncService) pushLocalChanges(ctx context.Context, trigger TeamSyncTrigger) (TeamSyncPushResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushLocked(ctx, trigger, false)
}

func (s *TeamSyncService) resolveRuntime(ctx context.Context, buildCtx contract.BuildCtx) (teamSyncRuntime, bool, error) {
	if !s.runtimeDependenciesReady() || !teamSyncGateOpen(ResolveMemoryGate(buildCtx, s.cfg)) {
		return teamSyncRuntime{}, false, nil
	}
	root := strings.TrimSpace(s.manager.GetTeamMemPath(buildCtx))
	if root == "" {
		return teamSyncRuntime{}, false, nil
	}
	store, state, err := loadTeamSyncRuntimeState(root)
	if err != nil {
		return teamSyncRuntime{}, false, err
	}
	repoSlug, ok, err := s.resolveRuntimeRepoSlug(ctx, buildCtx, root)
	if err != nil || !ok {
		return teamSyncRuntime{}, false, err
	}
	if !s.remote.OAuthReady(ctx) {
		return teamSyncRuntime{}, false, nil
	}
	return teamSyncRuntime{buildCtx: cloneBuildCtx(buildCtx), root: root, repoSlug: repoSlug, state: state, store: store}, true, nil
}

func (s *TeamSyncService) runtimeDependenciesReady() bool {
	return s != nil && s.manager != nil && s.remote != nil
}

func teamSyncGateOpen(gate MemoryGateSnapshot) bool {
	return gate.AutoEnabled && gate.TeamMemEnabled && !gate.KairosActive
}

func loadTeamSyncRuntimeState(root string) (*teamSyncStateStore, SyncState, error) {
	store, err := newTeamSyncStateStore(root)
	if err != nil {
		return nil, SyncState{}, err
	}
	state, err := store.Load()
	if err != nil {
		return nil, SyncState{}, err
	}
	return store, state, nil
}

func (s *TeamSyncService) resolveRuntimeRepoSlug(ctx context.Context, buildCtx contract.BuildCtx, root string) (string, bool, error) {
	repoSlug, err := s.resolveRepoSlug(ctx, resolveRuntimeProjectRoot(buildCtx, s.cfg, root))
	if err != nil {
		return "", false, err
	}
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return "", false, nil
	}
	return repoSlug, true, nil
}

func resolveRuntimeProjectRoot(buildCtx contract.BuildCtx, cfg *Config, root string) string {
	projectRoot := strings.TrimSpace(buildCtx.GitRoot)
	if projectRoot != "" {
		return projectRoot
	}
	if cfg != nil {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
		if projectRoot != "" {
			return projectRoot
		}
	}
	return filepath.Dir(root)
}

func (s *TeamSyncService) runtimeReadyLocked() bool {
	return s != nil && s.remote != nil && s.stateStore != nil && strings.TrimSpace(s.root) != "" && strings.TrimSpace(s.repoSlug) != ""
}

func (s *TeamSyncService) refreshLocalChecksumLocked() error {
	local, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return err
	}
	s.state.LastKnownChecksum = checksumTree(localChecksumMap(local))
	return s.persistStateLocked()
}

func (s *TeamSyncService) persistStateLocked() error {
	if s == nil || s.stateStore == nil {
		return nil
	}
	s.state = normalizeSyncState(s.state)
	return s.stateStore.Save(s.state)
}

func cloneBuildCtx(buildCtx contract.BuildCtx) contract.BuildCtx {
	cloned := buildCtx
	cloned.EnabledTools = append([]string(nil), buildCtx.EnabledTools...)
	cloned.AdditionalWorkingDirectories = append([]string(nil), buildCtx.AdditionalWorkingDirectories...)
	cloned.ClaudeMdExcludes = append([]string(nil), buildCtx.ClaudeMdExcludes...)
	if len(buildCtx.SessionFlags) > 0 {
		cloned.SessionFlags = make(map[string]bool, len(buildCtx.SessionFlags))
		for key, value := range buildCtx.SessionFlags {
			cloned.SessionFlags[key] = value
		}
	}
	return cloned
}

func resolveTeamRepoSlug(ctx context.Context, projectRoot string) (string, error) {
	projectRoot, err := cleanAbsolutePath(projectRoot)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	gitCtx, cancel := context.WithTimeout(ctx, teamSyncGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir = projectRoot
	output, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return parseTeamRepoSlug(string(output)), nil
}

func parseTeamRepoSlug(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	trimmed := strings.TrimSuffix(strings.TrimRight(raw, "/"), ".git")
	if strings.Contains(trimmed, "://") {
		if parsed, err := execLookalikeParse(trimmed); err == nil {
			trimmed = parsed
		}
	} else if index := strings.Index(trimmed, ":"); index > 0 && strings.Contains(trimmed[:index], "@") {
		trimmed = strings.Trim(trimmed[index+1:], "/")
	}
	parts := strings.Split(filepath.ToSlash(trimmed), "/")
	if len(parts) < 2 {
		return ""
	}
	owner := strings.TrimSpace(parts[len(parts)-2])
	repo := strings.TrimSpace(parts[len(parts)-1])
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

func execLookalikeParse(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return strings.Trim(parsed.Path, "/"), nil
}
