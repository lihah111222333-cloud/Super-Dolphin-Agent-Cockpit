package gopls

import (
	"context"
	"encoding/json"
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
	return requestDocument(ctx, m, uri, protocol.MethodHover,
		func(ref documentRef) any {
			return protocol.HoverParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
			}
		},
		func(raw json.RawMessage) (*protocol.HoverResult, error) {
			var result protocol.HoverResult
			if err := decodeInto(raw, &result); err != nil {
				return nil, fmt.Errorf("decode hover: %w", err)
			}
			return &result, nil
		},
		unsupportedDocument[*protocol.HoverResult]("hover"),
	)
}

func (m *manager) SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodSignatureHelp,
		func(ref documentRef) any {
			return protocol.SignatureHelpParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
			}
		},
		func(raw json.RawMessage) (*protocol.SignatureHelpResult, error) {
			var result protocol.SignatureHelpResult
			if err := decodeInto(raw, &result); err != nil {
				return nil, fmt.Errorf("decode signature help: %w", err)
			}
			return &result, nil
		},
		unsupportedDocument[*protocol.SignatureHelpResult]("signature help"),
	)
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
	return queryHierarchy(ctx, m, uri, protocol.MethodPrepareCallHierarchy, position, direction,
		func(ctx context.Context, client Client, item protocol.CallHierarchyItem, direction string) (protocol.CallHierarchyResult, error) {
			return m.resolveCallDirections(ctx, client, item, direction)
		},
		unsupportedHierarchy[protocol.CallHierarchyResult]("call hierarchy"),
	)
}

func (m *manager) resolveCallDirections(ctx context.Context, client Client, item protocol.CallHierarchyItem, direction string) (protocol.CallHierarchyResult, error) {
	result := protocol.CallHierarchyResult{Item: item}
	return resolveHierarchyDirections(ctx, m, client, item, direction, result, []hierarchyDirectionStep[protocol.CallHierarchyItem, protocol.CallHierarchyResult]{
		{
			enabled: func(direction string) bool { return wantCallDirection(direction, "incoming") },
			method:  protocol.MethodCallHierarchyIncoming,
			params: func(item protocol.CallHierarchyItem) any {
				return protocol.CallHierarchyIncomingCallsParams{Item: item}
			},
			label: "incoming hierarchy",
			assign: func(result *protocol.CallHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Incoming)
			},
		},
		{
			enabled: func(direction string) bool { return wantCallDirection(direction, "outgoing") },
			method:  protocol.MethodCallHierarchyOutgoing,
			params: func(item protocol.CallHierarchyItem) any {
				return protocol.CallHierarchyOutgoingCallsParams{Item: item}
			},
			label: "outgoing hierarchy",
			assign: func(result *protocol.CallHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Outgoing)
			},
		},
	})
}

func wantCallDirection(direction, target string) bool {
	return direction == "" || direction == target || direction == "both"
}

func (m *manager) TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error) {
	return queryHierarchy(ctx, m, uri, protocol.MethodPrepareTypeHierarchy, position, direction,
		func(ctx context.Context, client Client, item protocol.TypeHierarchyItem, direction string) (protocol.TypeHierarchyResult, error) {
			return m.resolveTypeDirections(ctx, client, item, direction)
		},
		unsupportedHierarchy[protocol.TypeHierarchyResult]("type hierarchy"),
	)
}

func (m *manager) resolveTypeDirections(ctx context.Context, client Client, item protocol.TypeHierarchyItem, direction string) (protocol.TypeHierarchyResult, error) {
	result := protocol.TypeHierarchyResult{Item: item}
	return resolveHierarchyDirections(ctx, m, client, item, direction, result, []hierarchyDirectionStep[protocol.TypeHierarchyItem, protocol.TypeHierarchyResult]{
		{
			enabled: func(direction string) bool { return direction == "" || direction == "supertypes" },
			method:  protocol.MethodTypeHierarchySupertypes,
			params:  func(item protocol.TypeHierarchyItem) any { return protocol.TypeHierarchySupertypesParams{Item: item} },
			label:   "supertypes",
			assign: func(result *protocol.TypeHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Supertypes)
			},
		},
		{
			enabled: func(direction string) bool { return direction == "" || direction == "subtypes" },
			method:  protocol.MethodTypeHierarchySubtypes,
			params:  func(item protocol.TypeHierarchyItem) any { return protocol.TypeHierarchySubtypesParams{Item: item} },
			label:   "subtypes",
			assign: func(result *protocol.TypeHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Subtypes)
			},
		},
	})
}

