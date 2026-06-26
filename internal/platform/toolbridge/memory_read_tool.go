package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const ToolNameMemoryRead = "memory_read"

// MemoryReadHostToolOptions 控制 memory_read 是否可见、是否可调用。
type MemoryReadHostToolOptions struct {
	Enabled      bool
	ToolsEnabled bool
}

// MemoryReadHostToolRegistry 将 memory_read 暴露为 host-direct 工具。
type MemoryReadHostToolRegistry struct {
	reader contract.AgentMemoryReader
	opts   MemoryReadHostToolOptions
}

// memoryReadToolInput 是模型侧 memory_read 参数。
type memoryReadToolInput struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Scope string `json:"scope,omitempty"`
	Type  string `json:"type,omitempty"`
}

// NewMemoryReadHostToolRegistry 创建 memory_read host-direct registry。
func NewMemoryReadHostToolRegistry(reader contract.AgentMemoryReader, opts MemoryReadHostToolOptions) *MemoryReadHostToolRegistry {
	if reader == nil {
		return nil
	}
	return &MemoryReadHostToolRegistry{reader: reader, opts: opts}
}

// ListHostTools 在能力与工具开关均启用时暴露 memory_read schema。
func (r *MemoryReadHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.reader == nil || !r.opts.Enabled || !r.opts.ToolsEnabled {
		return nil
	}
	schema, _ := json.Marshal(memoryReadInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameMemoryRead, Description: descriptionMemoryRead, InputSchema: schema}}
}

// HasTool 判断当前 registry 是否负责该工具名；可见性由 ListHostTools 单独控制。
func (r *MemoryReadHostToolRegistry) HasTool(name string) bool {
	return r != nil && name == ToolNameMemoryRead
}

// CallHostTool 校验开关、解析输入并调用内存读取端口。
func (r *MemoryReadHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil || r.reader == nil {
		return nil, contract.NewAgentMemoryError("reader_unavailable", fmt.Errorf("memory_read reader is not configured"))
	}
	if err := validateHostToolGuards(r.opts.Enabled, r.opts.ToolsEnabled, call.Name, ToolNameMemoryRead, "reader_unavailable"); err != nil {
		return nil, err
	}
	var input memoryReadToolInput
	if err := platformshared.DecodeInput(call.Arguments, &input); err != nil {
		return nil, contract.NewAgentMemoryError("invalid_input", err)
	}
	req, err := buildAgentMemoryReadRequest(input, call)
	if err != nil {
		return nil, err
	}
	return r.reader.ReadAgentMemory(ctx, req)
}

// buildAgentMemoryReadRequest 将模型输入转换为 memory read 请求，并带上调用上下文。
func buildAgentMemoryReadRequest(input memoryReadToolInput, call HostToolCall) (contract.MemoryReadRequest, error) {
	scope, err := parseMemoryReadScope(input.Scope)
	if err != nil {
		return contract.MemoryReadRequest{}, err
	}
	memType, err := parseMemoryReadType(input.Type)
	if err != nil {
		return contract.MemoryReadRequest{}, err
	}
	return contract.MemoryReadRequest{
		Name:     strings.TrimSpace(input.Name),
		Path:     strings.TrimSpace(input.Path),
		Scope:    scope,
		Type:     memType,
		AgentID:  strings.TrimSpace(call.AgentID),
		ThreadID: strings.TrimSpace(call.ThreadID),
		CWD:      strings.TrimSpace(call.CWD),
		CallID:   strings.TrimSpace(call.CallID),
	}, nil
}

// parseMemoryReadScope 校验 memory_read scope。
func parseMemoryReadScope(raw string) (contract.MemoryScope, error) {
	scope := contract.ParseMemoryScope(raw)
	if !scope.Valid() {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("invalid memory_read scope"))
	}
	return scope, nil
}

// parseMemoryReadType 校验可选 memory type；空值表示不按类型过滤。
func parseMemoryReadType(raw string) (contract.MemoryType, error) {
	trimmed := strings.TrimSpace(raw)
	memType := contract.ParseMemoryType(trimmed)
	if trimmed != "" && !memType.IsKnown() {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("invalid memory_read type"))
	}
	return memType, nil
}

// memoryReadInputSchema 描述模型可传入的 memory_read 参数。
func memoryReadInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"path":  map[string]any{"type": "string"},
			"scope": map[string]any{"type": "string", "enum": []string{"user", "team"}},
			"type":  map[string]any{"type": "string", "enum": []string{"user", "feedback", "project", "reference"}},
		},
		"additionalProperties": false,
	}
}

const descriptionMemoryRead = "Read a durable memory entry by name or relative path. Uses host-direct memory roots and never falls back to mcp-orch peer memory tools."
