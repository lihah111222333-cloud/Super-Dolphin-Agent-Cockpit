package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
)

const teamSyncGitTimeout = 4 * time.Second

// TeamSyncTrigger 标记触发同步的来源，便于日志和 prompt 失效追踪。
type TeamSyncTrigger string

// 团队记忆同步触发来源。
const (
	TeamSyncTriggerInitial  TeamSyncTrigger = "initial_pull"
	TeamSyncTriggerManual   TeamSyncTrigger = "manual"
	TeamSyncTriggerWatcher  TeamSyncTrigger = "watcher"
	TeamSyncTriggerConflict TeamSyncTrigger = "conflict_retry"
)

// TeamSyncPullResult 描述一次远端拉取对本地团队记忆目录造成的影响。
type TeamSyncPullResult struct {
	Applied     bool
	NotModified bool
	NotFound    bool
	Cleared     bool
	Paths       []string
}

// TeamSyncPushResult 描述一次本地推送的应用、重试和文件级失败情况。
type TeamSyncPushResult struct {
	Applied           bool
	Retried           bool
	LearnedMaxEntries bool
	Skipped           []TeamMemSkippedFile
	Failed            map[string]string
}

// teamSyncInvalidator 是同步成功后通知 prompt 缓存失效的最小接口。
type teamSyncInvalidator interface {
	Invalidate(context.Context, contract.InvalidateReason) error
}

// TeamSyncService 管理团队记忆目录与远端仓库之间的拉取、推送和 watcher 生命周期。
// 所有运行态字段由 mu 保护，同一时间只允许一个 watcher 绑定当前 root/repoSlug。
type TeamSyncService struct {
	cfg             Config
	manager         *TeamMemoryManager
	guard           *TeamMemoryGuard
	invalidator     teamSyncInvalidator
	logger          *slog.Logger
	remote          teamSyncRemote
	resolveRepoSlug func(context.Context, string) (string, error)
	newWatcher      func(*TeamSyncService, string, *slog.Logger) (*teamSyncWatcher, error)
	closeWatcher    func(context.Context, *teamSyncWatcher, bool) error
	refreshChecksum func(*TeamSyncService) error

	transitionMu sync.Mutex
	mu           sync.Mutex
	sessions     map[string]contract.BuildCtx
	root         string
	repoSlug     string
	state        SyncState
	stateStore   *teamSyncStateStore
	watcher      *teamSyncWatcher
}

// teamSyncRuntime 是 StartSession 解析出的可运行配置快照。
// 解析阶段先完成路径、repo slug、OAuth 和状态读取，入锁后只做状态切换和同步。
type teamSyncRuntime struct {
	buildCtx contract.BuildCtx
	root     string
	repoSlug string
	state    SyncState
	store    *teamSyncStateStore
}

// NewTeamSyncService 创建团队记忆同步服务。
// cfg 为空时使用禁用配置，remote 和 repo slug 解析器保留为字段方便测试替换。
func NewTeamSyncService(
	cfg Config,
	manager *TeamMemoryManager,
	guard *TeamMemoryGuard,
	invalidator contract.PromptAssemblyService,
	logger *slog.Logger,
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
		newWatcher:      newTeamSyncWatcher,
		closeWatcher: func(ctx context.Context, watcher *teamSyncWatcher, flush bool) error {
			return watcher.Close(ctx, flush)
		},
		refreshChecksum: func(runtime *TeamSyncService) error {
			return runtime.refreshLocalChecksumLocked()
		},
		sessions: map[string]contract.BuildCtx{},
	}
}

// startSessionDisabled 判断 start 请求是否没有可执行的服务或线程 ID。
func (s *TeamSyncService) startSessionDisabled(threadID string) bool {
	return s == nil || threadID == ""
}

// canReuseWatcherLocked 判断当前 watcher 是否已经覆盖同一个 root 和 repo。
// 调用方必须持有 s.mu，复用时只追加 session，避免重复拉取和重建 fsnotify watcher。
func (s *TeamSyncService) canReuseWatcherLocked(runtime teamSyncRuntime) bool {
	return s.watcher != nil && s.root == runtime.root && s.repoSlug == runtime.repoSlug
}

