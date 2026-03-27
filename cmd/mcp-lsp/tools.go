package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/tools"
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

func newToolHandlers(m *Manager) (ToolHandlers, error) {
	cfg := tools.Config{
		WorkspaceRoot: m.root,
		Manager:       m.goplsMgr,
	}
	codeRunH, err := tools.NewCodeRunHandler(m.root)
	if err != nil {
		return nil, fmt.Errorf("code_run handler: %w", err)
	}
	codeRunTestH, err := tools.NewCodeRunTestHandler(m.root)
	if err != nil {
		return nil, fmt.Errorf("code_run_test handler: %w", err)
	}
	return ToolHandlers{
		"lsp_file":       ToolHandler(tools.NewFileHandler(cfg)),
		"lsp_inspect":    ToolHandler(tools.NewInspectHandler(m.goplsMgr)),
		"lsp_xref":       ToolHandler(tools.NewXRefHandler(m.goplsMgr)),
		"lsp_grep":       ToolHandler(tools.NewGrepHandler(cfg)),
		"lsp_structure":  ToolHandler(tools.NewStructureHandler(m.goplsMgr)),
		"lsp_edit":       ToolHandler(tools.NewEditHandler(m.goplsMgr)),
		"lsp_completion": ToolHandler(tools.NewCompletionHandler(m.goplsMgr)),
		"code_run":       ToolHandler(codeRunH),
		"code_run_test":  ToolHandler(codeRunTestH),
	}, nil
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
