package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/util/toolresults"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

var (
	ErrMemoryAlreadyExists     = errors.New("memory already exists")
	ErrMemoryLockTimeout       = errors.New("memory store lock timeout")
	ErrMemoryNotFound          = errors.New("memory not found")
	ErrInvalidMemoryEntry      = errors.New("invalid memory entry")
	ErrMemoryIndexUpdateFailed = errors.New("memory_index_update_failed")
)

// WriteOptions 控制单次 memory 写入的维护行为。
// SkipIndex 用于批量整理或测试场景，避免每次文件写入都立即刷新 MEMORY.md。
type WriteOptions struct {
	SkipIndex bool
}

// MemoryWriteRequest 是内部结构化记忆写入请求。
// 它在落盘前承载名称、类型、正文和来源，最终会被序列化为 topic markdown。
type MemoryWriteRequest struct {
	Name        string
	Description string
	Type        MemoryType
	Body        string
	Title       string
	Source      string
}

type memoryWriteGuard interface {
	ValidateWrite(path, content string) (string, error)
}

type memoryStructuredStore interface {
	Read(name string) (MemoryEntry, error)
	CreateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	UpdateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	// UpsertStructured 在单次磁盘锁持有期间完成创建或更新。
	// 这里不能退回 Create 失败后再 Update 的两阶段写法，否则并发写入会在两次锁之间丢更新。
	UpsertStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	Delete(name string, opts ...WriteOptions) error
}

type memoryWriteStore interface {
	memoryStructuredStore
	Root() string
	Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error)
	Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error)
}

var _ contract.AgentMemoryReader = (*MemoryLifecycleHooks)(nil)

// MemoryReadEnabled 返回 AgentMemoryReader 是否允许读取记忆。
// 该开关只表示功能启用，具体 scope/path 权限仍在 ReadAgentMemory 内校验。
func (h *MemoryLifecycleHooks) MemoryReadEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.Enabled
}

// MemoryReadToolsEnabled 返回面向工具桥的记忆读取能力是否打开。
// Enabled 和 EnableTools 分开判断，避免服务端可用但工具入口未授权时误暴露读取接口。
func (h *MemoryLifecycleHooks) MemoryReadToolsEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.EnableTools
}

// ReadAgentMemory 按名称或路径读取单条代理记忆。
// 它会先解析 scope、类型和根目录，再校验路径边界；类型不匹配按 not_found 返回，
// 避免泄漏其它类型记忆的存在。
func (h *MemoryLifecycleHooks) ReadAgentMemory(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	root, prepared, err := h.prepareAgentMemoryRead(ctx, req)
	if err != nil {
		return contract.MemoryReadResult{}, err
	}
	entry, indexHit, err := readAgentMemoryEntry(root, prepared)
	if err != nil {
		return contract.MemoryReadResult{}, err
	}
	if prepared.Type.IsKnown() && entry.Type() != MemoryType(prepared.Type) {
		return contract.MemoryReadResult{}, agentMemoryError("not_found", ErrMemoryNotFound)
	}
	return agentMemoryReadResult(root, entry, indexHit), nil
}

// prepareAgentMemoryRead 标准化读取请求并解析目标根目录。
// feature/tool gate、输入字段和 scope 都在这里 fail-fast，后续读取函数只处理已准备好的请求。
func (h *MemoryLifecycleHooks) prepareAgentMemoryRead(ctx context.Context, req contract.MemoryReadRequest) (string, contract.MemoryReadRequest, error) {
	if h == nil || h.cfg == nil {
		return "", contract.MemoryReadRequest{}, agentMemoryError("reader_unavailable", fmt.Errorf("memory reader is not configured"))
	}
	if !h.cfg.Enabled {
		return "", contract.MemoryReadRequest{}, agentMemoryError("feature_disabled", contract.ErrFeatureDisabled)
	}
	if !h.cfg.EnableTools {
		return "", contract.MemoryReadRequest{}, agentMemoryError("tools_disabled", contract.ErrFeatureDisabled)
	}
	prepared := req
	prepared.Name = strings.TrimSpace(req.Name)
	prepared.Path = strings.TrimSpace(req.Path)
	prepared.Scope = parseAgentMemoryReadScope(req.Scope)
	prepared.Type = contract.ParseMemoryType(string(req.Type))
	if prepared.Name == "" && prepared.Path == "" {
		return "", prepared, agentMemoryError("invalid_input", fmt.Errorf("name or path is required"))
	}
	root, err := h.agentMemoryReadRoot(ctx, prepared)
	if err != nil {
		return "", prepared, err
	}
	return root, prepared, nil
}

