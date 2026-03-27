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
	WorkspaceSymbol(ctx context.Context, query string) ([]protocol.WorkspaceSymbolResult, error)
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
	root, err := normalizeAbsolutePath(cfg.WorkspaceRoot)
	if err != nil {
		root = ""
	}
	if root == "" {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			root, _ = normalizeAbsolutePath(cwd)
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
		absPath, err = normalizeAbsolutePath(trimmed)
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
	root, err := normalizeAbsolutePath(root)
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
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] != '[' {
		raw = append([]byte{'['}, append(raw, ']')...)
	}
	var payloads []json.RawMessage
	if err := json.Unmarshal(raw, &payloads); err != nil {
		return nil, fmt.Errorf("decode locations: %w", err)
	}
	results := make([]protocol.LocationResult, 0, len(payloads))
	for _, payload := range payloads {
		var location protocol.Location
		if err := json.Unmarshal(payload, &location); err == nil && location.URI != "" {
			results = append(results, protocol.LocationResult{Location: &location, Canonical: &location})
			continue
		}
		var link protocol.LocationLink
		if err := json.Unmarshal(payload, &link); err == nil && link.TargetURI != "" {
			results = append(results, protocol.LocationResult{LocationLink: &link})
		}
	}
	return results, nil
}

func decodeDocumentSymbols(raw json.RawMessage) ([]protocol.DocumentSymbol, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var symbols []protocol.DocumentSymbol
	if err := json.Unmarshal(raw, &symbols); err == nil {
		return symbols, nil
	}
	var infos []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &infos); err != nil {
		return nil, fmt.Errorf("decode document symbols: %w", err)
	}
	symbols = make([]protocol.DocumentSymbol, 0, len(infos))
	for _, info := range infos {
		symbols = append(symbols, protocol.DocumentSymbol{
			Name:           info.Name,
			Kind:           info.Kind,
			Range:          info.Location.Range,
			SelectionRange: info.Location.Range,
		})
	}
	return symbols, nil
}

func decodeWorkspaceSymbols(raw json.RawMessage) ([]protocol.WorkspaceSymbolResult, error) {
	var payloads []json.RawMessage
	if err := decodeInto(raw, &payloads); err != nil {
		return nil, fmt.Errorf("decode workspace symbols: %w", err)
	}
	results := make([]protocol.WorkspaceSymbolResult, 0, len(payloads))
	for _, payload := range payloads {
		var symbol protocol.WorkspaceSymbol
		if err := json.Unmarshal(payload, &symbol); err == nil && symbol.Name != "" {
			results = append(results, protocol.WorkspaceSymbolResult{WorkspaceSymbol: &symbol})
			continue
		}
		var info protocol.SymbolInformation
		if err := json.Unmarshal(payload, &info); err == nil && info.Name != "" {
			results = append(results, protocol.WorkspaceSymbolResult{SymbolInformation: &info})
		}
	}
	return results, nil
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
	var payloads []json.RawMessage
	if err := decodeInto(raw, &payloads); err != nil {
		return nil, fmt.Errorf("decode code actions: %w", err)
	}
	results := make([]protocol.CodeActionResult, 0, len(payloads))
	for _, payload := range payloads {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(payload, &keys); err != nil {
			return nil, fmt.Errorf("decode code action entry: %w", err)
		}
		if rawCommand, ok := keys["command"]; ok && len(rawCommand) > 0 && rawCommand[0] == '"' {
			var cmd protocol.Command
			if err := json.Unmarshal(payload, &cmd); err != nil {
				return nil, fmt.Errorf("decode command action: %w", err)
			}
			results = append(results, protocol.CodeActionResult{Command: &cmd})
			continue
		}
		var action protocol.CodeAction
		if err := json.Unmarshal(payload, &action); err != nil {
			return nil, fmt.Errorf("decode structured code action: %w", err)
		}
		results = append(results, protocol.CodeActionResult{CodeAction: &action})
	}
	return results, nil
}
