package unified

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// recordProviderTrace 把 unified client 的关键 provider 操作写入观测链路。
func (c *Client) recordProviderTrace(ctx context.Context, event observability.TraceEvent) {
	if c != nil {
		providershared.RecordTrace(ctx, c.tracer, event, "", observability.CodeAnchor{File: "internal/provider/unified/client.go", Function: "unified.(*Client).open", Line: 48})
	}
}

// wrapSession 在 tracer 可用时包裹 provider session，保持原 session 行为不变并追加 trace。
func (c *Client) wrapSession(provider string, session contract.Session) contract.Session {
	if c == nil || c.tracer == nil || session == nil {
		return session
	}
	return &tracedSession{Session: session, provider: provider, tracer: c.tracer}
}

// providerSessionEvent 组装 provider session 级别的 TraceEvent，错误状态由共享规则统一归类。
func providerSessionEvent(method, provider, agentID, threadID string, elapsed time.Duration, err error) observability.TraceEvent {
	status := providershared.TraceStatus(err)
	metadata := map[string]any{"provider": provider}
	for key, value := range providershared.ErrorMetadata(err) {
		metadata[key] = value
	}
	return observability.TraceEvent{Method: method, AgentID: agentID, ThreadID: threadID, DurationMS: elapsed.Milliseconds(), Status: status, Error: providershared.ErrorSummaryForError(status, err), Metadata: metadata}
}

// tracedSession 装饰 contract.Session，只在 StartTurn 等边界补充观测信息。
type tracedSession struct {
	contract.Session
	provider string
	tracer   *observability.Service
}

// RuntimeConfigSnapshot 透传底层 session 的运行时配置快照；底层不支持时返回 nil。
func (s *tracedSession) RuntimeConfigSnapshot() map[string]any {
	reader, ok := s.Session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return nil
	}
	return reader.RuntimeConfigSnapshot()
}

// StartTurn 调用底层 provider 启动 turn，并通过 defer 记录成功或失败的耗时 trace。
func (s *tracedSession) StartTurn(ctx context.Context, req dto.TurnRequest) (handle contract.TurnHandle, err error) {
	started := time.Now()
	defer func() {
		providershared.RecordTrace(ctx, s.tracer, providerTurnEvent(s.provider, req, handle, time.Since(started), err), "", observability.CodeAnchor{File: "internal/provider/unified/observability_trace.go", Function: "unified.(*tracedSession).StartTurn", Line: 33})
	}()
	return s.Session.StartTurn(ctx, req)
}

// providerTurnEvent 组装单次 turn 的 TraceEvent，优先使用本地 turn ID，缺失时回退到 provider ID。
func providerTurnEvent(provider string, req dto.TurnRequest, handle contract.TurnHandle, elapsed time.Duration, err error) observability.TraceEvent {
	status := providershared.TraceStatus(err)
	providerTurnID := ""
	if handle != nil {
		providerTurnID = handle.ProviderID()
	}
	metadata := map[string]any{"provider": provider, "provider_turn_id_set": providerTurnID != "", "input_count": int64(len(req.Inputs))}
	for key, value := range providershared.ErrorMetadata(err) {
		metadata[key] = value
	}
	return observability.TraceEvent{Method: "provider.turn.run", ThreadID: req.ThreadID, TurnID: firstTraceString(req.LocalID, providerTurnID), DurationMS: elapsed.Milliseconds(), Status: status, Error: providershared.ErrorSummaryForError(status, err), Metadata: metadata}
}

// firstTraceString 返回第一个非空 trace 字段，用于避免观测事件里写入空 ID。
func firstTraceString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
