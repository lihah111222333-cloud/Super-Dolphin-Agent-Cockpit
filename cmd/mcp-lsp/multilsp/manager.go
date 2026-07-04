package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

var (
	ErrManagerClosed       = errors.New("LSP manager is closed")
	ErrClientFactoryNil    = errors.New("LSP client factory is nil")
	ErrWorkspaceRootEmpty  = errors.New("workspace root is empty")
	ErrDocumentTargetEmpty = errors.New("document target is empty")
)

// 诊断等待默认值控制工具调用在语言服务器启动阶段的最短等待与最大阻塞时间。
const (
	defaultDiagnosticsInitialDelay = 80 * time.Millisecond
	defaultDiagnosticsPollInterval = 40 * time.Millisecond
	defaultDiagnosticsMaxWait      = 1500 * time.Millisecond
)

// Manager 聚合单 workspace LSP 管理器的工具面、客户端确保能力和后台 runner 入口。
type Manager interface {
	ClientEnsurer
	lspmanager.Manager
	BackgroundRunnerProvider
}

// ClientEnsurer 为指定文件和语言准备可用客户端，调用方无需知道底层 workspace 分片。
type ClientEnsurer interface {
	EnsureClient(ctx context.Context, filePath, languageID string) (Client, error)
}

// BackgroundRunnerProvider 暴露 manager 需要由应用生命周期托管的后台任务。
type BackgroundRunnerProvider interface {
	// BackgroundRunner 返回 manager 的后台回收 runner。
	// runner 由应用根生命周期统一启动和取消；没有后台任务时返回 nil。
	BackgroundRunner() platformrunner.Runner
}

// ClientFactory 创建绑定到已解析 workspace root 的语言服务器客户端。
type ClientFactory interface {
	// NewClient 创建 subprocess Dir 绑定到 rootDir 的语言客户端。
	// rootDir 必须来自本次调用解析出的 workspace，工厂不能捕获 mcp-lsp 启动目录，
	// 否则多 workspace 会把语言服务器启动到错误项目。
	NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error)
}

// ClientFactoryWithEnv 在创建客户端时允许注入按语言解析出的环境变量。
type ClientFactoryWithEnv interface {
	ClientFactory
	NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error)
}

// ClientFactoryFunc 将普通函数适配为 ClientFactory，便于测试和轻量 wiring。
type ClientFactoryFunc func(rootDir string, handler protocol.NotificationHandler) (Client, error)

// NewClient 用函数实现创建客户端的接口方法。
func (fn ClientFactoryFunc) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, handler)
}

// ClientFactoryWithEnvFunc 将普通函数适配为支持环境变量的 ClientFactory。
type ClientFactoryWithEnvFunc func(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error)

// NewClient 用空环境变量调用带 env 的工厂，保持 ClientFactory 兼容。
func (fn ClientFactoryWithEnvFunc) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, nil, handler)
}

// NewClientWithEnv 用调用方提供的 env 创建客户端。
func (fn ClientFactoryWithEnvFunc) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, env, handler)
}

// Config 描述 multilsp manager 的启动参数和诊断等待策略。
type Config struct {
	WorkspaceRoot                    string                   // 默认 workspace root，空值时回退到当前进程目录。
	ClientFactory                    ClientFactory            // 创建语言服务器客户端的必填工厂。
	LanguageAdapters                 *LanguageAdapterRegistry // 语言适配器注册表，空值时使用默认注册表。
	DiagnosticsInitialDelay          time.Duration            // 首次等待诊断前的延迟。
	DiagnosticsPollInterval          time.Duration            // 轮询诊断稳定性的间隔。
	DiagnosticsMaxWait               time.Duration            // 单次诊断等待的最大时长。
	Logger                           *slog.Logger             // 可选日志器，空值时下游使用默认日志路径。
	DisableInitialWorkspaceBootstrap bool                     // 是否跳过启动时的 workspace 预热。
}

