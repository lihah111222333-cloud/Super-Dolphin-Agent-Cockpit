package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
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
	toolManifestWithSchema("file", "Recommended tool: file. Why: opens files into LSP, reads exact line ranges, and returns diagnostics. Use action=open_file first before diagnostics or other stateful file operations. Example read_file: action=read_file file_path=internal/foo.go offset=42 limit=40.", lspFileSchema),
	toolManifestWithSchema("inspect", "Recommended tool: inspect. Why: resolves hover, definitions, implementations, type definitions, and signatures with language-server symbol knowledge. Use file action=open_file first when the target file may not already be open. Example definition: action=definition pos=internal/foo.go:42:9.", lspInspectSchema),
	toolManifestWithSchema("xref", "Recommended tool: xref. Why: finds references, call hierarchy, and type hierarchy with language-server relationships. Use file action=open_file first when the target file may not already be open. Example references: action=references pos=internal/foo.go:42:9.", lspXrefSchema),
	toolManifestWithOutputSchema("grep", "Recommended tool: grep. Why: searches the codebase by text, regex, or AST before jumping into exact symbols. Example text search: action=text_search query=targetName path=internal glob=*.go.", lspGrepSchema, lspGrepOutputSchema),
	toolManifestWithSchema("structure", "Recommended tool: structure. Why: lists document/workspace symbols, folding ranges, and semantic tokens for code navigation. Use file action=open_file first before file-scoped structure actions when the target file may not already be open. Example document_symbol: action=document_symbol file_path=internal/foo.go.", lspStructureSchema),
	toolManifestWithSchema("edit", "Recommended tool: edit. Why: patches disk and syncs the language server so diagnostics and later reads see the change. Use file action=open_file first before editing a file that may not already be open. Example: file_path=internal/foo.go patch=\"*** Begin Patch...\".", lspEditSchema),
	toolManifestWithSchema("completion", "Recommended tool: completion. Why: asks the language server for context-aware code completions at a position. Use file action=open_file first when the target file may not already be open. Example: pos=internal/foo.go:42:9.", lspCompletionSchema),
	toolManifestWithSchema("code_run", "Recommended tool: code_run. Why: runs project commands or snippets only when no host exec_command is available and no dedicated LSP tool covers the job. Example project_cmd: mode=project_cmd command=\"go test ./cmd/mcp-lsp\".", codeRunSchema),
	toolManifestWithSchema("code_run_test", "Recommended tool: code_run_test. Why: runs one Go test function after LSP diagnostics or targeted investigation. Example: test_func=TestName test_pkg=./cmd/mcp-lsp.", codeRunTestSchema),
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
	codeRunH, err := tools.NewCodeRunHandler(m.root)
	if err != nil {
		return nil, fmt.Errorf("code_run handler: %w", err)
	}
	codeRunTestH, err := tools.NewCodeRunTestHandler(m.root)
	if err != nil {
		return nil, fmt.Errorf("code_run_test handler: %w", err)
	}
	return ToolHandlers{
		"file":          ToolHandler(tools.NewFileHandler(cfg)),
		"inspect":       ToolHandler(tools.NewInspectHandler(m.registry)),
		"xref":          ToolHandler(tools.NewXRefHandler(m.registry)),
		"grep":          ToolHandler(tools.NewGrepHandler(cfg)),
		"structure":     ToolHandler(tools.NewStructureHandler(m.registry)),
		"edit":          ToolHandler(tools.NewEditHandlerWithRoot(m.root, m.registry)),
		"completion":    ToolHandler(tools.NewCompletionHandler(m.registry)),
		"code_run":      ToolHandler(codeRunH),
		"code_run_test": ToolHandler(codeRunTestH),
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
