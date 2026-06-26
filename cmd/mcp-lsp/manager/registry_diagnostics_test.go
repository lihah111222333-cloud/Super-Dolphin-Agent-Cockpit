package manager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type registryContextKey struct{}

func TestRegistryDiagnosticsAllKeepsCallerContext(t *testing.T) {
	mgr := &registryDiagnosticsManager{}
	registry := NewRegistry(nil)
	registry.Register("go", mgr)

	ctx := context.WithValue(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), registryContextKey{}, "caller-scope")
	if _, err := registry.Diagnostics(ctx, nil); err != nil {
		t.Fatalf("Diagnostics(ctx, nil): %v", err)
	}
	if got := mgr.diagnosticsContext.Value(registryContextKey{}); got != "caller-scope" {
		t.Fatalf("Diagnostics ctx value = %#v, want caller-scope", got)
	}
}

func TestRegistryGroupURIWaitUsesCallerContext(t *testing.T) {
	mgr := &registryDiagnosticsManager{}
	registry := NewRegistry(nil)
	registry.Register("go", mgr)

	ctx := context.WithValue(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), registryContextKey{}, "group-scope")
	if err := registry.WaitDiagnosticsStable(ctx, []string{"file:///tmp/registry-group.go"}); err != nil {
		t.Fatalf("WaitDiagnosticsStable: %v", err)
	}
	if got := mgr.waitContext.Value(registryContextKey{}); got != "group-scope" {
		t.Fatalf("WaitDiagnosticsStable ctx value = %#v, want group-scope", got)
	}
}

func TestRegistryDiagnosticsExplicitUnsupportedLanguageReturnsFileError(t *testing.T) {
	registry := NewRegistry(nil)
	registry.Register("go", &registryDiagnosticsManager{})

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: t.TempDir()})
	unsupportedURI := "file:///tmp/unsupported-language.zzz"
	cases := map[string]func(context.Context, []string) error{
		"Diagnostics": func(ctx context.Context, uris []string) error {
			_, err := registry.Diagnostics(ctx, uris)
			return err
		},
		"WaitDiagnosticsStable": registry.WaitDiagnosticsStable,
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call(ctx, []string{unsupportedURI})
			if !errors.Is(err, ErrUnsupportedLanguage) {
				t.Fatalf("%s() error = %v, want ErrUnsupportedLanguage", name, err)
			}
			if !strings.Contains(err.Error(), unsupportedURI) {
				t.Fatalf("%s() error = %q, want unsupported file URI in error", name, err.Error())
			}
		})
	}
}

type registryDiagnosticsManager struct {
	diagnosticsContext context.Context
	waitContext        context.Context
}

func (*registryDiagnosticsManager) Close() error { return nil }

func (*registryDiagnosticsManager) Definition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) Implementation(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) TypeDefinition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) Hover(context.Context, string, protocol.Position) (*protocol.HoverResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) SignatureHelp(context.Context, string, protocol.Position) (*protocol.SignatureHelpResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) References(context.Context, string, protocol.Position, bool) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) CallHierarchy(context.Context, string, protocol.Position, string) ([]protocol.CallHierarchyResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) TypeHierarchy(context.Context, string, protocol.Position, string) ([]protocol.TypeHierarchyResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) DocumentSymbol(context.Context, string) ([]protocol.DocumentSymbol, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) WorkspaceSymbol(context.Context, string, string) ([]protocol.WorkspaceSymbolResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) FoldingRange(context.Context, string) ([]protocol.FoldingRange, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) SemanticTokens(context.Context, string) (*protocol.SemanticTokensResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) Completion(context.Context, string, protocol.Position) (*protocol.CompletionList, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) Rename(context.Context, string, protocol.Position, string) (*protocol.WorkspaceEdit, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) CodeAction(context.Context, string, protocol.Range, []string) ([]protocol.CodeActionResult, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) Format(context.Context, string, protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return nil, nil
}

func (*registryDiagnosticsManager) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (*registryDiagnosticsManager) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (*registryDiagnosticsManager) DidClose(context.Context, string) error { return nil }

func (*registryDiagnosticsManager) BootstrapDocument(context.Context, string) error { return nil }

func (*registryDiagnosticsManager) BootstrapDocumentOpenOnly(context.Context, string) error {
	return nil
}

func (m *registryDiagnosticsManager) Diagnostics(ctx context.Context, _ []string) ([]protocol.PublishDiagnosticsParams, error) {
	m.diagnosticsContext = ctx
	return nil, nil
}

func (m *registryDiagnosticsManager) WaitDiagnosticsStable(ctx context.Context, _ []string) error {
	m.waitContext = ctx
	return nil
}

func (*registryDiagnosticsManager) CurrentDiagnosticGeneration() uint64 { return 1 }

func (*registryDiagnosticsManager) AdvanceDiagnosticGeneration() uint64 { return 2 }