// StartSession 为线程启动团队记忆同步。
// runtime 切换由 transitionMu 串行化；新状态在候选快照中准备完成后一次性提交。
func (s *TeamSyncService) StartSession(ctx context.Context, threadID string, buildCtx contract.BuildCtx) error {
	threadID = strings.TrimSpace(threadID)
	if s.startSessionDisabled(threadID) {
		return nil
	}
	runtime, ok, err := s.resolveRuntime(ctx, buildCtx)
	if err != nil || !ok {
		return err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()

	var oldWatcher *teamSyncWatcher
	s.mu.Lock()
	if s.canReuseWatcherLocked(runtime) {
		s.sessions[threadID] = cloneBuildCtx(buildCtx)
		s.mu.Unlock()
		return nil
	}
	oldWatcher = s.watcher
	oldRuntime := s.snapshotRuntimeLocked()
	s.watcher = nil
	s.mu.Unlock()
	if oldWatcher != nil {
		if closeErr := s.closeWatcher(ctx, oldWatcher, true); closeErr != nil {
			return s.rollbackRuntime(oldRuntime, closeErr)
		}
	}

	candidate := s.newRuntimeCandidate(runtime, oldRuntime.sessions)
	candidate.sessions[threadID] = cloneBuildCtx(buildCtx)
	if _, err := candidate.pullLocked(ctx, TeamSyncTriggerInitial); err != nil {
		return s.rollbackRuntime(oldRuntime, err)
	}
	if err := s.refreshRuntimeChecksum(candidate); err != nil {
		return s.rollbackRuntime(oldRuntime, err)
	}
	watcher, err := s.newWatcher(s, runtime.root, s.logger)
	if err != nil {
		return s.rollbackRuntime(oldRuntime, err)
	}
	s.mu.Lock()
	s.root = candidate.root
	s.repoSlug = candidate.repoSlug
	s.stateStore = candidate.stateStore
	s.state = candidate.state
	s.sessions = candidate.sessions
	s.watcher = watcher
	s.mu.Unlock()
	watcher.Start()
	return nil
}

// refreshRuntimeChecksum 通过服务实例绑定的准备函数刷新候选 runtime checksum。
func (s *TeamSyncService) refreshRuntimeChecksum(candidate *TeamSyncService) error {
	if s.refreshChecksum == nil {
		return errors.New("team sync: checksum refresher is not wired")
	}
	return s.refreshChecksum(candidate)
}

type teamSyncRuntimeSnapshot struct {
	root       string
	repoSlug   string
	state      SyncState
	stateStore *teamSyncStateStore
	sessions   map[string]contract.BuildCtx
	watcher    *teamSyncWatcher
}

func (s *TeamSyncService) snapshotRuntimeLocked() teamSyncRuntimeSnapshot {
	return teamSyncRuntimeSnapshot{
		root: s.root, repoSlug: s.repoSlug, state: cloneSyncState(s.state), stateStore: s.stateStore,
		sessions: cloneTeamSyncSessions(s.sessions), watcher: s.watcher,
	}
}

func cloneTeamSyncSessions(source map[string]contract.BuildCtx) map[string]contract.BuildCtx {
	result := make(map[string]contract.BuildCtx, len(source))
	for threadID, buildCtx := range source {
		result[threadID] = cloneBuildCtx(buildCtx)
	}
	return result
}

func (s *TeamSyncService) newRuntimeCandidate(runtime teamSyncRuntime, sessions map[string]contract.BuildCtx) *TeamSyncService {
	return &TeamSyncService{
		cfg: s.cfg, manager: s.manager, guard: s.guard, invalidator: s.invalidator, logger: s.logger,
		remote: s.remote, resolveRepoSlug: s.resolveRepoSlug, sessions: cloneTeamSyncSessions(sessions),
		root: runtime.root, repoSlug: runtime.repoSlug, state: runtime.state, stateStore: runtime.store,
	}
}

func (s *TeamSyncService) rollbackRuntime(old teamSyncRuntimeSnapshot, cause error) error {
	var rollbackErr error
	var restoredWatcher *teamSyncWatcher
	if old.watcher != nil {
		restoredWatcher, rollbackErr = s.newWatcher(s, old.root, s.logger)
	}
	s.mu.Lock()
	s.root = old.root
	s.repoSlug = old.repoSlug
	s.state = cloneSyncState(old.state)
	s.stateStore = old.stateStore
	s.sessions = cloneTeamSyncSessions(old.sessions)
	s.watcher = restoredWatcher
	s.mu.Unlock()
	if restoredWatcher != nil {
		restoredWatcher.Start()
	}
	if rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback team sync runtime: %w", rollbackErr))
	}
	return cause
}

// StopSession 移除线程绑定；最后一个 session 退出时关闭 watcher 并执行最终推送。
func (s *TeamSyncService) StopSession(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return nil
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	var watcher *teamSyncWatcher
	s.mu.Lock()
	delete(s.sessions, threadID)
	if len(s.sessions) == 0 {
		watcher = s.watcher
		s.watcher = nil
	}
	s.mu.Unlock()
	if watcher != nil {
		return s.closeWatcher(ctx, watcher, true)
	}
	return nil
}

