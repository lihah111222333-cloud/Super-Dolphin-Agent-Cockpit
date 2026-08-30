package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// ToolNameHistoryRead 是当前线程有界历史读取的 host-direct 规范名称。
// 它位于已退役的 memory owner 之外，历史能力可以独立装配。
const ToolNameHistoryRead = "history_read"

// HistoryReadHostToolRegistry 将当前线程有界历史读取暴露为 host-direct 工具。
// 可信线程身份由 Handler 元数据注入，模型参数不能选择任意线程。
type HistoryReadHostToolRegistry struct {
	history contract.SessionStatusPort
}

type historyReadToolInput struct {
	Scope  string  `json:"scope"`
	Limit  int     `json:"limit"`
	Cursor *string `json:"cursor,omitempty"`
}

// NewHistoryReadHostToolRegistry 创建 history_read host-direct 注册表。
func NewHistoryReadHostToolRegistry(history contract.SessionStatusPort) *HistoryReadHostToolRegistry {
	if history == nil {
		return nil
	}
	return &HistoryReadHostToolRegistry{history: history}
}

// ListHostTools 在历史状态端口可用时发布有界 history_read schema。
func (r *HistoryReadHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.history == nil {
		return nil
	}
	schema, _ := json.Marshal(historyReadInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameHistoryRead, Description: descriptionHistoryRead, InputSchema: schema}}
}

// HasTool 判断当前注册表是否负责 history_read；可见性由 ListHostTools 单独控制，
// 这样旧调用仍会在本地得到明确失败。
func (r *HistoryReadHostToolRegistry) HasTool(name string) bool {
	return r != nil && strings.TrimSpace(name) == ToolNameHistoryRead
}

// RequiresCWD 声明 history_read 不要求工作目录，可信范围来自线程元数据。
func (r *HistoryReadHostToolRegistry) RequiresCWD(name string) bool {
	return strings.TrimSpace(name) != ToolNameHistoryRead
}

// CallHostTool 校验有界输入，并读取可信线程的历史消息。
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

// buildHistoryReadRequest 只接受 current_thread 作用域，并保留显式分页边界。
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