// manager 维护 workspace 客户端、诊断缓存和后台分片池；所有共享状态必须经对应锁访问。
type manager struct {
	workspaceRoot                    string                   // manager 默认根目录，参与相对路径解析。
	factory                          ClientFactory            // 语言服务器客户端工厂。
	adapters                         *LanguageAdapterRegistry // 语言到 root/能力策略的适配表。
	logger                           *slog.Logger             // manager 内部诊断日志。
	pool                             *ManagerPool             // 按 scope 分片复用的子 manager 池。
	disableInitialWorkspaceBootstrap bool                     // 跳过初始化预热的开关。
	retryBaseDelay                   time.Duration            // transient retry 的基础退避时间。

	mu         sync.RWMutex                // 保护 closed 与 workspaces。
	ensureMu   sync.Mutex                  // 串行化客户端创建，避免同一 workspace 重复启动。
	closed     bool                        // manager 关闭后禁止再创建客户端。
	workspaces map[string]*workspaceClient // workspace key 到客户端状态。

	diagGeneration   atomic.Uint64                 // 诊断缓存代际，关闭/刷新时递增。
	diagMu           sync.RWMutex                  // 保护 diagnostics。
	diagnostics      map[string]diagnosticSnapshot // scope+URI 维度的诊断快照。
	diagnosticEpochs map[string]uint64             // scope+URI 维度的文档诊断 epoch。
	diagInitial      time.Duration                 // 诊断稳定等待前置延迟。
	diagPoll         time.Duration                 // 诊断稳定等待轮询间隔。
	diagMaxWait      time.Duration                 // 诊断稳定等待最大时长。

	explicitOpenMu sync.RWMutex        // 保护 explicitlyOpen。
	explicitlyOpen map[string]struct{} // 工具主动打开且尚未关闭的文档 URI。

	coordinatorMu sync.Mutex            // 保护启动协调器的懒初始化。
	coordinator   *bootstrapCoordinator // workspace bootstrap 去重协调器。
}

// workspaceClient 保存单个 workspace/language 客户端及其 root 身份。
type workspaceClient struct {
	key              string                     // workspace 缓存键。
	rootPath         string                     // 本地绝对 root 路径。
	rootURI          string                     // root 的 file URI。
	languageID       string                     // 该客户端服务的语言 ID。
	env              []string                   // 创建客户端时使用的环境变量。
	workspaceFolders []protocol.WorkspaceFolder // 发送给语言服务器的 workspace folders。
	client           Client                     // 实际 LSP 客户端。
	lastActivity     time.Time                  // recycler 判断闲置的依据。
}

// diagnosticSnapshot 是诊断缓存中的一条不可变快照，按 scope 与 URI 精确隔离。
type diagnosticSnapshot struct {
	scopeKey      string                            // agent/thread scope key。
	workspaceKey  string                            // workspace root 缓存键。
	language      string                            // 诊断来源语言。
	uri           string                            // 被诊断文档 URI。
	generation    uint64                            // 缓存代际，避免读取关闭前旧数据。
	fingerprint   string                            // 文件内容指纹。
	mtimeNS       int64                             // 文件修改时间纳秒。
	size          int64                             // 文件大小。
	updatedAt     time.Time                         // 最近一次诊断更新时间。
	documentEpoch uint64                            // publishDiagnostics 捕获到的文档诊断 epoch。
	source        string                            // 诊断来源说明。
	state         diagnosticState                   // ready/stale/deleted 等状态。
	params        protocol.PublishDiagnosticsParams // 原始 publishDiagnostics 参数。
}

// documentRef 是工具输入文件解析后的规范引用，后续请求统一使用 URI 和 absPath。
type documentRef struct {
	raw        string // 用户或工具传入的原始目标。
	uri        string // 规范 file URI。
	absPath    string // 规范绝对路径。
	languageID string // 解析出的语言 ID。
}

// workspaceConfig 是创建或复用客户端所需的完整 workspace 配置。
type workspaceConfig struct {
	key              string                     // workspace 缓存键。
	rootPath         string                     // 实际启动语言服务器的目录。
	rootURI          string                     // rootPath 对应 URI。
	languageID       string                     // 目标语言 ID。
	projectRoot      string                     // 语言适配器识别出的项目根。
	languageSpecific map[string]string          // 语言适配器附加的 root 元数据。
	env              []string                   // 创建客户端时注入的环境。
	workspaceFolders []protocol.WorkspaceFolder // 初始化请求中的 workspace folders。
}

var (
	_ Manager                      = (*manager)(nil)
	_ protocol.NotificationHandler = (*manager)(nil)
)

