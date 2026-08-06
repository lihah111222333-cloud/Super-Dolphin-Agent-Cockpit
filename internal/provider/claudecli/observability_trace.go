package claudecli

import (
	"context"
	"maps"
	"sync/atomic"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

type claudeTraceSpanCounter struct {
	value atomic.Uint64
}

func newClaudeTraceSpanCounter() *claudeTraceSpanCounter {
	return &claudeTraceSpanCounter{}
}

func (c *claudeTraceSpanCounter) next() uint64 {
	if c == nil {
		panic("claudecli: trace span counter is required")
	}
	return c.value.Add(1)
}

func (d *driver) recordDriverTrace(ctx context.Context, event observability.TraceEvent) {
	if d == nil || d.tracer == nil {
		return
	}
	fillClaudeTrace(ctx, &event, d.traceSpanCounter)
	if err := d.tracer.Record(ctx, event); err != nil {
		d.tracer.WarnRecordError(d.logger, "provider.claudecli.driver", event, err)
	}
}

func (s *session) recordProviderTrace(ctx context.Context, event observability.TraceEvent) {
	if s == nil || s.tracer == nil {
		return
	}
	if event.AgentID == "" {
		event.AgentID = s.agentID
	}
	fillClaudeTrace(ctx, &event, s.traceSpanCounter)
	if err := s.tracer.Record(ctx, event); err != nil {
		s.tracer.WarnRecordError(s.logger, "provider.claudecli.session", event, err)
	}
}

// fillClaudeTrace 补齐 Claude provider trace 事件的默认字段。
// 调用方可预先设置 Method/Code/Status；这里只填缺失值并按错误状态捕获栈。
func fillClaudeTrace(ctx context.Context, event *observability.TraceEvent, counter *claudeTraceSpanCounter) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Kind == "" {
		event.Kind = "provider"
	}
	if event.Status == "" {
		event.Status = observability.StatusOK
	}
	if event.Code.File == "" {
		event.Code = observability.CodeAnchor{File: "internal/provider/claudecli/driver.go", Function: "claudecli.(*driver).start", Line: 195}
	}
	if trace, ok := observability.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
		event.ParentSpanID = trace.SpanID
	}
	if event.SpanID == "" {
		event.SpanID = "claude:" + event.Method + ":" + claudeTraceID(counter.next())
	}
	if event.Status == observability.StatusError || event.Status == observability.StatusSlow || event.Status == observability.StatusPanic {
		event.Stack = observability.CaptureStackForStatus(claudeTraceStackConfig(), event.Status)
	}
}

func claudeSessionEvent(method string, spec startSpec, elapsed time.Duration, err error) observability.TraceEvent {
	status := observability.StatusOK
	if err != nil {
		status = observability.StatusError
	}
	metadata := map[string]any{"provider": "claude"}
	maps.Copy(metadata, providershared.ErrorMetadata(err))
	return observability.TraceEvent{Method: method, AgentID: spec.agentID, ThreadID: spec.publicThread, DurationMS: elapsed.Milliseconds(), Status: status, Error: claudeTraceErrorSummary(status, err), Metadata: metadata}
}

func claudeTurnRunEvent(req dto.TurnRequest, providerTurnID string, elapsed time.Duration, err error) observability.TraceEvent {
	status := observability.StatusOK
	if err != nil {
		status = observability.StatusError
	}
	metadata := map[string]any{"provider": "claude", "provider_turn_id_set": providerTurnID != "", "input_count": int64(len(req.Inputs))}
	maps.Copy(metadata, providershared.ErrorMetadata(err))
	return observability.TraceEvent{Method: "provider.turn.run", ThreadID: req.ThreadID, TurnID: firstNonEmpty(req.LocalID, providerTurnID), DurationMS: elapsed.Milliseconds(), Status: status, Error: claudeTraceErrorSummary(status, err), Code: observability.CodeAnchor{File: "internal/provider/claudecli/session.go", Function: "claudecli.(*session).StartTurn", Line: 189}, Metadata: metadata}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func claudeTraceErrorSummary(status observability.Status, err error) string {
	return providershared.ErrorSummaryForError(status, err)
}

func claudeTraceStackConfig() observability.Config {
	return observability.Config{TraceStacks: map[observability.Status]bool{observability.StatusSlow: true, observability.StatusError: true, observability.StatusPanic: true}, StackMaxFrames: 8, StackMaxBytes: 4096}
}

func claudeTraceID(v uint64) string {
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