func (m *manager) DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	ref, err := m.resolveDocumentRef(uri, "")
	if err != nil {
		return nil, err
	}
	if symbols, ok, err := m.fallbackDocumentSymbols(ref); ok || err != nil {
		return symbols, err
	}
	return requestDocument(ctx, m, ref.uri, protocol.MethodDocumentSymbol,
		func(ref documentRef) any {
			return protocol.DocumentSymbolParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
			}
		},
		decodeDocumentSymbols,
		nil,
	)
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
	return requestDocument(ctx, m, uri, protocol.MethodFoldingRange,
		func(ref documentRef) any {
			return protocol.FoldingRangeParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
			}
		},
		func(raw json.RawMessage) ([]protocol.FoldingRange, error) {
			var ranges []protocol.FoldingRange
			if err := decodeInto(raw, &ranges); err != nil {
				return nil, fmt.Errorf("decode folding ranges: %w", err)
			}
			return ranges, nil
		},
		fallbackDocument[[]protocol.FoldingRange](nil),
	)
}

func (m *manager) SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodSemanticTokensFull,
		func(ref documentRef) any {
			return protocol.SemanticTokensParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
			}
		},
		func(raw json.RawMessage) (*protocol.SemanticTokensResult, error) {
			var tokens protocol.SemanticTokens
			if err := decodeInto(raw, &tokens); err != nil {
				return nil, fmt.Errorf("decode semantic tokens: %w", err)
			}
			return &protocol.SemanticTokensResult{
				ResultID: tokens.ResultID,
				Data:     tokens.Data,
			}, nil
		},
		fallbackDocument(&protocol.SemanticTokensResult{}),
	)
}

func (m *manager) Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error) {
	return requestDocument(ctx, m, uri, protocol.MethodCompletion,
		func(ref documentRef) any {
			return protocol.CompletionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
			}
		},
		decodeCompletionList,
		fallbackDocument(&protocol.CompletionList{}),
	)
}

func (m *manager) Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	return requestDocument(ctx, m, uri, protocol.MethodRename,
		func(ref documentRef) any {
			return protocol.RenameParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
				NewName:      newName,
			}
		},
		func(raw json.RawMessage) (*protocol.WorkspaceEdit, error) {
			var edit protocol.WorkspaceEdit
			if err := decodeInto(raw, &edit); err != nil {
				return nil, fmt.Errorf("decode rename: %w", err)
			}
			return &edit, nil
		},
		unsupportedDocument[*protocol.WorkspaceEdit]("rename"),
	)
}

func (m *manager) CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodCodeAction,
		func(ref documentRef) any {
			return protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Range:        rng,
				Context:      protocol.CodeActionContext{Only: only},
			}
		},
		decodeCodeActions,
		fallbackDocument[[]protocol.CodeActionResult](nil),
	)
}

func (m *manager) Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return requestDocument(ctx, m, uri, protocol.MethodFormatting,
		func(ref documentRef) any {
			return protocol.DocumentFormattingParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Options:      options,
			}
		},
		func(raw json.RawMessage) ([]protocol.TextEdit, error) {
			var edits []protocol.TextEdit
			if err := decodeInto(raw, &edits); err != nil {
				return nil, fmt.Errorf("decode formatting edits: %w", err)
			}
			return edits, nil
		},
		fallbackDocument[[]protocol.TextEdit](nil),
	)
}

func (m *manager) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	return m.DocumentSymbol(context.Background(), fileURIFromPath(absPath))
}

func (m *manager) locationQuery(ctx context.Context, uri, method string, params any) ([]protocol.LocationResult, error) {
	return requestDocument(ctx, m, uri, method,
		func(ref documentRef) any {
			return normalizeLocationParams(params, ref.uri)
		},
		func(raw json.RawMessage) ([]protocol.LocationResult, error) {
			results, err := decodeLocationResults(raw)
			if err != nil {
				return nil, err
			}
			format.EnrichLocationResultsWithFuncRange(results, m)
			return results, nil
		},
		fallbackDocument[[]protocol.LocationResult](nil),
	)
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

func prepareHierarchy[T any](ctx context.Context, m *manager, client Client, method, uri string, position protocol.Position) ([]T, error) {
	raw, err := m.request(ctx, client, method, protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	var items []T
	if err := decodeInto(raw, &items); err != nil {
		return nil, fmt.Errorf("decode %s: %w", method, err)
	}
	return items, nil
}
