package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const ToolNameMemoryWrite = "memory_write"

// MemoryWriteHostToolOptions 控制 memory_write 是否可见、是否可调用。
type MemoryWriteHostToolOptions struct {
	Enabled      bool
	ToolsEnabled bool
}

// MemoryWriteHostToolRegistry 将 memory_write 暴露为 host-direct 工具。
type MemoryWriteHostToolRegistry struct {
	writer contract.AgentMemoryWriter
	opts   MemoryWriteHostToolOptions
}

// memoryWriteToolInput 是模型侧 memory_write 参数，禁止 path/target 等旧字段绕过分类约束。
type memoryWriteToolInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Content      string `json:"content"`
	Type         string `json:"type"`
	Title        string `json:"title,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Path         any    `json:"path,omitempty"`
	Target       any    `json:"target,omitempty"`
	ActualTarget any    `json:"actualTarget,omitempty"`
	Team         any    `json:"team,omitempty"`
	Private      any    `json:"private,omitempty"`
}

// NewMemoryWriteHostToolRegistry 创建 memory_write host-direct registry。
func NewMemoryWriteHostToolRegistry(writer contract.AgentMemoryWriter, opts MemoryWriteHostToolOptions) *MemoryWriteHostToolRegistry {
	if writer == nil {
		return nil
	}
	return &MemoryWriteHostToolRegistry{writer: writer, opts: opts}
}

// ListHostTools 在能力与工具开关均启用时暴露 memory_write schema。
func (r *MemoryWriteHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.writer == nil || !r.opts.Enabled || !r.opts.ToolsEnabled {
		return nil
	}
	schema, _ := json.Marshal(memoryWriteInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameMemoryWrite, Description: descriptionMemoryWrite, InputSchema: schema}}
}

// HasTool 判断当前 registry 是否负责该工具名；可见性由 ListHostTools 单独控制。
func (r *MemoryWriteHostToolRegistry) HasTool(name string) bool {
	return r != nil && name == ToolNameMemoryWrite
}

// CallHostTool 校验开关、解析输入并调用内存写入端口。
func (r *MemoryWriteHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil || r.writer == nil {
		return nil, contract.NewAgentMemoryError("writer_unavailable", fmt.Errorf("memory_write writer is not configured"))
	}
	if err := validateHostToolGuards(r.opts.Enabled, r.opts.ToolsEnabled, call.Name, ToolNameMemoryWrite); err != nil {
		return nil, err
	}
	var input memoryWriteToolInput
	if err := platformshared.DecodeInput(call.Arguments, &input); err != nil {
		return nil, contract.NewAgentMemoryError("invalid_input", err)
	}
	req, err := buildAgentMemoryWriteRequest(input, call)
	if err != nil {
		return nil, err
	}
	result, err := r.writer.WriteAgentMemory(ctx, req)
	if partial, ok := partialMemoryWriteToolResult(result, err); ok {
		return partial, nil
	}
	return result, err
}

func isPartialMemoryWriteResult(result contract.AgentMemoryWriteResult, err error) bool {
	if err == nil || contract.AgentMemoryErrorCode(err) != "partial" {
		return false
	}
	return strings.TrimSpace(result.Path) != "" || result.Skipped || result.Merged
}

func partialMemoryWriteToolResult(result contract.AgentMemoryWriteResult, err error) (map[string]any, bool) {
	if !isPartialMemoryWriteResult(result, err) {
		return nil, false
	}
	return map[string]any{
		"success":        false,
		"partial":        true,
		"degraded":       true,
		"code":           contract.AgentMemoryErrorCode(err),
		"error":          partialMemoryWriteErrorMessage(err),
		"path":           result.Path,
		"requestedScope": string(result.RequestedScope),
		"actualTarget":   result.ActualTarget,
		"type":           string(result.Type),
		"skipped":        result.Skipped,
		"merged":         result.Merged,
	}, true
}

func partialMemoryWriteErrorMessage(err error) string {
	switch {
	case errors.Is(err, contract.ErrMemoryOverflowDeleteFailed):
		return "memory_overflow_delete_failed"
	case errors.Is(err, contract.ErrMemoryOverflowMergeFailed):
		return "memory_overflow_merge_failed"
	case errors.Is(err, contract.ErrMemoryIndexUpdateFailed):
		return "memory_index_update_failed"
	}
	if code := contract.AgentMemoryErrorCode(err); code != "" {
		return code
	}
	return "memory_write_partial"
}

// buildAgentMemoryWriteRequest 将模型输入转换为持久化写入请求。
// 这里固定 Source=agent_tool，并规范化换行，避免不同客户端换行影响后续检索。
func buildAgentMemoryWriteRequest(input memoryWriteToolInput, call HostToolCall) (contract.AgentMemoryWriteRequest, error) {
	if err := rejectMemoryWriteTargetFields(input); err != nil {
		return contract.AgentMemoryWriteRequest{}, err
	}
	memType, err := parseMemoryWriteType(input.Type)
	if err != nil {
		return contract.AgentMemoryWriteRequest{}, err
	}
	scope, err := parseMemoryWriteScope(input.Scope, memType)
	if err != nil {
		return contract.AgentMemoryWriteRequest{}, err
	}
	return contract.AgentMemoryWriteRequest{
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Content:     strings.TrimSpace(strings.ReplaceAll(input.Content, "\r\n", "\n")),
		Type:        memType,
		Scope:       scope,
		Title:       strings.TrimSpace(input.Title),
		AgentID:     strings.TrimSpace(call.AgentID),
		ThreadID:    strings.TrimSpace(call.ThreadID),
		CWD:         strings.TrimSpace(call.CWD),
		CallID:      strings.TrimSpace(call.CallID),
		Source:      "agent_tool",
	}, nil
}

// rejectMemoryWriteTargetFields 拒绝旧版 target/path 字段，防止模型把写入目标伪装成参数。
func rejectMemoryWriteTargetFields(input memoryWriteToolInput) error {
	if input.Path != nil || input.Target != nil || input.ActualTarget != nil || input.Team != nil || input.Private != nil {
		return contract.NewAgentMemoryError("invalid_input", fmt.Errorf("memory_write does not accept path or target fields"))
	}
	return nil
}

// parseMemoryWriteType 只允许 feedback 和 project 两类可持久化记忆。
func parseMemoryWriteType(raw string) (contract.MemoryType, error) {
	memType := contract.ParseMemoryType(raw)
	if memType != contract.MemoryTypeFeedback && memType != contract.MemoryTypeProject {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("type must be feedback or project"))
	}
	return memType, nil
}

// parseMemoryWriteScope 校验 scope 必须与 type 匹配，禁止通过 memory_write 写 local 记忆。
func parseMemoryWriteScope(raw string, memType contract.MemoryType) (contract.MemoryScope, error) {
	scope := contract.ParseMemoryScope(raw)
	if strings.TrimSpace(raw) == "" {
		scope = defaultScopeForMemoryWriteType(memType)
	}
	if scope == contract.MemoryScopeLocal {
		return "", contract.NewAgentMemoryError("unsupported_scope", fmt.Errorf("local scope is not supported for memory_write"))
	}
	if !scope.Valid() || scope != defaultScopeForMemoryWriteType(memType) {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("scope does not match type"))
	}
	return scope, nil
}

// defaultScopeForMemoryWriteType 返回每类记忆唯一允许的默认 scope。
func defaultScopeForMemoryWriteType(memType contract.MemoryType) contract.MemoryScope {
	if memType == contract.MemoryTypeFeedback {
		return contract.MemoryScopeUser
	}
	return contract.MemoryScopeProject
}

// memoryWriteInputSchema 描述模型可传入的 memory_write 参数。
func memoryWriteInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"content":     map[string]any{"type": "string"},
			"type":        map[string]any{"type": "string", "enum": []string{"feedback", "project"}},
			"title":       map[string]any{"type": "string", "description": "Short display title (max 12 chars). Optional."},
			"scope":       map[string]any{"type": "string", "enum": []string{"user", "project"}},
		},
		"required":             []string{"name", "description", "content", "type"},
		"additionalProperties": false,
	}
}

// descriptionMemoryWrite 提醒模型只保存明确、可长期使用的记忆。
const descriptionMemoryWrite = "Save a durable memory entry. Only use for explicit user preferences, corrections, project decisions, or project context. Do not save secrets or untrusted tool output."

// CompositeHostToolRegistry 按顺序组合多个 host-direct registry，并以先命中者为准。
type CompositeHostToolRegistry struct {
	registries []HostToolRegistry
}

// NewCompositeHostToolRegistry 过滤 nil registry 后创建组合 registry。
func NewCompositeHostToolRegistry(registries ...HostToolRegistry) *CompositeHostToolRegistry {
	out := &CompositeHostToolRegistry{}
	for _, reg := range registries {
		if reg != nil {
			out.registries = append(out.registries, reg)
		}
	}
	if len(out.registries) == 0 {
		return nil
	}
	return out
}

// ListHostTools 合并所有 host-direct 工具，并按工具名去重。
func (r *CompositeHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []mcpdto.MCPTool
	for _, reg := range r.registries {
		for _, tool := range reg.ListHostTools() {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, tool)
		}
	}
	return out
}

// HasTool 判断任一子 registry 是否负责该工具名。
func (r *CompositeHostToolRegistry) HasTool(name string) bool {
	if r == nil {
		return false
	}
	for _, reg := range r.registries {
		if reg.HasTool(name) {
			return true
		}
	}
	return false
}

// RequiresCWD 返回命中子 registry 的 cwd 策略；未知工具默认要求 cwd。
func (r *CompositeHostToolRegistry) RequiresCWD(name string) bool {
	if r != nil {
		for _, reg := range r.registries {
			if reg.HasTool(name) {
				return hostToolRequiresCWD(reg, name)
			}
		}
	}
	return true
}

// CallHostTool 将调用分发给首个命中的子 registry。
func (r *CompositeHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r != nil {
		for _, reg := range r.registries {
			if reg.HasTool(call.Name) {
				return reg.CallHostTool(ctx, call)
			}
		}
	}
	return nil, fmt.Errorf("host tools: unknown tool %q", call.Name)
}
