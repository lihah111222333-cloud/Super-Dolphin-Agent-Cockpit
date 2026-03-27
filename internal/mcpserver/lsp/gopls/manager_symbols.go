package gopls

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

func (m *manager) Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodDefinition, protocol.DefinitionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
}

func (m *manager) Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodImplementation, protocol.ImplementationParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
}

func (m *manager) TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodTypeDefinition, protocol.TypeDefinitionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
}

func (m *manager) Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("hover is unsupported for %s", ref.languageID)
	}
	raw, err := m.request(ctx, client, protocol.MethodHover, protocol.HoverParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	var result protocol.HoverResult
	if err := decodeInto(raw, &result); err != nil {
		return nil, fmt.Errorf("decode hover: %w", err)
	}
	return &result, nil
}

func (m *manager) SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("signature help is unsupported for %s", ref.languageID)
	}
	raw, err := m.request(ctx, client, protocol.MethodSignatureHelp, protocol.SignatureHelpParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	var result protocol.SignatureHelpResult
	if err := decodeInto(raw, &result); err != nil {
		return nil, fmt.Errorf("decode signature help: %w", err)
	}
	return &result, nil
}

func (m *manager) References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodReferences, protocol.ReferenceParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
		Context: protocol.ReferenceContext{
			IncludeDeclaration: includeDeclaration,
		},
	})
}

func (m *manager) CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("call hierarchy is unsupported for %s", ref.languageID)
	}
	items, err := m.prepareCallHierarchy(ctx, client, ref.uri, position)
	if err != nil {
		return nil, err
	}
	results := make([]protocol.CallHierarchyResult, 0, len(items))
	for _, item := range items {
		result := protocol.CallHierarchyResult{Item: item}
		if direction == "" || direction == "incoming" || direction == "both" {
			raw, err := m.request(ctx, client, protocol.MethodCallHierarchyIncoming, protocol.CallHierarchyIncomingCallsParams{Item: item})
			if err != nil {
				return nil, err
			}
			if err := decodeInto(raw, &result.Incoming); err != nil {
				return nil, fmt.Errorf("decode incoming hierarchy: %w", err)
			}
		}
		if direction == "" || direction == "outgoing" || direction == "both" {
			raw, err := m.request(ctx, client, protocol.MethodCallHierarchyOutgoing, protocol.CallHierarchyOutgoingCallsParams{Item: item})
			if err != nil {
				return nil, err
			}
			if err := decodeInto(raw, &result.Outgoing); err != nil {
				return nil, fmt.Errorf("decode outgoing hierarchy: %w", err)
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (m *manager) TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("type hierarchy is unsupported for %s", ref.languageID)
	}
	items, err := m.prepareTypeHierarchy(ctx, client, ref.uri, position)
	if err != nil {
		return nil, err
	}
	results := make([]protocol.TypeHierarchyResult, 0, len(items))
	for _, item := range items {
		result := protocol.TypeHierarchyResult{Item: item}
		if direction == "" || direction == "supertypes" {
			raw, err := m.request(ctx, client, protocol.MethodTypeHierarchySupertypes, protocol.TypeHierarchySupertypesParams{Item: item})
			if err != nil {
				return nil, err
			}
			if err := decodeInto(raw, &result.Supertypes); err != nil {
				return nil, fmt.Errorf("decode supertypes: %w", err)
			}
		}
		if direction == "" || direction == "subtypes" {
			raw, err := m.request(ctx, client, protocol.MethodTypeHierarchySubtypes, protocol.TypeHierarchySubtypesParams{Item: item})
			if err != nil {
				return nil, err
			}
			if err := decodeInto(raw, &result.Subtypes); err != nil {
				return nil, fmt.Errorf("decode subtypes: %w", err)
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (m *manager) DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	ref, err := m.resolveDocumentRef(uri, "")
	if err != nil {
		return nil, err
	}
	if symbols, ok, err := m.fallbackDocumentSymbols(ref); ok || err != nil {
		return symbols, err
	}
	client, _, err := m.documentClient(ctx, ref.uri)
	if err != nil {
		return nil, err
	}
	raw, err := m.request(ctx, client, protocol.MethodDocumentSymbol, protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
	})
	if err != nil {
		return nil, err
	}
	return decodeDocumentSymbols(raw)
}

func (m *manager) WorkspaceSymbol(ctx context.Context, query string) ([]protocol.WorkspaceSymbolResult, error) {
	client, err := m.ensureClientForLanguage(ctx, "go")
	if err != nil {
		return nil, err
	}
	raw, err := m.request(ctx, client, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: query})
	if err != nil {
		return nil, err
	}
	return decodeWorkspaceSymbols(raw)
}

func (m *manager) FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	raw, err := m.request(ctx, client, protocol.MethodFoldingRange, protocol.FoldingRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
	})
	if err != nil {
		return nil, err
	}
	var ranges []protocol.FoldingRange
	if err := decodeInto(raw, &ranges); err != nil {
		return nil, fmt.Errorf("decode folding ranges: %w", err)
	}
	return ranges, nil
}

