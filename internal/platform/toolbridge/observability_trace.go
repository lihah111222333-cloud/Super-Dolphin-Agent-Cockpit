package toolbridge

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

// toolTraceSpanSeq 为本进程内 toolbridge trace span 生成递增后缀。
var toolTraceSpanSeq atomic.Uint64

// recordToolTrace 记录 toolbridge trace 事件，tracer 未启用时保持空操作。
func (h *Handler) recordToolTrace(ctx context.Context, event observability.TraceEvent) {
	if h == nil || h.tracer == nil {
		return
	}
	fillToolTrace(ctx, &event)
	if err := h.tracer.Record(ctx, event); err != nil {
		observability.WarnRecordError(h.logger, "toolbridge", event, err)
	}
}

// beginToolTraceContext 为一次工具调用创建或延续 trace/span 上下文。
func beginToolTraceContext(ctx context.Context) context.Context {
	trace, _ := observability.TraceFromContext(ctx)
	parentSpanID := trace.SpanID
	if trace.TraceID == "" {
		trace.TraceID = nextToolTraceSpan("trace")
	}
	trace.ParentSpanID = parentSpanID
	trace.SpanID = nextToolTraceSpan("tool.call")
	return observability.ContextWithTrace(ctx, trace)
}

// fillToolTrace 填充 trace/span/code/status 默认值，并按错误状态采集 stack。
func fillToolTrace(ctx context.Context, event *observability.TraceEvent) {
	fillToolTraceDefaults(event)
	if trace, ok := observability.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
		if event.SpanID == "" {
			event.SpanID = trace.SpanID
			event.ParentSpanID = trace.ParentSpanID
		} else if event.ParentSpanID == "" {
			event.ParentSpanID = trace.SpanID
		}
	}
	if event.SpanID == "" {
		event.SpanID = nextToolTraceSpan(event.Method)
	}
	if event.TraceID != "" {
		if event.Metadata == nil {
			event.Metadata = map[string]any{}
		}
		event.Metadata["trace_id"] = event.TraceID
	}
	if shouldCaptureToolStack(event.Status) {
		event.Stack = observability.CaptureStackForStatus(toolTraceStackConfig(), event.Status)
	}
}

// fillToolTraceDefaults 填充 toolbridge trace 事件的时间、kind、code 和状态默认值。
func fillToolTraceDefaults(event *observability.TraceEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Kind == "" {
		event.Kind = "toolbridge"
	}
	if event.Code.File == "" {
		event.Code = observability.CodeAnchor{File: "internal/platform/toolbridge/handler.go", Function: "toolbridge.(*Handler).HandleToolCall", Line: 89}
	}
	if event.Status == "" {
		event.Status = observability.StatusOK
	}
}

// shouldCaptureToolStack 判断当前状态是否需要记录调用栈。
func shouldCaptureToolStack(status observability.Status) bool {
	return status == observability.StatusError || status == observability.StatusSlow || status == observability.StatusPanic
}

// nextToolTraceSpan 生成本地唯一的 toolbridge span id。
func nextToolTraceSpan(method string) string {
	return "toolbridge:" + method + ":" + formatUint(toolTraceSpanSeq.Add(1))
}

// formatUint 在无额外分配的情况下格式化正整数。
func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// toolTraceStackConfig 返回 toolbridge trace 栈采集的固定上限配置。
func toolTraceStackConfig() observability.Config {
	return observability.Config{TraceStacks: map[observability.Status]bool{observability.StatusSlow: true, observability.StatusError: true, observability.StatusPanic: true}, StackMaxFrames: 8, StackMaxBytes: 4096}
}

// toolTraceBeginEvent 构造工具调用开始 trace 事件。
func toolTraceBeginEvent(req ToolCallRequest) observability.TraceEvent {
	metadata := toolTraceBeginMetadata(req)
	return observability.TraceEvent{
		Method:      "tool.call.begin",
		ThreadID:    req.ThreadID,
		AgentID:     req.AgentID,
		TurnID:      req.TurnID,
		CallID:      req.CallID,
		ToolName:    req.Name,
		ClientKind:  classifyTool(req.Name),
		ClientRoute: req.ClientKind,
		Status:      observability.StatusOK,
		Metadata:    metadata,
	}
}

// toolTraceBeginMetadata 构造开始事件 metadata，并只记录敏感参数键名不记录值。
func toolTraceBeginMetadata(req ToolCallRequest) map[string]any {
	targetPeer := strings.TrimSpace(req.ClientKind)
	if targetPeer == "" {
		targetPeer = classifyTool(req.Name)
	}
	metadata := map[string]any{
		"argument_bytes":   int64(len(req.Arguments)),
		"redaction_policy": "metadata_only",
		"source_actor":     strings.TrimSpace(req.AgentID),
		"target_peer":      targetPeer,
		"tool_name":        strings.TrimSpace(req.Name),
	}
	if keys := sensitiveToolArgumentKeys(req.Arguments); len(keys) > 0 {
		metadata["sensitive_argument_keys"] = keys
	}
	return metadata
}

