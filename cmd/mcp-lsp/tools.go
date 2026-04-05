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
	toolManifestWithSchema("lsp_file", "File: read_file (offset/limit paging), open_file, diagnostics. Batch: file_paths.", lspFileSchema),
	toolManifestWithSchema("lsp_inspect", "Hover/definition/implementation/type_definition/signature_help at file:line:column (1-based).", lspInspectSchema),
	toolManifestWithSchema("lsp_xref", "References/call_hierarchy/type_hierarchy. verbosity=compact(default)|full, max_results cap 50.", lspXrefSchema),
	toolManifestWithSchema("lsp_grep", "Search codebase: text_search (literal default, regex=true) or ast_search. Returns 1-based file:line:col.", lspGrepSchema),
	toolManifestWithSchema("lsp_structure", "Document/workspace symbols, folding ranges, semantic tokens.", lspStructureSchema),
	toolManifestWithSchema("lsp_edit", "Edit: rename, replace_range (single-hunk patch), code_action, format.", lspEditSchema),
	toolManifestWithSchema("lsp_completion", "Request code completions via LSP.", lspCompletionSchema),
	toolManifestWithSchema("code_run", "Execute code snippet or project shell command. mode=project_cmd for shell.", codeRunSchema),
	toolManifestWithSchema("code_run_test", "Run a specific Go test function.", codeRunTestSchema),
}

func newToolHandlers(m *Manager) (ToolHandlers, error) {
	cfg := tools.Config{
		WorkspaceRoot: m.root,
		Registry:      m.registry,
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
		"lsp_inspect":    ToolHandler(tools.NewInspectHandler(m.registry)),
		"lsp_xref":       ToolHandler(tools.NewXRefHandler(m.registry)),
		"lsp_grep":       ToolHandler(tools.NewGrepHandler(cfg)),
		"lsp_structure":  ToolHandler(tools.NewStructureHandler(m.registry)),
		"lsp_edit":       ToolHandler(tools.NewEditHandler(m.registry)),
		"lsp_completion": ToolHandler(tools.NewCompletionHandler(m.registry)),
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

func toolManifestWithSchema(name, description string, s schema) common.ToolManifest {
	return common.ToolManifest{
		Name:        name,
		Description: description,
		Schema:      map[string]any(s),
	}
}

func stubToolHandler(context.Context, json.RawMessage) (any, error) {
	return nil, errors.New("not implemented")
}