// resolvedAgentMemoryRoot 解析 agent memory 读取使用的私有根目录。
// override 优先；已配置为项目级目录时直接使用；否则尝试把 CWD 规整到 canonical git root。
func resolvedAgentMemoryRoot(ctx context.Context, cfg *Config, projectRoot string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("memory config is not configured")
	}
	if override := strings.TrimSpace(cfg.AutoMemPathOverride); override != "" {
		return resolvedStoreRoot(cfg.RootDir, projectRoot, override)
	}
	if root := strings.TrimSpace(cfg.RootDir); root != "" {
		if filepath.Base(root) == memoryProjectDirName {
			return strings.TrimSuffix(root, string(os.PathSeparator)), nil
		}
	}
	if projectRoot != "" {
		if canonical, err := FindCanonicalGitRoot(ctx, projectRoot); err == nil && strings.TrimSpace(canonical) != "" {
			projectRoot = canonical
		}
	}
	return resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
}

// parseAgentMemoryReadScope 解析读取 scope，空值按用户私有记忆处理。
// 其它未知值保留给后续 agentMemoryReadRoot 返回 unsupported_scope。
func parseAgentMemoryReadScope(scope contract.MemoryScope) contract.MemoryScope {
	if strings.TrimSpace(string(scope)) == "" {
		return contract.MemoryScopeUser
	}
	return contract.ParseMemoryScope(string(scope))
}

// agentMemoryReadRoot 根据 scope 选择私有或团队记忆根目录。
// 团队记忆未配置时请求 team 会返回 unsupported_scope，不能退回 private。
func (h *MemoryLifecycleHooks) agentMemoryReadRoot(ctx context.Context, req contract.MemoryReadRequest) (string, error) {
	switch req.Scope {
	case contract.MemoryScopeUser:
		projectRoot := agentMemoryReadProjectRoot(req, h.cfg)
		root, err := resolvedAgentMemoryRoot(ctx, h.cfg, projectRoot)
		if err != nil {
			return "", agentMemoryError("read_failed", err)
		}
		return root, nil
	case contract.MemoryScopeTeam:
		if !teamMemoryConfigured(*h.cfg) {
			return "", agentMemoryError("unsupported_scope", fmt.Errorf("team memory is not enabled"))
		}
		projectRoot := agentMemoryReadProjectRoot(req, h.cfg)
		root, err := configuredTeamMemRoot(h.cfg, contract.BuildCtx{CWD: projectRoot})
		if err != nil {
			return "", agentMemoryError("read_failed", err)
		}
		return root, nil
	default:
		return "", agentMemoryError("unsupported_scope", fmt.Errorf("unsupported memory scope %q", req.Scope))
	}
}

// agentMemoryReadProjectRoot 选择读取请求关联的项目根。
// 请求 CWD 优先，其次使用配置 ProjectRoot；最后才从 RootDir 反推，供旧配置兼容。
func agentMemoryReadProjectRoot(req contract.MemoryReadRequest, cfg *Config) string {
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		return cwd
	}
	if cfg == nil {
		return ""
	}
	projectRoot := strings.TrimSpace(cfg.ProjectRoot)
	if projectRoot != "" {
		return projectRoot
	}
	return filepath.Dir(filepath.Dir(strings.TrimSuffix(strings.TrimSpace(cfg.RootDir), string(os.PathSeparator))))
}

