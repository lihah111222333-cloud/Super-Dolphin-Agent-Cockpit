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

type ObservabilityTraceHostToolRegistry struct {
	svc *observability.Service
}

type observabilityTraceToolInput struct {
	TraceID      string `json:"trace_id"`
	Limit        int    `json:"limit,omitempty"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
	IncludeStack bool   `json:"include_stack,omitempty"`
}

// NewObservabilityTraceHostToolRegistry 创建observabilitytracehost工具注册表。
func NewObservabilityTraceHostToolRegistry(svc *observability.Service) *ObservabilityTraceHostToolRegistry {
	if svc == nil {
		return nil
	}
	return &ObservabilityTraceHostToolRegistry{svc: svc}
}

// ListHostTools 列出host工具。
func (r *ObservabilityTraceHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.svc == nil || !r.svc.Enabled() {
		return nil
	}
	schema, _ := json.Marshal(observabilityTraceInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameObservabilityTraceGet, Description: descriptionObservabilityTraceGet, InputSchema: schema}}
}

// HasTool 判断工具是否可用。
func (r *ObservabilityTraceHostToolRegistry) HasTool(name string) bool {
	return r != nil && strings.TrimSpace(name) == ToolNameObservabilityTraceGet
}

// RequiresCWD 处理requires工作目录。
func (r *ObservabilityTraceHostToolRegistry) RequiresCWD(name string) bool {
	return strings.TrimSpace(name) != ToolNameObservabilityTraceGet
}

// CallHostTool 调用host工具。
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

func validateObservabilityTraceInput(input observabilityTraceToolInput) error {
	if strings.TrimSpace(input.TraceID) == "" {
		return fmt.Errorf("invalid observability_trace_get input: trace_id is required")
	}
	if input.Limit < 0 || input.Limit > observability.TraceDiagnosisMaxLimit {
		return fmt.Errorf("invalid observability_trace_get input: limit must be between 0 and %d", observability.TraceDiagnosisMaxLimit)
	}
	return nil
}

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

const descriptionObservabilityTraceGet = "Diagnose a local observability trace by trace_id using bounded, redacted memory and JSONL tail data."
