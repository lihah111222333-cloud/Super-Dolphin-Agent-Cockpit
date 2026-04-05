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

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
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
	EnsureClient(ctx context.Context, filePath, languageID string) (Client, error)
	Close() error

	Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error)
	SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error)

	References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error)
	CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error)
	TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error)

	DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error)
	WorkspaceSymbol(ctx context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error)
	FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error)
	SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error)

	Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error)
	Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error)
	CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error)
	Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error)

	DidOpen(ctx context.Context, uri, languageID string, version int, text string) error
	DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error
	DidClose(ctx context.Context, uri string) error
	BootstrapDocument(ctx context.Context, uri string) error
	BootstrapDocumentOpenOnly(ctx context.Context, uri string) error

	Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error)
	WaitDiagnosticsStable(ctx context.Context, uris []string) error

	CurrentDiagnosticGeneration() uint64
	AdvanceDiagnosticGeneration() uint64
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
	} else {
		absPath, err = platformshared.NormalizeAbsolutePath(trimmed)
	}
	if err != nil {
		return documentRef{}, err
	}
	lang := normalizeLanguageID(languageID)
	if lang == "" {
		lang = detectLanguageID(absPath)
	}
	return documentRef{
		raw:        target,
		uri:        fileURIFromPath(absPath),
		absPath:    absPath,
		languageID: lang,
	}, nil
}

func (m *manager) resolveWorkspaceForDocument(ref documentRef) (workspaceConfig, error) {
	if ref.absPath == "" {
		return workspaceConfig{}, ErrWorkspaceRootEmpty
	}
	root := m.workspaceRoot
	if shouldUseGoWorkspace(ref.languageID) {
		goRoot, err := findGoModRoot(ref.absPath)
		if err != nil {
			return workspaceConfig{}, err
		}
		if goRoot != "" {
			root = goRoot
		}
	}
	if root == "" {
		root = filepath.Dir(ref.absPath)
	}
	root, err := platformshared.NormalizeAbsolutePath(root)
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
