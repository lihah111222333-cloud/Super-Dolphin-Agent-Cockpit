package gopls

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
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

var (
	ErrManagerClosed       = errors.New("gopls manager is closed")
	ErrClientFactoryNil    = errors.New("gopls client factory is nil")
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
	// P22 P2 gopls-S1 — docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md
	// §480-494.
	BackgroundRunner() platformrunner.Runner
}

type ClientFactory interface {
	NewClient(handler protocol.NotificationHandler) (Client, error)
}

type ClientFactoryFunc func(handler protocol.NotificationHandler) (Client, error)

func (fn ClientFactoryFunc) NewClient(handler protocol.NotificationHandler) (Client, error) {
	return fn(handler)
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
	key      string
	rootPath string
	rootURI  string
	client   Client
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
	key        string
	rootPath   string
	rootURI    string
	languageID string
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

func (m *manager) resolveDocumentRef(target, languageID string) (documentRef, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return documentRef{}, ErrDocumentTargetEmpty
	}
	var (
		absPath string
		err     error
	)
	if strings.HasPrefix(trimmed, "file://") {
		absPath, err = absolutePathFromURI(trimmed)
	} else if !filepath.IsAbs(trimmed) && m.workspaceRoot != "" {
		absPath, err = platformshared.NormalizeAbsolutePath(filepath.Join(m.workspaceRoot, trimmed))
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

func (m *manager) resolveWorkspaceForDocument(ref documentRef) (workspaceConfig, error) {
	if ref.absPath == "" {
		return workspaceConfig{}, ErrWorkspaceRootEmpty
	}
	root := m.workspaceRoot
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
	return workspaceConfig{
		key:        root,
		rootPath:   root,
		rootURI:    fileURIFromPath(root),
		languageID: ref.languageID,
	}, nil
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
