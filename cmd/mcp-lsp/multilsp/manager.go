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
	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

var (
	ErrManagerClosed       = errors.New("LSP manager is closed")
	ErrClientFactoryNil    = errors.New("LSP client factory is nil")
	ErrWorkspaceRootEmpty  = errors.New("workspace root is empty")
	ErrDocumentTargetEmpty = errors.New("document target is empty")
)

const (
	defaultDiagnosticsInitialDelay = 80 * time.Millisecond
	defaultDiagnosticsPollInterval = 40 * time.Millisecond
	defaultDiagnosticsMaxWait      = 1500 * time.Millisecond
)

type Manager interface {
	ClientEnsurer
	lspmanager.Manager
	BackgroundRunnerProvider
}

type ClientEnsurer interface {
	EnsureClient(ctx context.Context, filePath, languageID string) (Client, error)
}

type BackgroundRunnerProvider interface {
	// BackgroundRunner returns the long-running owner(s) for this
	// manager's background work (currently the ManagerPool recycler).
	// The root `group:"runners"` aggregation drives them via ctx; nil
	// is returned when the manager has no background work to own.
	// P22 P2 LSP-S1 — docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md
	// §480-494.
	BackgroundRunner() platformrunner.Runner
}

type ClientFactory interface {
	// NewClient creates a language client whose subprocess Dir is bound
	// to rootDir. rootDir is the per-workspace root resolved at call
	// time (cfg.rootPath in createAndRegisterClient); the factory MUST
	// NOT capture mcp-lsp's startup CWD via closure, otherwise the
	// language server inherits the wrong project root when sessions
	// span multiple workspaces.
	NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error)
}

type ClientFactoryWithEnv interface {
	ClientFactory
	NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error)
}

type ClientFactoryFunc func(rootDir string, handler protocol.NotificationHandler) (Client, error)

// NewClient 创建客户端。
func (fn ClientFactoryFunc) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, handler)
}

type ClientFactoryWithEnvFunc func(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error)

// NewClient 创建客户端。
func (fn ClientFactoryWithEnvFunc) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, nil, handler)
}

// NewClientWithEnv 创建带env的客户端。
func (fn ClientFactoryWithEnvFunc) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, env, handler)
}

type Config struct {
	WorkspaceRoot                    string
	ClientFactory                    ClientFactory
	LanguageAdapters                 *LanguageAdapterRegistry
	DiagnosticsInitialDelay          time.Duration
	DiagnosticsPollInterval          time.Duration
	DiagnosticsMaxWait               time.Duration
	Logger                           *slog.Logger
	DisableInitialWorkspaceBootstrap bool
}

type manager struct {
	workspaceRoot                    string
	factory                          ClientFactory
	adapters                         *LanguageAdapterRegistry
	logger                           *slog.Logger
	pool                             *ManagerPool
	disableInitialWorkspaceBootstrap bool
	retryBaseDelay                   time.Duration

	mu         sync.RWMutex
	ensureMu   sync.Mutex
	closed     bool
	workspaces map[string]*workspaceClient

	diagGeneration atomic.Uint64
	diagMu         sync.RWMutex
	diagnostics    map[string]diagnosticSnapshot
	diagInitial    time.Duration
	diagPoll       time.Duration
	diagMaxWait    time.Duration

	explicitOpenMu sync.RWMutex
	explicitlyOpen map[string]struct{}

	coordinatorMu sync.Mutex
	coordinator   *bootstrapCoordinator
}

type workspaceClient struct {
	key              string
	rootPath         string
	rootURI          string
	languageID       string
	env              []string
	workspaceFolders []protocol.WorkspaceFolder
	client           Client
	lastActivity     time.Time
}

type diagnosticSnapshot struct {
	scopeKey     string
	workspaceKey string
	language     string
	uri          string
	generation   uint64
	fingerprint  string
	mtimeNS      int64
	size         int64
	updatedAt    time.Time
	source       string
	state        diagnosticState
	params       protocol.PublishDiagnosticsParams
}

type documentRef struct {
	raw        string
	uri        string
	absPath    string
	languageID string
}

type workspaceConfig struct {
	key              string
	rootPath         string
	rootURI          string
	languageID       string
	projectRoot      string
	languageSpecific map[string]string
	env              []string
	workspaceFolders []protocol.WorkspaceFolder
}

var (
	_ Manager                      = (*manager)(nil)
	_ protocol.NotificationHandler = (*manager)(nil)
)

// NewManager 创建manager。
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

// cloneForWorkspace 为工作区复制LSP。
func (m *manager) cloneForWorkspace(workspaceRoot string) *manager {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" && m != nil {
		root = m.workspaceRoot
	}
	if normalized, err := platformshared.NormalizeAbsolutePath(root); err == nil && normalized != "" {
		root = normalized
	}
	clone := &manager{
		workspaceRoot:  root,
		workspaces:     make(map[string]*workspaceClient),
		diagnostics:    make(map[string]diagnosticSnapshot),
		explicitlyOpen: make(map[string]struct{}),
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

// effectiveWorkspaceRoot picks the workspace root for resolving relative
// paths / language-only workspace lookups. When the MCP toolbridge has
// injected a per-call _cwd into ctx (see internal/mcpserver/runtime +
// cmd/mcp-lsp/fx.go OnToolsCall), the manager MUST follow that cwd
// instead of the build-time m.workspaceRoot, otherwise an agent bound
// to a project other than the mcp-lsp startup directory ends up looking
// up symbols / opening files in the wrong project.
// effectiveWorkspaceRoot 处理effective工作区根目录。
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

// resolveDocumentRef 解析document引用。
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

// adapterToolScope 处理适配器工具作用域。
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

// completeResolvedLanguageScope 完成已解析语言作用域。
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

// decodeCompletionList 解码补全list。
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
