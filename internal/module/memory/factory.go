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

type WriteOptions struct {
	SkipIndex bool
}

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
	// UpsertStructured writes the entry atomically inside a single disk
	// store lock acquisition (Phase 自有.1a). Replaces the legacy
	// Create-then-Update pattern in upsertStructuredMemory which had a
	// two-phase locking window where another writer could race in between
	// the failed Create and the follow-up Update, producing a lost update.
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

// MemoryReadEnabled 处理记忆readenabled。
func (h *MemoryLifecycleHooks) MemoryReadEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.Enabled
}

// MemoryReadToolsEnabled 处理记忆read工具enabled。
func (h *MemoryLifecycleHooks) MemoryReadToolsEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.EnableTools
}

// ReadAgentMemory 读取代理记忆。
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

// prepareAgentMemoryRead 准备代理记忆read。
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

// resolvedAgentMemoryRoot 处理已解析代理记忆根目录。
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

func parseAgentMemoryReadScope(scope contract.MemoryScope) contract.MemoryScope {
	if strings.TrimSpace(string(scope)) == "" {
		return contract.MemoryScopeUser
	}
	return contract.ParseMemoryScope(string(scope))
}

// agentMemoryReadRoot 处理代理记忆read根目录。
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

// readAgentMemoryEntry 读取代理记忆条目。
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
		return entry, agentMemoryIndexHit(root, entry), nil
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
	return entry, agentMemoryIndexHit(root, entry), nil
}

func agentMemoryIndexHit(root string, entry MemoryEntry) bool {
	entries, err := ReadMemoryIndex(memoryIndexPath(root))
	if err != nil {
		return false
	}
	rel, _ := filepath.Rel(root, entry.FilePath)
	rel = filepath.ToSlash(rel)
	for _, item := range entries {
		if item.CanonicalName == entry.CanonicalName || item.Path == rel {
			return true
		}
	}
	return false
}

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

func safeRelativeMemoryPath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

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

// NewMemorySubscribers declares memory lifecycle bus subscriptions for BusModule.
// NewMemorySubscribers 创建记忆subscribers。
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
				// Create and start the memoryHookWorker so the
				// onTurnInputReceived / onTurnCompleted bus callbacks
				// only enqueue; disk I/O runs on the worker goroutine.
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

func newAutoDreamSchedulerProvider(p autoDreamSchedulerProviderParams) *autoDreamScheduler {
	return newAutoDreamScheduler(p.Hooks, pkglogger.Get())
}

func newNestedIngestWorkerProvider(p nestedIngestWorkerProviderParams) *nestedIngestWorker {
	if p.NestedRuntime != nil {
		// Empty cache root (host without UserCacheDir nor TempDir) disables
		// persistedPath reads via NestedRuntime.SetToolReadCacheRoot's
		// empty-root contract — fail-closed.
		p.NestedRuntime.SetToolReadCacheRoot(toolresults.CacheDir())
	}
	return newNestedIngestWorker(p.NestedRuntime, pkglogger.Get())
}

func newTeamSyncCoordinatorProvider(p teamSyncCoordinatorProviderParams) *teamSyncCoordinator {
	return newTeamSyncCoordinator(p.TeamSync, p.ThreadStore, pkglogger.Get())
}

func autoDreamSchedulerAsRunner(s *autoDreamScheduler) contract.Runner {
	return contract.AsRunner(s)
}

func nestedIngestWorkerAsRunner(w *nestedIngestWorker) contract.Runner {
	return contract.AsRunner(w)
}

func teamSyncCoordinatorAsRunner(c *teamSyncCoordinator) contract.Runner {
	return contract.AsRunner(c)
}
