package team

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
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
	Invalidate(context.Context, contract.InvalidateReason) error
}

type TeamSyncService struct {
	cfg             Config
	manager         *TeamMemoryManager
	guard           *TeamMemoryGuard
	invalidator     teamSyncInvalidator
	logger          *pkglogger.Logger
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

// NewTeamSyncService 创建teamsync服务。
func NewTeamSyncService(
	cfg Config,
	manager *TeamMemoryManager,
	guard *TeamMemoryGuard,
	invalidator contract.PromptAssemblyService,
	logger *pkglogger.Logger,
) *TeamSyncService {
	if cfg == nil {
		cfg = disabledConfig{}
	}
	return &TeamSyncService{
		cfg:             cfg,
		manager:         manager,
		guard:           guard,
		invalidator:     invalidator,
		logger:          logger,
		remote:          newTeamSyncRemoteFromEnv(),
		resolveRepoSlug: resolveTeamRepoSlug,
		sessions:        map[string]contract.BuildCtx{},
	}
}

func (s *TeamSyncService) startSessionDisabled(threadID string) bool {
	return s == nil || threadID == ""
}

func (s *TeamSyncService) canReuseWatcherLocked(runtime teamSyncRuntime) bool {
	return s.watcher != nil && s.root == runtime.root && s.repoSlug == runtime.repoSlug
}

// StartSession 启动会话。
func (s *TeamSyncService) StartSession(ctx context.Context, threadID string, buildCtx contract.BuildCtx) error {
	threadID = strings.TrimSpace(threadID)
	if s.startSessionDisabled(threadID) {
		return nil
	}
	runtime, ok, err := s.resolveRuntime(ctx, buildCtx)
	if err != nil || !ok {
		return err
	}
	var oldWatcher *teamSyncWatcher
	s.mu.Lock()
	if s.canReuseWatcherLocked(runtime) {
		s.sessions[threadID] = cloneBuildCtx(buildCtx)
		s.mu.Unlock()
		return nil
	}
	oldWatcher = s.watcher
	s.watcher = nil
	s.mu.Unlock()
	if oldWatcher != nil {
		_ = oldWatcher.Close(ctx, true)
	}

	s.mu.Lock()
	s.root = runtime.root
	s.repoSlug = runtime.repoSlug
	s.stateStore = runtime.store
	s.state = runtime.state
	s.sessions[threadID] = cloneBuildCtx(buildCtx)
	if _, err := s.pullLocked(ctx, TeamSyncTriggerInitial); err != nil {
		delete(s.sessions, threadID)
		s.mu.Unlock()
		return err
	}
	if err := s.refreshLocalChecksumLocked(); err != nil {
		delete(s.sessions, threadID)
		s.mu.Unlock()
		return err
	}
	watcher, err := newTeamSyncWatcher(s, runtime.root, s.logger)
	if err != nil {
		delete(s.sessions, threadID)
		s.mu.Unlock()
		return err
	}
	s.watcher = watcher
	s.mu.Unlock()
	watcher.Start()
	return nil
}

// StopSession 停止会话。
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

// Shutdown 发送 LSP 关闭请求。
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

func (s *TeamSyncService) pushLocalChanges(ctx context.Context, trigger TeamSyncTrigger) (TeamSyncPushResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushLocked(ctx, trigger, false)
}

func (s *TeamSyncService) runtimeUnavailable() bool {
	return s == nil || s.manager == nil || s.remote == nil
}

func teamSyncGateEnabled(gate GateSnapshot) bool {
	return gate.AutoEnabled && gate.TeamMemEnabled && !gate.KairosActive
}

func (s *TeamSyncService) projectRootForRuntime(buildCtx contract.BuildCtx, root string) string {
	projectRoot := strings.TrimSpace(buildCtx.GitRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(s.cfg.ProjectRoot(buildCtx))
	}
	if projectRoot == "" {
		projectRoot = filepath.Dir(root)
	}
	return projectRoot
}

// resolveRuntime 解析运行时。
func (s *TeamSyncService) resolveRuntime(ctx context.Context, buildCtx contract.BuildCtx) (teamSyncRuntime, bool, error) {
	if s.runtimeUnavailable() {
		return teamSyncRuntime{}, false, nil
	}
	gate := s.cfg.Gate(buildCtx)
	if !teamSyncGateEnabled(gate) {
		return teamSyncRuntime{}, false, nil
	}
	root := strings.TrimSpace(s.manager.GetTeamMemPath(buildCtx))
	if root == "" {
		return teamSyncRuntime{}, false, nil
	}
	store, err := newTeamSyncStateStore(root)
	if err != nil {
		return teamSyncRuntime{}, false, err
	}
	state, err := store.Load()
	if err != nil {
		return teamSyncRuntime{}, false, err
	}
	projectRoot := s.projectRootForRuntime(buildCtx, root)
	repoSlug, err := s.resolveRepoSlug(ctx, projectRoot)
	if err != nil || strings.TrimSpace(repoSlug) == "" {
		return teamSyncRuntime{}, false, err
	}
	if !s.remote.OAuthReady(ctx) {
		return teamSyncRuntime{}, false, nil
	}
	return teamSyncRuntime{buildCtx: cloneBuildCtx(buildCtx), root: root, repoSlug: repoSlug, state: state, store: store}, true, nil
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
	projectRoot, err := shared.CleanAbsolutePath(projectRoot)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	gitCtx, cancel := kernel.WithTimeout(ctx, teamSyncGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir = projectRoot
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("team sync resolve repo slug: read git remote.origin.url: %w", err)
	}
	repoSlug := parseTeamRepoSlug(string(output))
	if strings.TrimSpace(repoSlug) == "" {
		return "", fmt.Errorf("team sync resolve repo slug: git remote.origin.url is empty or unsupported")
	}
	return repoSlug, nil
}

// parseTeamRepoSlug 解析team仓库slug。
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
