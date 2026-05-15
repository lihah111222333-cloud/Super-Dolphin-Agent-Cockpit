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

const (
	defaultDiagnosticsInitialDelay = 80 * time.Millisecond
	defaultDiagnosticsPollInterval = 40 * time.Millisecond
	defaultDiagnosticsMaxWait      = 800 * time.Millisecond
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

func (fn ClientFactoryFunc) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, handler)
}

type ClientFactoryWithEnvFunc func(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error)

func (fn ClientFactoryWithEnvFunc) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, nil, handler)
}

func (fn ClientFactoryWithEnvFunc) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	return fn(rootDir, env, handler)
}

type Config struct {
	WorkspaceRoot           string
	ClientFactory           ClientFactory
	DiagnosticsInitialDelay time.Duration
	DiagnosticsPollInterval time.Duration
	DiagnosticsMaxWait      time.Duration
	Logger                  *slog.Logger
}

type manager struct {
	workspaceRoot string
	factory       ClientFactory
	logger        *slog.Logger
	pool          *ManagerPool

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
}

type workspaceClient struct {
	key              string
	rootPath         string
	rootURI          string
	languageID       string
	env              []string
	workspaceFolders []protocol.WorkspaceFolder
	client           Client
}

type diagnosticSnapshot struct {
	params     protocol.PublishDiagnosticsParams
	generation uint64
	updatedAt  time.Time
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
	env              []string
	workspaceFolders []protocol.WorkspaceFolder
}

var (
	_ Manager                      = (*manager)(nil)
	_ protocol.NotificationHandler = (*manager)(nil)
)

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
		workspaceRoot: root,
		factory:       cfg.ClientFactory,
		logger:        cfg.Logger,
		workspaces:    make(map[string]*workspaceClient),
		diagnostics:   make(map[string]diagnosticSnapshot),
		diagInitial:   chooseDuration(cfg.DiagnosticsInitialDelay, defaultDiagnosticsInitialDelay),
		diagPoll:      chooseDuration(cfg.DiagnosticsPollInterval, defaultDiagnosticsPollInterval),
		diagMaxWait:   chooseDuration(cfg.DiagnosticsMaxWait, defaultDiagnosticsMaxWait),
	}
	mgr.diagGeneration.Store(1)
	mgr.pool = NewManagerPool(mgr, PoolSizeFromEnv())
	return mgr
}

// effectiveWorkspaceRoot picks the workspace root for resolving relative
// paths / language-only workspace lookups. When the MCP toolbridge has
// injected a per-call _cwd into ctx (see internal/mcpserver/common +
// cmd/mcp-lsp/fx.go OnToolsCall), the manager MUST follow that cwd
// instead of the build-time m.workspaceRoot, otherwise an agent bound
// to a project other than the mcp-lsp startup directory ends up looking
// up symbols / opening files in the wrong project.
func (m *manager) effectiveWorkspaceRoot(ctx context.Context) string {
	if ctx != nil {
		if cwd, ok := ctx.Value(common.CwdContextKey).(string); ok {
			if trimmed := strings.TrimSpace(cwd); trimmed != "" {
				if normalized, err := platformshared.NormalizeAbsolutePath(trimmed); err == nil && normalized != "" {
					return normalized
				}
			}
		}
	}
	return m.workspaceRoot
}

func (m *manager) resolveDocumentRef(ctx context.Context, target, languageID string) (documentRef, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return documentRef{}, ErrDocumentTargetEmpty
	}
	baseRoot := m.effectiveWorkspaceRoot(ctx)
	var (
		absPath string
		err     error
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

func resolveProjectRoot(languageID, absPath string) (string, error) {
	switch {
	case shouldUseGoWorkspace(languageID):
		return findGoModRoot(absPath)
	case shouldUseJSTSWorkspace(languageID):
		return findJSTSProjectRoot(absPath)
	case shouldUseJavaWorkspace(languageID):
		return findJavaProjectRoot(absPath)
	default:
		return "", nil
	}
}

func (m *manager) resolveWorkspaceForDocument(ctx context.Context, ref documentRef) (workspaceConfig, error) {
	if ref.absPath == "" {
		return workspaceConfig{}, ErrWorkspaceRootEmpty
	}
	if shouldUseGoWorkspace(ref.languageID) {
		return m.resolveGoWorkspaceForDocument(ctx, ref)
	}
	root := m.effectiveWorkspaceRoot(ctx)
	langRoot, err := resolveProjectRoot(ref.languageID, ref.absPath)
	if err != nil {
		return workspaceConfig{}, err
	}
	if langRoot != "" {
		root = langRoot
	}
	if root == "" {
		root = filepath.Dir(ref.absPath)
	}
	root, err = platformshared.NormalizeAbsolutePath(root)
	if err != nil {
		return workspaceConfig{}, err
	}
	return workspaceConfigForRoot(root, ref.languageID), nil
}

func (m *manager) resolveGoWorkspaceForDocument(ctx context.Context, ref documentRef) (workspaceConfig, error) {
	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      m.effectiveWorkspaceRoot(ctx),
		FilePath: ref.absPath,
		Env:      os.Environ(),
	})
	if err != nil {
		return workspaceConfig{}, err
	}
	return workspaceConfigForGoRoot(info, ref.languageID), nil
}

func workspaceConfigForRoot(root, languageID string) workspaceConfig {
	rootURI := fileURIFromPath(root)
	return workspaceConfig{
		key:              root,
		rootPath:         root,
		rootURI:          rootURI,
		languageID:       languageID,
		workspaceFolders: workspaceFoldersFromRootURI(rootURI),
	}
}

func workspaceConfigForGoRoot(info GoRootInfo, languageID string) workspaceConfig {
	root := info.WorkspaceRoot
	if root == "" {
		root = info.ProjectRoot
	}
	rootURI := fileURIFromPath(root)
	return workspaceConfig{
		key:              goWorkspaceKey(info),
		rootPath:         root,
		rootURI:          rootURI,
		languageID:       normalizeGoWorkspaceLanguageID(languageID),
		env:              goRootEnv(info),
		workspaceFolders: workspaceFolders(info),
	}
}

func normalizeGoWorkspaceLanguageID(languageID string) string {
	if normalized := normalizeLanguageID(languageID); normalized != "" {
		return normalized
	}
	return "go"
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
