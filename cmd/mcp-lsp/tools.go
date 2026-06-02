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
	toolManifestWithSchema("file", "Open files into LSP, read line ranges, and fetch LSP/type diagnostics. Run package scripts such as npm run lint with host exec_command. Pass action=open_file before stateful actions on a fresh file. Example: action=read_file pos=internal/foo.go:42 limit=40.", lspFileSchema),
	toolManifestWithSchema("inspect", "Resolve hover, definition, implementation, type_definition, signature_help at a position. Example: action=definition pos=internal/foo.go:42:9.", lspInspectSchema),
	toolManifestWithSchema("xref", "References / call_hierarchy / type_hierarchy at a position. Example: action=references pos=internal/foo.go:42:9.", lspXrefSchema),
	toolManifestWithOutputSchema("grep", "Codebase text or AST search; use before jumping to symbols. Example: action=text_search query=targetName path=internal glob=*.go.", lspGrepSchema, lspGrepOutputSchema),
	toolManifestWithSchema("structure", "Document/workspace symbol outline. Example: action=document_symbol file_path=internal/foo.go.", lspStructureSchema),
	toolManifestWithSchema("edit", "Patch file contents and resync the language server so diagnostics reflect the change. Patch body lines start with ' '=context, '-'=remove, '+'=add (blank context = single space, not empty). Pure-insertion hunks are rejected; add a ' ' context line and a '+' line. Example minimal patch: \" import \\\"fmt\\\"\\n-x := 1\\n+x := 2\\n y := 3\".", lspEditSchema),
	toolManifestWithSchema("completion", "Context-aware code completions at a position. Example: pos=internal/foo.go:42:9.", lspCompletionSchema),
	toolManifestWithSchema("code_run", "Run a project command or short snippet (Go/JS/TS) only when no host exec_command is available; do not use for package scripts such as npm run lint when exec_command exists. Example: mode=project_cmd command=\"go test ./cmd/mcp-lsp\".", codeRunSchema),
	toolManifestWithSchema("code_run_test", "Run one Go test function. Example: test_func=TestName test_pkg=./cmd/mcp-lsp.", codeRunTestSchema),
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