// NewManager 根据配置创建 manager，空 root 会回退到当前目录但工厂缺失仍保持 fail-fast。
func NewManager(cfg Config) Manager {
	root, err := platformshared.NormalizeAbsolutePath(cfg.WorkspaceRoot)
	if err != nil {
		root = ""
	}
	if root == "" {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			root, _ = platformshared.NormalizeAbsolutePath(cwd)
		}
	}
	mgr := &manager{
		workspaceRoot:                    root,
		factory:                          cfg.ClientFactory,
		adapters:                         cfg.LanguageAdapters,
		logger:                           cfg.Logger,
		workspaces:                       make(map[string]*workspaceClient),
		diagnostics:                      make(map[string]diagnosticSnapshot),
		diagnosticEpochs:                 make(map[string]uint64),
		explicitlyOpen:                   make(map[string]struct{}),
		diagInitial:                      chooseDuration(cfg.DiagnosticsInitialDelay, defaultDiagnosticsInitialDelay),
		diagPoll:                         chooseDuration(cfg.DiagnosticsPollInterval, defaultDiagnosticsPollInterval),
		diagMaxWait:                      chooseDuration(cfg.DiagnosticsMaxWait, defaultDiagnosticsMaxWait),
		disableInitialWorkspaceBootstrap: cfg.DisableInitialWorkspaceBootstrap,
		retryBaseDelay:                   500 * time.Millisecond,
	}
	if mgr.adapters == nil {
		mgr.adapters = NewDefaultLanguageAdapterRegistry()
	}
	mgr.diagGeneration.Store(1)
	mgr.pool = NewManagerPool(mgr, PoolSizeFromEnv())
	return mgr
}

// cloneForWorkspace 为 scoped workspace 创建独立 manager 状态。
// clone 复用工厂、适配器和诊断等待策略，但重新分配客户端、诊断和显式打开文档缓存。
func (m *manager) cloneForWorkspace(workspaceRoot string) *manager {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" && m != nil {
		root = m.workspaceRoot
	}
	if normalized, err := platformshared.NormalizeAbsolutePath(root); err == nil && normalized != "" {
		root = normalized
	}
	clone := &manager{
		workspaceRoot:    root,
		workspaces:       make(map[string]*workspaceClient),
		diagnostics:      make(map[string]diagnosticSnapshot),
		diagnosticEpochs: make(map[string]uint64),
		explicitlyOpen:   make(map[string]struct{}),
	}
	if m != nil {
		clone.factory = m.factory
		clone.adapters = m.adapters
		clone.logger = m.logger
		clone.pool = m.pool
		clone.diagInitial = m.diagInitial
		clone.diagPoll = m.diagPoll
		clone.diagMaxWait = m.diagMaxWait
	}
	clone.diagGeneration.Store(1)
	return clone
}

// effectiveWorkspaceRoot 从可信 ctx 读取本次工具调用的 workspace root。
// 相对路径和 language-only 查询必须跟随每次调用注入的 CWD，缺失上下文时直接报错，
// 避免回退到 mcp-lsp 启动目录导致跨项目读写。
func (m *manager) effectiveWorkspaceRoot(ctx context.Context) (string, error) {
	if ctx != nil {
		root, err := common.WorkspaceRootFromContextStrict(ctx)
		if err != nil {
			return "", err
		}
		if trimmed := strings.TrimSpace(root); trimmed != "" {
			if normalized, err := platformshared.NormalizeAbsolutePath(trimmed); err == nil && normalized != "" {
				return normalized, nil
			}
		}
	}
	return "", errors.New("strict context enforcement failed: missing context")
}

func (m *manager) adapterForLanguage(languageID string) (LanguageAdapter, error) {
	lang := normalizeLanguageID(languageID)
	if lang == "" {
		return nil, fmt.Errorf("LSP language is empty")
	}
	registry := m.adapters
	if registry == nil {
		registry = NewDefaultLanguageAdapterRegistry()
	}
	adapter, ok := registry.AdapterForLanguage(lang)
	if !ok {
		return nil, fmt.Errorf("unsupported language adapter %q", lang)
	}
	return adapter, nil
}

func (m *manager) capabilityPolicy(languageID string) ToolCapabilityPolicy {
	adapter, err := m.adapterForLanguage(languageID)
	if err != nil {
		return ToolCapabilityPolicy{RequiresLSPClient: true}
	}
	return adapter.CapabilityPolicy()
}

