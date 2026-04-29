package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/pkg/skilltool"
)

// HostToolRegistry 暴露由 host 进程**直接执行**（不走 mcp-orch / mcp-lsp peer）
// 的工具集合。p20.18 第一个使用方是 SkillHostTools，把 skill_expand_body /
// skill_read_resource 接到 toolbridge 上。
//
// nil HostToolRegistry 等价于 "no host-direct tools"：所有 ListToolsForCodex /
// routeToolCall 调用必须 nil-safe，保证 mcp-orch standalone 模式（skill module
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
	ListHostTools() []common.MCPTool
	// HasTool 判断给定工具名是否由本 registry 处理。routeToolCall 用它做分支
	// 决策，避免把 skill_* 工具误投到 peer。
	HasTool(name string) bool
	// CallHostTool 同进程执行工具调用。cwd 由 Handler 从 thread context 解析后注入，
	// 不暴露给模型；arguments 是模型填的 JSON，只允许 schema 定义的字段。
	CallHostTool(ctx context.Context, call HostToolCall) (any, error)
}

// SkillHostTools 把 skill.SkillHostToolReader 的 ExpandBody / ReadResource 包装成两个 host-direct
// 工具：skill_expand_body / skill_read_resource。schema 来自 pkg/skilltool（避免 schema
// 在 toolbridge / claudecli 两边漂移）。
//
// NOTE: SkillHostTools is kept for use by existing tests and the legacy skill module.
// The Codex-facing DynamicTools list no longer exposes skill_expand_body / skill_read_resource;
// it now exposes skill_read_section via SkillReadSectionRegistry (see below).
type SkillHostTools struct {
	svc skillpkg.SkillHostToolReader
}

// NewSkillHostTools 构造函数。svc 为 nil 时返回 nil，由 fx 注入后调用方自行 nil-check。
func NewSkillHostTools(svc skillpkg.SkillHostToolReader) *SkillHostTools {
	if svc == nil {
		return nil
	}
	return &SkillHostTools{svc: svc}
}

// ensure interface compliance.
var _ HostToolRegistry = (*SkillHostTools)(nil)

// ListHostTools 返回两个 skill 工具的 MCPTool schema。
func (s *SkillHostTools) ListHostTools() []common.MCPTool {
	if s == nil {
		return nil
	}
	expandSchema, _ := json.Marshal(skilltool.ExpandBodyInputSchema())
	readSchema, _ := json.Marshal(skilltool.ReadResourceInputSchema())
	return []common.MCPTool{
		{
			Name:        skilltool.ToolNameExpandBody,
			Description: skilltool.DescriptionExpandBody,
			InputSchema: expandSchema,
		},
		{
			Name:        skilltool.ToolNameReadResource,
			Description: skilltool.DescriptionReadResource,
			InputSchema: readSchema,
		},
	}
}

// HasTool 仅命中本 registry 已注册的工具名。其他名字应让 Handler 走 peer 路径。
func (s *SkillHostTools) HasTool(name string) bool {
	if s == nil {
		return false
	}
	switch name {
	case skilltool.ToolNameExpandBody, skilltool.ToolNameReadResource:
		return true
	}
	return false
}

// CallHostTool 把工具调用分派给 skill.Service 的对应方法。
//
// 注入 cwd：调用方（Handler）必须传入已解析的 cwd（通常通过 WorkDirResolver
// 从 agentID 拿到），本函数把 cwd 包进 ctx（skillpkg.WithCWD）和 params 字段，
// 双轨保证：service 实现既支持 ctx 注入也支持 params 字段（见 skills_expand.go）。
//
// 错误语义：未知工具名 → fmt.Errorf("unknown host tool")；arguments 解析失败
// 直接返回 json 错误；service 错误（含审批拒绝、skill 不存在等）原样上抛。
func (s *SkillHostTools) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if s == nil || s.svc == nil {
		return nil, fmt.Errorf("host tools: skill service not configured")
	}
	scopedCtx := skillpkg.WithCWD(ctx, call.CWD)
	switch call.Name {
	case skilltool.ToolNameExpandBody:
		var p skillpkg.ExpandBodyParams
		if err := decodeArgs(call.Arguments, &p); err != nil {
			return nil, err
		}
		// 强制以 host 解析的 cwd 覆盖 model 提供的字段（防御性：模型不应也无法填 cwd，
		// 但 schema 之外的伪造仍可能携带，这里强制清零再重设）。
		p.CWD = call.CWD
		p.AgentID = call.AgentID
		p.ThreadID = call.ThreadID
		p.TurnID = call.TurnID
		p.CallID = call.CallID
		return s.svc.ExpandBody(scopedCtx, p)
	case skilltool.ToolNameReadResource:
		var p skillpkg.ReadResourceParams
		if err := decodeArgs(call.Arguments, &p); err != nil {
			return nil, err
		}
		p.CWD = call.CWD
		p.AgentID = call.AgentID
		p.ThreadID = call.ThreadID
		p.TurnID = call.TurnID
		p.CallID = call.CallID
		return s.svc.ReadResource(scopedCtx, p)
	}
	return nil, fmt.Errorf("host tools: unknown tool %q", call.Name)
}

// SkillReadSectionRegistry implements HostToolRegistry exposing only
// skill_read_section to the Codex-facing DynamicTools list.
//
// This replaces the previous SkillHostTools-based registration for Codex.
// skill_expand_body and skill_read_resource are no longer listed here;
// they remain functional via SkillHostTools for legacy usage.
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
func (r *SkillReadSectionRegistry) ListHostTools() []common.MCPTool {
	if r == nil {
		return nil
	}
	schema, _ := json.Marshal(skillReadSectionInputSchema())
	return []common.MCPTool{
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
	Body string `json:"body"`
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
	raw, err := r.tool.Call(ctx, call.Arguments)
	if err != nil {
		return nil, err
	}
	return SkillReadSectionResult{Body: string(raw)}, nil
}

// decodeArgs 把 JSON 参数解码到目标结构。空 / null arguments 视为空 object。
func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("host tools: decode arguments: %w", err)
	}
	return nil
}
