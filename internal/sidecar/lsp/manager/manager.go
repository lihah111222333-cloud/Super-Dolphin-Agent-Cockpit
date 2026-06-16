package manager

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
)

// ErrDiagnosticsNotReady identifies a manager error condition.
var ErrDiagnosticsNotReady = errors.New("diagnostics not ready")

// Manager coordinates manager runtime behavior.
type Manager interface {
	LifecycleManager
	NavigationManager
	XRefManager
	StructureManager
	CompletionManager
	EditManager
	DocumentLifecycleManager
	DiagnosticsManager
}

// LifecycleManager coordinates manager runtime behavior.
type LifecycleManager interface {
	Close() error
}

// NavigationManager coordinates manager runtime behavior.
type NavigationManager interface {
	Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error)
	SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error)
}

// XRefManager coordinates manager runtime behavior.
type XRefManager interface {
	References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error)
	CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error)
	TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error)
}

// StructureManager coordinates manager runtime behavior.
type StructureManager interface {
	DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error)
	WorkspaceSymbol(ctx context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error)
	FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error)
	SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error)
}

// BestEffortDocumentSymbolManager coordinates manager runtime behavior.
type BestEffortDocumentSymbolManager interface {
	DocumentSymbolBestEffort(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error)
}

// CompletionManager coordinates manager runtime behavior.
type CompletionManager interface {
	Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error)
}

// EditManager coordinates manager runtime behavior.
type EditManager interface {
	Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error)
	CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error)
	Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error)
}

// DocumentLifecycleManager coordinates manager runtime behavior.
type DocumentLifecycleManager interface {
	DidOpen(ctx context.Context, uri, languageID string, version int, text string) error
	DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error
	DidClose(ctx context.Context, uri string) error
	BootstrapDocument(ctx context.Context, uri string) error
	BootstrapDocumentOpenOnly(ctx context.Context, uri string) error
}

// DiagnosticsManager coordinates manager runtime behavior.
type DiagnosticsManager interface {
	Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error)
	WaitDiagnosticsStable(ctx context.Context, uris []string) error
	CurrentDiagnosticGeneration() uint64
	AdvanceDiagnosticGeneration() uint64
}
