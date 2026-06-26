package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

const ToolNameObservabilityTraceGet = "observability_trace_get"

// ObservabilityTraceHostToolRegistry 将本地 trace 诊断服务暴露为 host-direct 工具。
type ObservabilityTraceHostToolRegistry struct {
	svc *observability.Service
}

// observabilityTraceToolInput 是 observability_trace_get 的严格输入结构。
type observabilityTraceToolInput struct {
	TraceID      string `json:"trace_id"`
	Limit        int    `json:"limit,omitempty"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
	IncludeStack bool   `json:"include_stack,omitempty"`
}

// NewObservabilityTraceHostToolRegistry 创建 trace 诊断 host-direct registry。
func NewObservabilityTraceHostToolRegistry(svc *observability.Service) *ObservabilityTraceHostToolRegistry {
	if svc == nil {
		return nil
	}
	return &ObservabilityTraceHostToolRegistry{svc: svc}
}

// ListHostTools 在 observability 服务启用时暴露 trace 诊断工具。
func (r *ObservabilityTraceHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.svc == nil || !r.svc.Enabled() {
		return nil
	}
	schema, _ := json.Marshal(observabilityTraceInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameObservabilityTraceGet, Description: descriptionObservabilityTraceGet, InputSchema: schema}}
}

// HasTool 判断当前 registry 是否负责 trace 诊断工具。
func (r *ObservabilityTraceHostToolRegistry) HasTool(name string) bool {
	return r != nil && strings.TrimSpace(name) == ToolNameObservabilityTraceGet
}

// RequiresCWD 声明 trace 诊断不强制依赖 cwd，其它未知工具仍按默认要求 cwd。
func (r *ObservabilityTraceHostToolRegistry) RequiresCWD(name string) bool {
	return strings.TrimSpace(name) != ToolNameObservabilityTraceGet
}

// CallHostTool 校验 trace 输入并调用本地 observability 诊断服务。
func (r *ObservabilityTraceHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil || r.svc == nil {
		return nil, contract.NewAgentMemoryError("trace_unavailable", fmt.Errorf("observability trace service is not configured"))
	}
	if strings.TrimSpace(call.Name) != ToolNameObservabilityTraceGet {
		return nil, contract.NewAgentMemoryError("invalid_input", fmt.Errorf("host tools: unknown tool %q", call.Name))
	}
	input, err := decodeObservabilityTraceInput(call.Arguments)
	if err != nil {
		return nil, contract.NewAgentMemoryError("invalid_input", err)
	}
	return r.svc.DiagnoseTrace(ctx, observabilityTraceRequest(input, call))
}

// decodeObservabilityTraceInput 用严格 JSON 解码 trace 诊断参数，拒绝未知字段和尾随内容。
func decodeObservabilityTraceInput(raw json.RawMessage) (observabilityTraceToolInput, error) {
	var input observabilityTraceToolInput
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, fmt.Errorf("invalid observability_trace_get input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return input, fmt.Errorf("invalid observability_trace_get input: trailing JSON")
	}
	return input, validateObservabilityTraceInput(input)
}

// validateObservabilityTraceInput 校验 trace_id 与 limit 边界。
func validateObservabilityTraceInput(input observabilityTraceToolInput) error {
	if strings.TrimSpace(input.TraceID) == "" {
		return fmt.Errorf("invalid observability_trace_get input: trace_id is required")
	}
	if input.Limit < 0 || input.Limit > observability.TraceDiagnosisMaxLimit {
		return fmt.Errorf("invalid observability_trace_get input: limit must be between 0 and %d", observability.TraceDiagnosisMaxLimit)
	}
	return nil
}

// observabilityTraceRequest 将工具输入和调用上下文映射为诊断请求。
func observabilityTraceRequest(input observabilityTraceToolInput, call HostToolCall) observability.TraceDiagnosisRequest {
	cwd := strings.TrimSpace(call.CWD)
	return observability.TraceDiagnosisRequest{
		TraceID:       strings.TrimSpace(input.TraceID),
		Limit:         input.Limit,
		ForceRefresh:  input.ForceRefresh,
		IncludeStack:  input.IncludeStack,
		CWD:           cwd,
		WorkspaceRoot: cwd,
	}
}

// observabilityTraceInputSchema 描述 trace 诊断工具的模型可见参数。
func observabilityTraceInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"trace_id":      map[string]any{"type": "string"},
			"limit":         map[string]any{"type": "integer", "minimum": 0, "maximum": observability.TraceDiagnosisMaxLimit},
			"force_refresh": map[string]any{"type": "boolean"},
			"include_stack": map[string]any{"type": "boolean"},
		},
		"required":             []string{"trace_id"},
		"additionalProperties": false,
	}
}

// descriptionObservabilityTraceGet 提醒模型该工具只返回本地、脱敏且有界的 trace 诊断结果。
const descriptionObservabilityTraceGet = "Diagnose a local observability trace by trace_id using bounded, redacted memory and JSONL tail data."
