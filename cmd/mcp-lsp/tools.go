// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
)

// ToolHandler 是单个工具的处理函数类型。
type ToolHandler func(ctx context.Context, params json.RawMessage) (any, error)

// ToolHandlers 按工具名索引的处理器映射。
type ToolHandlers map[string]ToolHandler

// ToolManifest 描述单个工具的名称、说明和输入/输出 schema。
type ToolManifest struct {
	Name         string
	Description  string
	Schema       map[string]any
	OutputSchema map[string]any
}

// toolDefinition 将工具清单和处理器绑定在一起。
type toolDefinition struct {
	Manifest ToolManifest
	Handler  ToolHandler
}

// lspToolManifests 是所有 LSP 工具的清单列表，顺序与对外暴露顺序一致。
var lspToolManifests = []ToolManifest{
	toolManifestWithSchema("file", "Read files, open them into LSP, or fetch diagnostics. Example: action=read_file pos=internal/foo.go:42 limit=40.", lspFileSchema),
	toolManifestWithSchema("inspect", "Hover, definition, implementation, type_definition, or signature_help at a position. Example: action=definition pos=internal/foo.go:42:9.", lspInspectSchema),
	toolManifestWithSchema("xref", "Find references, call hierarchy, or type hierarchy. Example: action=references pos=internal/foo.go:42:9.", lspXrefSchema),
	toolManifestWithOutputSchema("grep", "Search codebase by text or AST pattern. Example: action=text_search query=targetName path=internal glob=*.go.", lspGrepSchema, lspGrepOutputSchema),
	toolManifestWithSchema("structure", "List document or workspace symbols. Example: action=document_symbol file_path=internal/foo.go.", lspStructureSchema),
	toolManifestWithSchema("edit", "Apply patch edits, LSP rename, code actions, or format. Pure insertion: context (' ') + add ('+') lines only. Example: action=replace_range file_path=internal/foo.go patch=\" import (\\n+\\t\\\"fmt\\\"\\n )\".", lspEditSchema),
	toolManifestWithSchema("completion", "Context-aware code completions at a cursor position. Example: pos=internal/foo.go:42:9.", lspCompletionSchema),
}

// legacyToolAliases 旧版工具名到规范名的映射，保持向后兼容。
var legacyToolAliases = map[string]string{
	"lsp_file":       "file",
	"lsp_inspect":    "inspect",
	"lsp_xref":       "xref",
	"lsp_grep":       "grep",
	"lsp_structure":  "structure",
	"lsp_edit":       "edit",
	"lsp_completion": "completion",
}

// canonicalToolName 返回工具名称的规范形式，处理历史别名映射。
func canonicalToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if alias, ok := legacyToolAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

// newToolHandlers 根据 Manager 构建所有工具的处理器映射。
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

// toolDefinitions 将工具清单列表与处理器映射合并为 toolDefinition 切片，缺少处理器时使用 stub。
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

// toolManifestWithSchema 创建带输入 schema 的工具清单。
func toolManifestWithSchema(name, description string, s schema) ToolManifest {
	return ToolManifest{
		Name:        name,
		Description: description,
		Schema:      map[string]any(s),
	}
}

// toolManifestWithOutputSchema 创建带输入和输出 schema 的工具清单。
func toolManifestWithOutputSchema(name, description string, s schema, out schema) ToolManifest {
	return ToolManifest{
		Name:         name,
		Description:  description,
		Schema:       map[string]any(s),
		OutputSchema: map[string]any(out),
	}
}

// stubToolHandler 是未实现工具的占位处理器，始终返回 not implemented 错误。
func stubToolHandler(context.Context, json.RawMessage) (any, error) {
	return nil, errors.New("not implemented")
}
