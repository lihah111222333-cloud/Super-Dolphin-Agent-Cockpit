package lspgui

import (
	"context"
	"errors"
	"strings"
)

const stubStatusNotImplemented = "not_implemented"

func stubSearchResult() searchResult {
	return searchResult{
		Results: []searchMatch{},
		Status:  stubStatusNotImplemented,
		Stub:    true,
	}
}

func (s *service) HandleStructure(_ context.Context, p structureParams) (any, error) {
	switch strings.TrimSpace(p.Action) {
	case "document_symbol", "workspace_symbol":
		return symbolsResult{Symbols: []any{}, Status: stubStatusNotImplemented, Stub: true}, nil
	default:
		return nil, errors.New("unsupported lsp/gui_structure action")
	}
}

func (s *service) HandleInspect(_ context.Context, p inspectParams) (any, error) {
	switch strings.TrimSpace(p.Action) {
	case "hover":
		return hoverResult{Contents: "", Status: stubStatusNotImplemented, Stub: true}, nil
	case "definition", "implementation", "type_definition", "signature_help":
		return stubSearchResult(), nil
	default:
		return nil, errors.New("unsupported lsp/gui_inspect action")
	}
}

func (s *service) HandleXref(_ context.Context, p xrefParams) (any, error) {
	switch strings.TrimSpace(p.Action) {
	case "references":
		return referencesResult{References: []searchMatch{}, Status: stubStatusNotImplemented, Stub: true}, nil
	case "call_hierarchy", "type_hierarchy":
		return stubSearchResult(), nil
	default:
		return nil, errors.New("unsupported lsp/gui_xref action")
	}
}