func (m *manager) shouldUseClientForLanguage(languageID string) bool {
	return m.capabilityPolicy(languageID).RequiresLSPClient
}

// resolveDocumentRef 把工具传入的路径或 file URI 解析为规范文档引用。
// 空目标、非法路径或缺失可信 workspace 会立即返回错误，避免后续 LSP 请求落到错误文件。
func (m *manager) resolveDocumentRef(ctx context.Context, target, languageID string) (documentRef, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return documentRef{}, ErrDocumentTargetEmpty
	}
	baseRoot, err := m.effectiveWorkspaceRoot(ctx)
	if err != nil {
		return documentRef{}, err
	}
	var (
		absPath string
	)
	if strings.HasPrefix(trimmed, "file://") {
		absPath, err = absolutePathFromURI(trimmed)
	} else if !filepath.IsAbs(trimmed) && baseRoot != "" {
		absPath, err = platformshared.NormalizeAbsolutePath(filepath.Join(baseRoot, trimmed))
	} else {
		absPath, err = platformshared.NormalizeAbsolutePath(trimmed)
	}
	if err != nil {
		return documentRef{}, err
	}
	lang := normalizeLanguageID(languageID)
	if lang == "" {
		lang = lspmanager.DetectLanguageID(absPath)
	}
	return documentRef{
		raw:        target,
		uri:        fileURIFromPath(absPath),
		absPath:    absPath,
		languageID: lang,
	}, nil
}

func (m *manager) resolveWorkspaceForDocument(ctx context.Context, ref documentRef) (workspaceConfig, error) {
	if ref.absPath == "" {
		return workspaceConfig{}, ErrWorkspaceRootEmpty
	}
	scope, adapter, err := m.resolveLanguageScope(ctx, ref.languageID, ref.absPath, ref.uri)
	if err != nil {
		return workspaceConfig{}, err
	}
	return workspaceConfigForLanguageScope(scope, adapter)
}

func (m *manager) resolveLanguageScope(ctx context.Context, languageID, targetPath, targetURI string) (ResolvedLanguageScope, LanguageAdapter, error) {
	adapter, err := m.adapterForLanguage(languageID)
	if err != nil {
		return ResolvedLanguageScope{}, nil, err
	}
	scope, err := m.adapterToolScope(ctx, languageID, targetPath, targetURI)
	if err != nil {
		return ResolvedLanguageScope{}, nil, err
	}
	resolved, err := adapter.ResolveRoot(ctx, scope, targetPath)
	if err != nil {
		return ResolvedLanguageScope{}, nil, err
	}
	resolved = completeResolvedLanguageScope(resolved, scope)
	resolved.LanguageSpecific = mergeLanguageSpecific(resolved.LanguageSpecific, adapter.CacheKeyParts(resolved))
	if err := ensureResolvedLanguageScopeWithinWorkspaceRoots(scope.WorkspaceRoots, resolved); err != nil {
		return ResolvedLanguageScope{}, nil, err
	}
	return resolved, adapter, nil
}

// adapterToolScope 从调用上下文构造语言适配器使用的可信 scope。
// 它会按目标文件选择最窄 workspace root，并在目标越界时 fail-fast。
func (m *manager) adapterToolScope(ctx context.Context, languageID, targetPath, targetURI string) (LSPToolScope, error) {
	scope := lspToolScopeFromContext(ctx)
	if scope.CWD == "" {
		baseRoot, err := m.effectiveWorkspaceRoot(ctx)
		if err != nil {
			return LSPToolScope{}, err
		}
		scope.CWD = baseRoot
	}
	scope.WorkspaceRoots = normalizeScopeWorkspaceRoots(scope.CWD, scope.WorkspaceRoots)
	selected, err := selectWorkspaceRootForTarget(scope.WorkspaceRoots, firstNonEmpty(targetPath, targetURI))
	if err != nil {
		return LSPToolScope{}, err
	}
	if selected != "" {
		scope.CWD = selected
		scope.WorkspaceRoots = normalizeScopeWorkspaceRoots(scope.CWD, scope.WorkspaceRoots)
	}
	scope.LanguageID = normalizeLanguageID(languageID)
	scope.TargetPath = targetPath
	scope.TargetURI = targetURI
	if err := ensurePathWithinWorkspaceRoots(scope.WorkspaceRoots, firstNonEmpty(targetPath, targetURI)); err != nil {
		return LSPToolScope{}, err
	}
	return scope, nil
}

