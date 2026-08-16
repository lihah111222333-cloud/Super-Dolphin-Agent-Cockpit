// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/tools"
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

// newLSPToolManifests 创建所有 LSP 工具的清单列表，顺序与对外暴露顺序一致。
func newLSPToolManifests() []ToolManifest {
	return []ToolManifest{
		toolManifestWithSchema("file", `Read files, open them into LSP, or fetch diagnostics. Diagnostics are an action on this tool, not a separate tool. Examples: {"action":"read_file","pos":"internal/foo.go:42","limit":40}; {"action":"diagnostics","file_path":"internal/foo.go"}.`, newLSPFileSchema()),
		toolManifestWithSchema("inspect", `Hover, definition, implementation, type_definition, or signature_help at a position. Example: {"action":"definition","pos":"internal/foo.go:42:9"}.`, newLSPInspectSchema()),
		toolManifestWithSchema("xref", `Find references, call hierarchy, or type hierarchy. Example: {"action":"references","pos":"internal/foo.go:42:9"}.`, newLSPXrefSchema()),
		toolManifestWithOutputSchema("grep", `Search codebase by text or AST pattern. Example: {"action":"text_search","query":"targetName","paths":["internal"],"glob":"*.go"}.`, newLSPGrepSchema(), newLSPGrepOutputSchema()),
		toolManifestWithSchema("structure", `List document symbols, workspace symbols, folding ranges, or semantic tokens. Examples: {"action":"document_symbol","file_path":"internal/foo.go"}; {"action":"workspace_symbol","query":"Handler","language":"go"}.`, newLSPStructureSchema()),
		toolManifestWithSchema("patch_edit", `Apply patch edits, LSP rename, code actions, or format. replace_range supports multi-section edits; an explicit '@@' block with context lines only is an exact, read-only anchor for later changed sections. Pure insertion uses context (' ') plus added ('+') lines. Example: {"action":"format","file_path":"internal/foo.go"}.`, newPatchEditSchema()),
		toolManifestWithSchema("completion", `Context-aware code completions at a cursor position. Example: {"pos":"internal/foo.go:42:9"}.`, newLSPCompletionSchema()),
	}
}

// canonicalToolName 返回工具名称的规范形式。
func canonicalToolName(name string) string {
	return strings.TrimSpace(name)
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
		"patch_edit": ToolHandler(tools.NewEditHandlerWithRoot(m.root, m.registry)),
		"completion": ToolHandler(tools.NewCompletionHandler(m.registry)),
	}, nil
}

// toolDefinitions 将工具清单列表与处理器映射合并为 toolDefinition 切片，缺少处理器时使用 stub。
func toolDefinitions(handlers ToolHandlers) []toolDefinition {
	manifests := newLSPToolManifests()
	defs := make([]toolDefinition, 0, len(manifests))
	for _, manifest := range manifests {
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