// Shutdown 停止所有团队记忆同步 session，并关闭当前 watcher。
// 该方法用于模块关机路径，必须清空 sessions，避免后续复用已关闭的运行态。
func (s *TeamSyncService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	var watcher *teamSyncWatcher
	s.mu.Lock()
	watcher = s.watcher
	s.watcher = nil
	s.sessions = map[string]contract.BuildCtx{}
	s.mu.Unlock()
	if watcher != nil {
		return s.closeWatcher(ctx, watcher, true)
	}
	return nil
}

// pushLocalChanges 在锁内把当前本地变更推送到远端。
// watcher 和手动入口共用此方法，避免并发推送与本地状态写入交错。
func (s *TeamSyncService) pushLocalChanges(ctx context.Context, trigger TeamSyncTrigger) (TeamSyncPushResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushLocked(ctx, trigger, false)
}

// runtimeUnavailable 判断同步服务缺少 manager 或 remote 等硬依赖。
func (s *TeamSyncService) runtimeUnavailable() bool {
	return s == nil || s.manager == nil || s.remote == nil
}

// teamSyncGateEnabled 判断功能开关是否允许团队记忆同步运行。
func teamSyncGateEnabled(gate GateSnapshot) bool {
	return gate.AutoEnabled && gate.TeamMemEnabled && !gate.KairosActive
}

// projectRootForRuntime 选择用于解析远端仓库 slug 的项目根目录。
// 优先使用 BuildCtx.GitRoot，其次配置提供的项目根，最后退回团队记忆根目录的父目录。
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

// resolveRuntime 解析一次 session 启动所需的完整运行态。
// 未满足开关、路径或 OAuth 条件时返回 ok=false；配置或状态读取失败时返回 error 阻断启动。
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

// runtimeReadyLocked 检查锁内运行态是否足以执行 pull/push。
func (s *TeamSyncService) runtimeReadyLocked() bool {
	return s != nil && s.remote != nil && s.stateStore != nil && strings.TrimSpace(s.root) != "" && strings.TrimSpace(s.repoSlug) != ""
}

// refreshLocalChecksumLocked 重新扫描本地团队记忆文件并持久化 checksum。
// 调用方必须持有 s.mu，确保 state 与磁盘写入顺序一致。
func (s *TeamSyncService) refreshLocalChecksumLocked() error {
	local, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return err
	}
	s.state.LastKnownChecksum = checksumTree(localChecksumMap(local))
	return s.persistStateLocked()
}

// persistStateLocked 标准化后保存同步状态。
// 调用方必须持有 s.mu；stateStore 为空表示同步尚未完成运行态初始化。
func (s *TeamSyncService) persistStateLocked() error {
	if s == nil || s.stateStore == nil {
		return nil
	}
	s.state = normalizeSyncState(s.state)
	return s.stateStore.Save(s.state)
}

// cloneBuildCtx 深拷贝 BuildCtx 中会被 session 保存的切片和 map。
func cloneBuildCtx(buildCtx contract.BuildCtx) contract.BuildCtx {
	cloned := buildCtx
	cloned.EnabledTools = append([]string(nil), buildCtx.EnabledTools...)
	cloned.AdditionalWorkingDirectories = append([]string(nil), buildCtx.AdditionalWorkingDirectories...)
	cloned.ClaudeMdExcludes = append([]string(nil), buildCtx.ClaudeMdExcludes...)
	if len(buildCtx.SessionFlags) > 0 {
		cloned.SessionFlags = make(map[string]bool, len(buildCtx.SessionFlags))
		maps.Copy(cloned.SessionFlags, buildCtx.SessionFlags)
	}
	return cloned
}

// resolveTeamRepoSlug 通过 git remote.origin.url 解析 owner/repo。
// 命令受 teamSyncGitTimeout 限制，避免 StartSession 卡在 git 子进程上。
func resolveTeamRepoSlug(ctx context.Context, projectRoot string) (string, error) {
	projectRoot, err := shared.CleanAbsolutePath(projectRoot)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	gitCtx, cancel := ctxutil.WithTimeout(ctx, teamSyncGitTimeout)
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

// parseTeamRepoSlug 从 HTTPS、SSH 和 scp-like git remote URL 中提取 owner/repo。
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

// execLookalikeParse 解析带 scheme 的 git remote URL，并返回去掉首尾斜杠的 path。
func execLookalikeParse(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return strings.Trim(parsed.Path, "/"), nil
}
