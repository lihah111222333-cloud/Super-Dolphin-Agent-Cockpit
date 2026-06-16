package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/tools"
)

type ToolHandler func(ctx context.Context, params json.RawMessage) (any, error)

type ToolHandlers map[string]ToolHandler

type ToolManifest struct {
	Name         string
	Description  string
	Schema       map[string]any
	OutputSchema map[string]any
}

type toolDefinition struct {
	Manifest ToolManifest
	Handler  ToolHandler
}

var lspToolManifests = []ToolManifest{
	toolManifestWithSchema("file", "Read files, open them into LSP, or fetch diagnostics. Example: action=read_file pos=internal/foo.go:42 limit=40.", lspFileSchema),
	toolManifestWithSchema("inspect", "Hover, definition, implementation, type_definition, or signature_help at a position. Example: action=definition pos=internal/foo.go:42:9.", lspInspectSchema),
	toolManifestWithSchema("xref", "Find references, call hierarchy, or type hierarchy. Example: action=references pos=internal/foo.go:42:9.", lspXrefSchema),
	toolManifestWithOutputSchema("grep", "Search codebase by text or AST pattern. Example: action=text_search query=targetName path=internal glob=*.go.", lspGrepSchema, lspGrepOutputSchema),
	toolManifestWithSchema("structure", "List document or workspace symbols. Example: action=document_symbol file_path=internal/foo.go.", lspStructureSchema),
	toolManifestWithSchema("edit", "Apply patch edits, LSP rename, code actions, or format. Pure insertion: context (' ') + add ('+') lines only. Example: action=replace_range file_path=internal/foo.go patch=\" import (\\n+\\t\\\"fmt\\\"\\n )\".", lspEditSchema),
	toolManifestWithSchema("completion", "Context-aware code completions at a cursor position. Example: pos=internal/foo.go:42:9.", lspCompletionSchema),
}

var legacyToolAliases = map[string]string{
	"lsp_file":       "file",
	"lsp_inspect":    "inspect",
	"lsp_xref":       "xref",
	"lsp_grep":       "grep",
	"lsp_structure":  "structure",
	"lsp_edit":       "edit",
	"lsp_completion": "completion",
}

func canonicalToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if alias, ok := legacyToolAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

func newToolHandlers(m *Manager) (ToolHandlers, error) {
	cfg := tools.Config{
		WorkspaceRoot: m.root,
		Registry:      m.registry,
	}
	return ToolHandlers{
		"file":       ToolHandler(tools.NewFileHandler(cfg)),
		"inspect":    ToolHandler(tools.NewInspectHandler(m.registry)),
		"xref":       ToolHandler(tools.NewXRefHandler(m.registry)),
		"grep":       ToolHandler(tools.NewGrepHandler(cfg)),
		"structure":  ToolHandler(tools.NewStructureHandler(m.registry)),
		"edit":       ToolHandler(tools.NewEditHandlerWithRoot(m.root, m.registry)),
		"completion": ToolHandler(tools.NewCompletionHandler(m.registry)),
	}, nil
}

func toolDefinitions(handlers ToolHandlers) []toolDefinition {
	defs := make([]toolDefinition, 0, len(lspToolManifests))
	for _, manifest := range lspToolManifests {
		handler := handlers[canonicalToolName(manifest.Name)]
		if handler == nil {
			handler = stubToolHandler
		}
		defs = append(defs, toolDefinition{Manifest: manifest, Handler: handler})
	}
	return defs
}

func toolManifestWithSchema(name, description string, s schema) ToolManifest {
	return ToolManifest{
		Name:        name,
		Description: description,
		Schema:      map[string]any(s),
	}
}

func toolManifestWithOutputSchema(name, description string, s schema, out schema) ToolManifest {
	return ToolManifest{
		Name:         name,
		Description:  description,
		Schema:       map[string]any(s),
		OutputSchema: map[string]any(out),
	}
}

func stubToolHandler(context.Context, json.RawMessage) (any, error) {
	return nil, errors.New("not implemented")
}