// readAgentMemoryEntry 按路径或名称读取记忆条目。
// 路径读取必须通过 ValidateMemoryReadPath；名称读取走索引查找，避免绕过根目录边界。
func readAgentMemoryEntry(root string, req contract.MemoryReadRequest) (MemoryEntry, bool, error) {
	if strings.TrimSpace(req.Path) != "" {
		path, err := ValidateMemoryReadPath(root, req.Path)
		if err != nil {
			return MemoryEntry{}, false, agentMemoryError("invalid_path", err)
		}
		entry, err := readMemoryEntryFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return MemoryEntry{}, false, agentMemoryError("not_found", ErrMemoryNotFound)
			}
			return MemoryEntry{}, false, agentMemoryError("read_failed", err)
		}
		indexHit, err := agentMemoryIndexHit(root, entry)
		if err != nil {
			return MemoryEntry{}, false, agentMemoryError("index_read_failed", err)
		}
		return entry, indexHit, nil
	}
	name, err := canonicalLookupName(req.Name)
	if err != nil {
		return MemoryEntry{}, false, agentMemoryError("invalid_input", err)
	}
	entry, exists, err := findMemoryEntry(root, name)
	if err != nil {
		return MemoryEntry{}, false, agentMemoryError("read_failed", err)
	}
	if !exists {
		return MemoryEntry{}, false, agentMemoryError("not_found", ErrMemoryNotFound)
	}
	indexHit, err := agentMemoryIndexHit(root, entry)
	if err != nil {
		return MemoryEntry{}, false, agentMemoryError("index_read_failed", err)
	}
	return entry, indexHit, nil
}

// agentMemoryIndexHit 判断读取到的条目是否仍在 MEMORY.md 索引中。
// 索引不可读时必须返回错误，避免把索引损坏静默降级为未命中。
func agentMemoryIndexHit(root string, entry MemoryEntry) (bool, error) {
	entries, err := ReadMemoryIndex(memoryIndexPath(root))
	if err != nil {
		return false, err
	}
	rel, _ := filepath.Rel(root, entry.FilePath)
	rel = filepath.ToSlash(rel)
	for _, item := range entries {
		if item.CanonicalName == entry.CanonicalName || item.Path == rel {
			return true, nil
		}
	}
	return false, nil
}

// relativeAgentMemoryReadPath 返回面向工具结果的相对路径。
// 常规路径失败时会解析真实路径再重试，以兼容符号链接但仍保持在根目录内。
func relativeAgentMemoryReadPath(root, path string) string {
	if rel, ok := safeRelativeMemoryPath(root, path); ok {
		return rel
	}
	rootReal, rootErr := resolveExistingMemoryPath(root)
	pathReal, pathErr := resolveExistingMemoryPath(path)
	if rootErr == nil && pathErr == nil {
		if rel, ok := safeRelativeMemoryPath(rootReal, pathReal); ok {
			return rel
		}
	}
	return ""
}

// safeRelativeMemoryPath 校验 path 是否在 root 内并返回 slash 风格相对路径。
// 任何向上逃逸或 filepath.Rel 错误都会返回 false。
func safeRelativeMemoryPath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// agentMemoryReadResult 将内部 MemoryEntry 转为契约层读取结果。
// 只输出相对 SourcePath，避免把本机绝对路径泄漏给外部工具。
func agentMemoryReadResult(root string, entry MemoryEntry, indexHit bool) contract.MemoryReadResult {
	sourcePath := relativeAgentMemoryReadPath(root, entry.FilePath)
	result := contract.MemoryEntry{
		Name:        entry.Frontmatter.Name,
		Description: entry.Frontmatter.Description,
		Type:        contract.MemoryType(entry.Type()),
		Content:     entry.Content,
		SourcePath:  sourcePath,
		UpdatedAt:   entry.UpdatedAt,
	}
	return contract.MemoryReadResult{Entry: &result, SourcePath: sourcePath, IndexHit: indexHit}
}