// completeResolvedLanguageScope 补齐适配器未返回的 root 字段。
// 默认值只来自已验证的 tool scope，保证后续 workspace key 和初始化 folders 使用同一边界。
func completeResolvedLanguageScope(resolved ResolvedLanguageScope, scope LSPToolScope) ResolvedLanguageScope {
	if resolved.LanguageID == "" {
		resolved.LanguageID = scope.LanguageID
	}
	if resolved.WorkspaceRoot == "" {
		resolved.WorkspaceRoot = firstNonEmpty(scope.WorkspaceRoot, scope.CWD, filepath.Dir(scope.TargetPath))
	}
	if resolved.LanguageWorkspaceRoot == "" {
		resolved.LanguageWorkspaceRoot = resolved.WorkspaceRoot
	}
	if resolved.ProjectRoot == "" {
		resolved.ProjectRoot = resolved.WorkspaceRoot
	}
	if resolved.RootKind == "" {
		resolved.RootKind = "dir_fallback"
	}
	return resolved
}

func workspaceConfigForLanguageScope(scope ResolvedLanguageScope, adapter LanguageAdapter) (workspaceConfig, error) {
	key, err := buildWorkspaceKey(LSPToolScope{
		LanguageID:            scope.LanguageID,
		RootKind:              scope.RootKind,
		WorkspaceRoot:         scope.WorkspaceRoot,
		LanguageWorkspaceRoot: scope.LanguageWorkspaceRoot,
		ProjectRoot:           scope.ProjectRoot,
		LanguageSpecific:      scope.LanguageSpecific,
	})
	if err != nil {
		return workspaceConfig{}, err
	}
	rootURI := fileURIFromPath(scope.WorkspaceRoot)
	folders := scope.WorkspaceFolders
	if len(folders) == 0 {
		folders = workspaceFoldersFromRootURI(rootURI)
	}
	return workspaceConfig{
		key:              key,
		rootPath:         scope.WorkspaceRoot,
		rootURI:          rootURI,
		languageID:       scope.LanguageID,
		projectRoot:      scope.ProjectRoot,
		languageSpecific: copyLanguageSpecific(scope.LanguageSpecific),
		env:              adapter.EnvPolicy(scope),
		workspaceFolders: cloneWorkspaceFolders(folders),
	}, nil
}

func mergeLanguageSpecific(base, extra map[string]string) map[string]string {
	merged := copyLanguageSpecific(base)
	for key, value := range extra {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if merged == nil {
			merged = map[string]string{}
		}
		merged[trimmed] = strings.TrimSpace(value)
	}
	return merged
}

func chooseDuration(given, fallback time.Duration) time.Duration {
	if given > 0 {
		return given
	}
	return fallback
}

func decodeLocationResults(raw json.RawMessage) ([]protocol.LocationResult, error) {
	results, err := decodeUnionListWithMode(raw, true, decodeLocationUnion)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func decodeDocumentSymbols(raw json.RawMessage) ([]protocol.DocumentSymbol, error) {
	return decodeUnionList(raw, decodeDocumentSymbolUnion)
}

func decodeWorkspaceSymbols(raw json.RawMessage) ([]protocol.WorkspaceSymbolResult, error) {
	return decodeUnionList(raw, decodeWorkspaceSymbolUnion)
}

// decodeCompletionList 兼容 LSP completion 返回的数组和 CompletionList 两种形态。
// 空或 null 响应会转为空列表，非法 JSON 保持错误返回给调用方。
func decodeCompletionList(raw json.RawMessage) (*protocol.CompletionList, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return &protocol.CompletionList{}, nil
	}
	if raw[0] == '[' {
		var items []protocol.CompletionItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("decode completion items: %w", err)
		}
		return &protocol.CompletionList{Items: items}, nil
	}
	var list protocol.CompletionList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode completion list: %w", err)
	}
	return &list, nil
}

func decodeCodeActions(raw json.RawMessage) ([]protocol.CodeActionResult, error) {
	return decodeUnionList(raw, decodeCodeActionUnion)
}
