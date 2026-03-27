package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type ToolHandler func(ctx context.Context, params json.RawMessage) (any, error)

type ToolHandlers map[string]ToolHandler

type toolDefinition struct {
	Manifest common.ToolManifest
	Handler  ToolHandler
}

var lspToolManifests = []common.ToolManifest{
	toolManifest("lsp_file", "Read files and diagnostics via LSP"),
	toolManifest("lsp_inspect", "Inspect symbols and definitions via LSP"),
	toolManifest("lsp_xref", "Query references and hierarchies via LSP"),
	toolManifest("lsp_grep", "Search text and AST patterns via LSP"),
	toolManifest("lsp_structure", "Inspect document and workspace structure via LSP"),
	toolManifest("lsp_edit", "Apply semantic edits via LSP"),
	toolManifest("lsp_completion", "Request code completions via LSP"),
	toolManifest("code_run", "Run project commands and snippets"),
	toolManifest("code_run_test", "Run focused project tests"),
}

func newToolHandlers(*Manager) ToolHandlers {
	return ToolHandlers{
		"lsp_file":       stubToolHandler,
		"lsp_inspect":    stubToolHandler,
		"lsp_xref":       stubToolHandler,
		"lsp_grep":       stubToolHandler,
		"lsp_structure":  stubToolHandler,
		"lsp_edit":       stubToolHandler,
		"lsp_completion": stubToolHandler,
		"code_run":       stubToolHandler,
		"code_run_test":  stubToolHandler,
	}
}

func toolDefinitions(handlers ToolHandlers) []toolDefinition {
	defs := make([]toolDefinition, 0, len(lspToolManifests))
	for _, manifest := range lspToolManifests {
		handler := handlers[manifest.Name]
		if handler == nil {
			handler = stubToolHandler
		}
		defs = append(defs, toolDefinition{Manifest: manifest, Handler: handler})
	}
	return defs
}

func toolManifest(name, description string) common.ToolManifest {
	return common.ToolManifest{
		Name:        name,
		Description: description,
		Schema:      map[string]any{"type": "object"},
	}
}

func stubToolHandler(context.Context, json.RawMessage) (any, error) {
	return nil, errors.New("not implemented")
}
