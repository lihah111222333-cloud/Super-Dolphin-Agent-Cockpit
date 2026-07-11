package app

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	platformobservability "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

// busTraceRecorder 将 bus trace 写入 observability 服务。
type busTraceRecorder struct {
	svc *platformobservability.Service
}

// busTraceRecorder 必须满足 bus.TraceRecorder。
var _ bus.TraceRecorder = busTraceRecorder{}

// provideBusTraceRecorder 提供 bus trace adapter。
func provideBusTraceRecorder(svc *platformobservability.Service) bus.TraceRecorder {
	return busTraceRecorder{svc: svc}
}

// RecordTrace 记录 bus trace。
// observability 服务未接线时直接返回，保持 tracing 为可选能力。
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

// rpcTraceRecorder 将 RPC trace 写入 observability 服务。
type rpcTraceRecorder struct {
	svc *platformobservability.Service
}

// rpcTraceRecorder 必须满足 rpc.TraceRecorder。
var _ rpc.TraceRecorder = rpcTraceRecorder{}

// provideRPCTraceRecorder 提供 RPC trace adapter。
func provideRPCTraceRecorder(svc *platformobservability.Service) rpc.TraceRecorder {
	return rpcTraceRecorder{svc: svc}
}

// Enabled 判断 RPC tracing 是否启用。
func (r rpcTraceRecorder) Enabled() bool {
	return r.svc != nil && r.svc.Enabled()
}

// RecordTrace 记录 RPC trace。
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