func (m *manager) SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &protocol.SemanticTokensResult{}, nil
	}
	raw, err := m.request(ctx, client, protocol.MethodSemanticTokensFull, protocol.SemanticTokensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
	})
	if err != nil {
		return nil, err
	}
	var tokens protocol.SemanticTokens
	if err := decodeInto(raw, &tokens); err != nil {
		return nil, fmt.Errorf("decode semantic tokens: %w", err)
	}
	return &protocol.SemanticTokensResult{
		ResultID: tokens.ResultID,
		Data:     tokens.Data,
	}, nil
}

func (m *manager) Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &protocol.CompletionList{}, nil
	}
	raw, err := m.request(ctx, client, protocol.MethodCompletion, protocol.CompletionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	return decodeCompletionList(raw)
}

func (m *manager) Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("rename is unsupported for %s", ref.languageID)
	}
	raw, err := m.request(ctx, client, protocol.MethodRename, protocol.RenameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
		Position:     position,
		NewName:      newName,
	})
	if err != nil {
		return nil, err
	}
	var edit protocol.WorkspaceEdit
	if err := decodeInto(raw, &edit); err != nil {
		return nil, fmt.Errorf("decode rename: %w", err)
	}
	return &edit, nil
}

func (m *manager) CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	raw, err := m.request(ctx, client, protocol.MethodCodeAction, protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
		Range:        rng,
		Context:      protocol.CodeActionContext{Only: only},
	})
	if err != nil {
		return nil, err
	}
	return decodeCodeActions(raw)
}

func (m *manager) Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	raw, err := m.request(ctx, client, protocol.MethodFormatting, protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
		Options:      options,
	})
	if err != nil {
		return nil, err
	}
	var edits []protocol.TextEdit
	if err := decodeInto(raw, &edits); err != nil {
		return nil, fmt.Errorf("decode formatting edits: %w", err)
	}
	return edits, nil
}

func (m *manager) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	return m.DocumentSymbol(context.Background(), fileURIFromPath(absPath))
}

func (m *manager) locationQuery(ctx context.Context, uri, method string, params any) ([]protocol.LocationResult, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	params = normalizeLocationParams(params, ref.uri)
	raw, err := m.request(ctx, client, method, params)
	if err != nil {
		return nil, err
	}
	results, err := decodeLocationResults(raw)
	if err != nil {
		return nil, err
	}
	format.EnrichLocationResultsWithFuncRange(results, m)
	return results, nil
}

func normalizeLocationParams(params any, documentURI string) any {
	switch typed := params.(type) {
	case protocol.TextDocumentPositionParams:
		typed.TextDocument.URI = documentURI
		return typed
	case protocol.ReferenceParams:
		typed.TextDocument.URI = documentURI
		return typed
	default:
		return params
	}
}

func (m *manager) prepareCallHierarchy(ctx context.Context, client Client, uri string, position protocol.Position) ([]protocol.CallHierarchyItem, error) {
	raw, err := m.request(ctx, client, protocol.MethodPrepareCallHierarchy, protocol.PrepareCallHierarchyParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	var items []protocol.CallHierarchyItem
	if err := decodeInto(raw, &items); err != nil {
		return nil, fmt.Errorf("decode prepare call hierarchy: %w", err)
	}
	return items, nil
}

func (m *manager) prepareTypeHierarchy(ctx context.Context, client Client, uri string, position protocol.Position) ([]protocol.TypeHierarchyItem, error) {
	raw, err := m.request(ctx, client, protocol.MethodPrepareTypeHierarchy, protocol.PrepareTypeHierarchyParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	var items []protocol.TypeHierarchyItem
	if err := decodeInto(raw, &items); err != nil {
		return nil, fmt.Errorf("decode prepare type hierarchy: %w", err)
	}
	return items, nil
}