// NewMemorySubscribers 声明记忆模块的生命周期事件订阅。
// 注册时会启动 memoryHookWorker，使 turn 输入和完成事件只在总线回调里入队；
// 取消订阅时同步停止 worker，避免关闭后继续写盘。
func NewMemorySubscribers(scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, p memorySubscriberParams) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: contract.SubscriberSpec{
			EventType:     "memory.lifecycle",
			HandlerSymbol: "memory.registerLifecycleSubscriptions",
			OwnerModule:   "memory",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "memory-lifecycle-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				deps := memorySubscriptionDeps{
					Dispatcher:      dispatcher,
					Hooks:           p.Hooks,
					ContextProvider: p.ContextProvider,
					NestedRuntime:   p.NestedRuntime,
					ThreadStore:     p.ThreadStore,
					TeamSync:        p.TeamSync,
				}
				// 创建并启动 memoryHookWorker，让 turn 输入/完成回调只入队；
				// 记忆写删和抽取启动都在 worker goroutine 上执行。
				hookWorker := newMemoryHookWorker(p.Hooks, pkglogger.Get())
				hookWorker.Start()
				var cancels []context.CancelFunc
				appendCancel := func(cancel context.CancelFunc) {
					if cancel != nil {
						cancels = append(cancels, cancel)
					}
				}
				registerLifecycleSubscriptions(deps, scheduler, nested, teamSync, hookWorker, appendCancel)
				var once sync.Once
				return func() {
					once.Do(func() {
						cancelSubscriptions(cancels)
						_ = hookWorker.Stop(context.Background())
					})
				}
			},
		},
	}
}

// newAutoDreamSchedulerProvider 构造自动整理调度器。
// logger 在 provider 层统一注入，避免下游组件自行创建不同日志入口。
func newAutoDreamSchedulerProvider(p autoDreamSchedulerProviderParams) *autoDreamScheduler {
	return newAutoDreamScheduler(p.Hooks, pkglogger.Get())
}

// newNestedIngestWorkerProvider 构造 nested ingest worker 并配置工具输出缓存根。
// 缓存根为空时 NestedRuntime 会按空根契约拒绝 persistedPath 读取，保持 fail-closed。
func newNestedIngestWorkerProvider(p nestedIngestWorkerProviderParams) *nestedIngestWorker {
	if p.NestedRuntime != nil {
		// 主机无法提供 UserCacheDir/TempDir 时传入空根，NestedRuntime 会拒绝
		// persistedPath 读取，避免从未知位置读取工具输出。
		p.NestedRuntime.SetToolReadCacheRoot(toolresults.CacheDir())
	}
	return newNestedIngestWorker(p.NestedRuntime, pkglogger.Get())
}

// newTeamSyncCoordinatorProvider 构造团队记忆同步协调器。
// 真正的远端读写由 coordinator worker 串行执行，不在 provider 中启动。
func newTeamSyncCoordinatorProvider(p teamSyncCoordinatorProviderParams) *teamSyncCoordinator {
	return newTeamSyncCoordinator(p.TeamSync, p.ThreadStore, pkglogger.Get())
}

// autoDreamSchedulerAsRunner 暴露自动整理调度器给 RunnerModule 生命周期管理。
func autoDreamSchedulerAsRunner(s *autoDreamScheduler) contract.Runner {
	return contract.AsRunner(s)
}

// nestedIngestWorkerAsRunner 暴露 nested ingest worker 给 RunnerModule 生命周期管理。
func nestedIngestWorkerAsRunner(w *nestedIngestWorker) contract.Runner {
	return contract.AsRunner(w)
}

// teamSyncCoordinatorAsRunner 暴露团队同步协调器给 RunnerModule 生命周期管理。
func teamSyncCoordinatorAsRunner(c *teamSyncCoordinator) contract.Runner {
	return contract.AsRunner(c)
}
