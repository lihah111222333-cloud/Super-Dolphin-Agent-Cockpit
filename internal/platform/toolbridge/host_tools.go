package toolbridge

import (
	"context"
	"encoding/json"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// HostToolRegistry 暴露由 host 进程**直接执行**（不走 mcp-orch / mcp-lsp peer）
// 的工具集合。当前生产装配只保留 memory_read / memory_write host-direct。
//
// nil HostToolRegistry 等价于 "no host-direct tools"：所有 ListToolsForCodex /
// routeToolCall 调用必须 nil-safe，保证 standalone 模式仍可运行。
type HostToolCall struct {
	Name      string
	Arguments json.RawMessage
	CWD       string
	AgentID   string
	ThreadID  string
	TurnID    string
	CallID    string
}

type HostToolRegistry interface {
	// ListHostTools 列出 host 直跑的工具，结果会与 peer 工具合并送给模型。
	ListHostTools() []mcpdto.MCPTool
	// HasTool 判断给定工具名是否由本 registry 处理。routeToolCall 用它做分支
	// 决策，避免把 skill_* 工具误投到 peer。
	HasTool(name string) bool
	// CallHostTool 同进程执行工具调用。cwd 由 Handler 从 thread context 解析后注入，
	// 不暴露给模型；arguments 是模型填的 JSON，只允许 schema 定义的字段。
	CallHostTool(ctx context.Context, call HostToolCall) (any, error)
}

// Removed skill tool names are kept only so stale Codex tool calls and
// shadowing MCP peers are rejected explicitly. The implementations were
// removed with the provider-native mirror cutover.
const (
	ToolNameReadSection             = "skill_read_section"
	ToolNameLegacySkillExpandBody   = "skill_expand_body"
	ToolNameLegacySkillReadResource = "skill_read_resource"
)
