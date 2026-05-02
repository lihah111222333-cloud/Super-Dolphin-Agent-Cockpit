package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// HostToolRegistry 暴露由 host 进程**直接执行**（不走 mcp-orch / mcp-lsp peer）
// 的工具集合。P3 后唯一实现是 SkillReadSectionRegistry，提供 skill_read_section
// 一个本进程直跑的工具；旧 SkillHostTools (skill_expand_body / skill_read_resource)
// 在 P4 Task 4 同期删除。
//
// nil HostToolRegistry 等价于 "no host-direct tools"：所有 ListToolsForCodex /
// routeToolCall 调用必须 nil-safe，保证 mcp-orch standalone 模式（skilllibrary
// 未接入）仍可运行。
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

// SkillReadSectionRegistry implements HostToolRegistry exposing only
// skill_read_section to the Codex-facing DynamicTools list. This is the
// only host-direct tool path post-P4: legacy skill_expand_body /
// skill_read_resource have been deleted (Task 4).
type SkillReadSectionRegistry struct {
	tool *SkillReadSectionTool
}

// NewSkillReadSectionRegistry wraps tool in a HostToolRegistry. Returns nil
// when tool is nil (fx optional-inject guard).
func NewSkillReadSectionRegistry(tool *SkillReadSectionTool) *SkillReadSectionRegistry {
	if tool == nil {
		return nil
	}
	return &SkillReadSectionRegistry{tool: tool}
}

// ensure interface compliance.
var _ HostToolRegistry = (*SkillReadSectionRegistry)(nil)

// skillReadSectionInputSchema returns the JSON Schema for skill_read_section.
func skillReadSectionInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name as listed in the available-skills section of the system prompt.",
			},
			"anchor": map[string]any{
				"type":        "string",
				"description": "Section anchor (slug of the Markdown H2/H3 heading) to fetch.",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"description": "Optional cap on returned body bytes. Server enforces its own ceiling.",
				"minimum":     1,
			},
		},
		"required":             []string{"name", "anchor"},
		"additionalProperties": false,
	}
}

const descriptionReadSection = "Read a reference section from an installed skill's cache. " +
	"Pass the skill `name` and the section `anchor` (slug of its Markdown H2/H3 heading). " +
	"The host reads <cacheDir>/<name>/references/<NN-anchor>.md directly without an MCP round-trip. " +
	"Optionally cap the result to `max_bytes`."

// ListHostTools returns the single skill_read_section tool schema.
func (r *SkillReadSectionRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil {
		return nil
	}
	schema, _ := json.Marshal(skillReadSectionInputSchema())
	return []mcpdto.MCPTool{
		{
			Name:        ToolNameReadSection,
			Description: descriptionReadSection,
			InputSchema: schema,
		},
	}
}

// HasTool returns true only for skill_read_section.
func (r *SkillReadSectionRegistry) HasTool(name string) bool {
	return r != nil && name == ToolNameReadSection
}

// SkillReadSectionResult is the structured return value for a skill_read_section
// host-direct call. Wrapping the raw body in a struct ensures json.Marshal in
// callHostTool succeeds regardless of the markdown file content.
type SkillReadSectionResult struct {
	Name       string `json:"name"`
	Anchor     string `json:"anchor"`
	Body       string `json:"body"`
	Truncated  bool   `json:"truncated"`
	TotalBytes int    `json:"total_bytes"`
}

// CallHostTool executes skill_read_section via SkillReadSectionTool.Call.
// CWD is not needed for cache-based reads; arguments are passed through directly.
// The raw file bytes are wrapped in SkillReadSectionResult so callHostTool can
// json.Marshal the result without requiring the markdown content to be valid JSON.
func (r *SkillReadSectionRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil || r.tool == nil {
		return nil, fmt.Errorf("host tools: skill_read_section tool not configured")
	}
	if call.Name != ToolNameReadSection {
		return nil, fmt.Errorf("host tools: unknown tool %q", call.Name)
	}
	args, err := decodeSkillReadSectionArgs(call.Arguments)
	if err != nil {
		return nil, err
	}
	result, err := r.tool.readSection(args)
	if err != nil {
		return nil, err
	}
	return SkillReadSectionResult{
		Name:       args.Name,
		Anchor:     args.Anchor,
		Body:       string(result.body),
		Truncated:  result.truncated,
		TotalBytes: result.totalBytes,
	}, nil
}

func decodeSkillReadSectionArgs(raw json.RawMessage) (skillReadSectionArgs, error) {
	var args skillReadSectionArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return skillReadSectionArgs{}, fmt.Errorf("skill_read_section: parse args: %w", err)
	}
	return args, nil
}
