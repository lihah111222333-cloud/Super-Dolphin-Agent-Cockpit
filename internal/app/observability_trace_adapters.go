package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformobservability "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type busTraceRecorder struct {
	svc *platformobservability.Service
}

var _ bus.TraceRecorder = busTraceRecorder{}

func provideBusTraceRecorder(svc *platformobservability.Service) bus.TraceRecorder {
	return busTraceRecorder{svc: svc}
}

// RecordTrace 记录trace。
func (r busTraceRecorder) RecordTrace(ctx context.Context, record bus.TraceRecord) error {
	if r.svc == nil {
		return nil
	}
	return r.svc.Record(ctx, platformobservability.TraceEvent{
		SchemaVersion: record.SchemaVersion,
		Timestamp:     record.Timestamp,
		TraceID:       record.TraceID,
		SpanID:        record.SpanID,
		ParentSpanID:  record.ParentSpanID,
		Kind:          record.Kind,
		Method:        record.Method,
		ThreadID:      record.ThreadID,
		AgentID:       record.AgentID,
		TurnID:        record.TurnID,
		CallID:        record.CallID,
		ToolName:      record.ToolName,
		Status:        platformobservability.Status(record.Status),
		Code: platformobservability.CodeAnchor{
			File:     record.Code.File,
			Function: record.Code.Function,
			Line:     record.Code.Line,
		},
		Metadata: platformobservability.Metadata(record.Metadata),
	})
}

type rpcTraceRecorder struct {
	svc *platformobservability.Service
}

var _ rpc.TraceRecorder = rpcTraceRecorder{}

func provideRPCTraceRecorder(svc *platformobservability.Service) rpc.TraceRecorder {
	return rpcTraceRecorder{svc: svc}
}

// Enabled 判断应用装配是否启用。
func (r rpcTraceRecorder) Enabled() bool {
	return r.svc != nil && r.svc.Enabled()
}

// RecordTrace 记录trace。
func (r rpcTraceRecorder) RecordTrace(ctx context.Context, record rpc.TraceRecord) error {
	if r.svc == nil {
		return nil
	}
	return r.svc.Record(ctx, platformobservability.TraceEvent{
		Timestamp:    record.Timestamp,
		TraceID:      record.TraceID,
		SpanID:       record.SpanID,
		ParentSpanID: record.ParentSpanID,
		Kind:         record.Kind,
		Phase:        record.Phase,
		Method:       record.Method,
		DurationMS:   record.DurationMS,
		Status:       platformobservability.Status(record.Status),
		Error:        record.Error,
		Code: platformobservability.CodeAnchor{
			File:     record.Code.File,
			Function: record.Code.Function,
			Line:     record.Code.Line,
		},
		Metadata: platformobservability.Metadata(record.Metadata),
	})
}