// sensitiveToolArgumentKeys 返回参数中可能包含密钥的键名。
func sensitiveToolArgumentKeys(raw json.RawMessage) []string {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		if isSensitiveToolArgumentKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// isSensitiveToolArgumentKey 判断参数键名是否疑似包含敏感信息。
func isSensitiveToolArgumentKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token")
}

// toolTraceEndEvent 构造工具调用结束 trace 事件。
func toolTraceEndEvent(req ToolCallRequest, result any, callErr error, elapsed time.Duration, affectedFiles int) observability.TraceEvent {
	success := callErr == nil && toolTraceResultSuccess(result)
	status := observability.StatusOK
	if !success {
		status = observability.StatusError
	}
	metadata := map[string]any{
		"success":              success,
		"result_bytes":         int64(toolTraceJSONSize(result)),
		"truncated":            false,
		"affected_files_count": int64(affectedFiles),
	}
	for key, value := range toolTraceErrorMetadata(result, callErr) {
		metadata[key] = value
	}
	return observability.TraceEvent{
		Method:      "tool.call.end",
		ThreadID:    req.ThreadID,
		AgentID:     req.AgentID,
		TurnID:      req.TurnID,
		CallID:      req.CallID,
		ToolName:    req.Name,
		ClientKind:  classifyTool(req.Name),
		ClientRoute: req.ClientKind,
		DurationMS:  elapsed.Milliseconds(),
		Status:      status,
		Error:       toolTraceErrorSummary(status),
		Metadata:    metadata,
	}
}

// toolTraceErrorMetadata 合并外层调用错误和被包装进 ToolCallResult 的失败摘要。
// peer callback 错误会以失败结果返回，不能因为 callErr 为空就丢失可诊断预览。
func toolTraceErrorMetadata(result any, callErr error) map[string]any {
	metadata := observability.SafeErrorMetadata(callErr, "tool_call_failed")
	if len(metadata) > 0 {
		return metadata
	}
	preview := toolTraceFailurePreview(result)
	if preview == "" {
		return nil
	}
	return map[string]any{
		observability.ErrorPreviewField: preview,
		observability.ErrorCodeField:    "tool_call_failed",
	}
}

// toolTraceFailurePreview 从失败工具结果中提取短预览，并统一走 observability 脱敏逻辑。
func toolTraceFailurePreview(result any) string {
	r := toolTraceResult(result)
	if r == nil || r.Success {
		return ""
	}
	for _, item := range r.ContentItems {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		return observability.SafePreview(item.Text, 512).Preview
	}
	if len(r.StructuredContent) > 0 {
		return observability.SafePreview(r.StructuredContent, 512).Preview
	}
	return ""
}

func toolTraceResult(result any) *ToolCallResult {
	if r, ok := result.(*ToolCallResult); ok {
		return r
	}
	if r, ok := result.(ToolCallResult); ok {
		return &r
	}
	return nil
}

// toolTraceErrorSummary 返回 trace status 对应的简短错误摘要。
func toolTraceErrorSummary(status observability.Status) string {
	if status == observability.StatusError {
		return "tool call failed"
	}
	return ""
}

// toolTraceResultSuccess 从 ToolCallResult 判断工具调用是否成功。
func toolTraceResultSuccess(result any) bool {
	if r := toolTraceResult(result); r != nil {
		return r.Success
	}
	return true
}

// toolTraceJSONSize 估算结果 JSON 字节数；不可序列化时返回 0。
func toolTraceJSONSize(value any) int {
	if value == nil {
		return 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}

// toolDiffTraceEvent 构造工具 diff 发布 trace 事件。
func toolDiffTraceEvent(req ToolCallRequest, result difftracker.DiffResult, elapsed time.Duration, err error) observability.TraceEvent {
	status := observability.StatusOK
	if err != nil {
		status = observability.StatusError
	}
	return observability.TraceEvent{
		Method:     "tool.diff.emit",
		SpanID:     nextToolTraceSpan("tool.diff.emit"),
		ThreadID:   req.ThreadID,
		AgentID:    req.AgentID,
		TurnID:     req.TurnID,
		CallID:     req.CallID,
		ToolName:   req.Name,
		DurationMS: elapsed.Milliseconds(),
		Status:     status,
		Error:      toolTraceErrorSummary(status),
		Code:       observability.CodeAnchor{File: "internal/platform/toolbridge/diff_gen.go", Function: "toolbridge.(*Handler).emitToolDiff", Line: 36},
		Metadata: map[string]any{
			"success":              err == nil,
			"result_bytes":         int64(len(result.DiffText)),
			"truncated":            false,
			"affected_files_count": int64(len(result.Files)),
		},
	}
}
