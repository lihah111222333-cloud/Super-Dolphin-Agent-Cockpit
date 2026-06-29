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
	if err := validateHostToolGuards(r.opts.Enabled, r.opts.ToolsEnabled, call.Name, ToolNameMemoryRead); err != nil {
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

const ToolNameHistoryRead = "history_read"

// HistoryReadHostToolRegistry 将当前线程历史分页读取暴露为 host-direct 工具。
// threadID 只能来自 Handler 注入的可信 metadata，模型参数不得选择任意线程。
type HistoryReadHostToolRegistry struct {
	history contract.SessionStatusPort
}

// historyReadToolInput 是模型侧 history_read 参数；只允许读取当前线程的有界分页。
type historyReadToolInput struct {
	Scope  string  `json:"scope"`
	Limit  int     `json:"limit"`
	Cursor *string `json:"cursor,omitempty"`
}

// NewHistoryReadHostToolRegistry 创建 history_read host-direct registry。
func NewHistoryReadHostToolRegistry(history contract.SessionStatusPort) *HistoryReadHostToolRegistry {
	if history == nil {
		return nil
	}
	return &HistoryReadHostToolRegistry{history: history}
}

// ListHostTools 在 session status 端口存在时暴露 history_read schema。
func (r *HistoryReadHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.history == nil {
		return nil
	}
	schema, _ := json.Marshal(historyReadInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameHistoryRead, Description: descriptionHistoryRead, InputSchema: schema}}
}

// HasTool 判断当前 registry 是否负责 history_read。
func (r *HistoryReadHostToolRegistry) HasTool(name string) bool {
	return r != nil && strings.TrimSpace(name) == ToolNameHistoryRead
}

// RequiresCWD 声明 history_read 不依赖 cwd；可信范围来自 thread metadata。
func (r *HistoryReadHostToolRegistry) RequiresCWD(name string) bool {
	return strings.TrimSpace(name) != ToolNameHistoryRead
}

// CallHostTool 校验有界输入并从当前可信 thread 读取历史。
func (r *HistoryReadHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil || r.history == nil {
		return nil, contract.NewAgentMemoryError("history_unavailable", fmt.Errorf("history_read status port is not configured"))
	}
	if strings.TrimSpace(call.Name) != ToolNameHistoryRead {
		return nil, contract.NewAgentMemoryError("invalid_input", fmt.Errorf("host tools: unknown tool %q", call.Name))
	}
	var input historyReadToolInput
	if err := platformshared.DecodeInput(call.Arguments, &input); err != nil {
		return nil, contract.NewAgentMemoryError("invalid_input", err)
	}
	threadID, limit, cursor, err := buildHistoryReadRequest(input, call)
	if err != nil {
		return nil, err
	}
	result, err := r.history.ReadMessages(ctx, threadID, limit, cursor)
	if err != nil {
		return nil, contract.NewAgentMemoryError("history_read_failed", err)
	}
	return result, nil
}

// buildHistoryReadRequest 只接受 current_thread scope，并保留显式分页边界。
func buildHistoryReadRequest(input historyReadToolInput, call HostToolCall) (string, int, string, error) {
	threadID := strings.TrimSpace(call.ThreadID)
	if threadID == "" {
		return "", 0, "", contract.NewAgentMemoryError(
			"missing_thread_id",
			fmt.Errorf("history_read requires trusted current thread metadata"),
		)
	}
	if strings.TrimSpace(input.Scope) != "current_thread" {
		return "", 0, "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("history_read scope must be current_thread"))
	}
	if input.Limit < 1 || input.Limit > 50 {
		return "", 0, "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("history_read limit must be between 1 and 50"))
	}
	cursor, err := parseHistoryReadCursor(input.Cursor)
	if err != nil {
		return "", 0, "", err
	}
	return threadID, input.Limit, cursor, nil
}

// parseHistoryReadCursor 校验可选游标；显式传入时不能是空白或超长。
func parseHistoryReadCursor(raw *string) (string, error) {
	if raw == nil {
		return "", nil
	}
	cursor := strings.TrimSpace(*raw)
	if cursor == "" {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("history_read cursor must be non-empty when provided"))
	}
	if len(cursor) > 512 {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("history_read cursor must be at most 512 bytes"))
	}
	return cursor, nil
}

// historyReadInputSchema 描述 history_read 的有界输入 contract。
func historyReadInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope":  map[string]any{"type": "string", "enum": []string{"current_thread"}},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"cursor": map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
		},
		"required":             []string{"scope", "limit"},
		"additionalProperties": false,
	}
}

const descriptionHistoryRead = "Read a bounded page of the current thread history. Requires scope=current_thread and an explicit limit between 1 and 50."
