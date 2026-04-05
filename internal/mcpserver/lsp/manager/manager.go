package manager

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

type Manager interface {
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
