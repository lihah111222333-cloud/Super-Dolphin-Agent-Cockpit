package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/creachadair/jrpc2/handler"

	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const maxFrontendIngestEvents = 100

type statusParams struct{}

type frontendIngestParams struct {
	Events []json.RawMessage `json:"events"`
}

type frontendIngestResponse struct {
	Enabled        bool   `json:"enabled"`
	Recorded       int    `json:"recorded"`
	Dropped        int    `json:"dropped"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

type frontendTraceEvent struct {
	Timestamp    time.Time          `json:"ts,omitzero"`
	TraceID      string             `json:"trace_id,omitempty"`
	SpanID       string             `json:"span_id,omitempty"`
	ParentSpanID string             `json:"parent_span_id,omitempty"`
	Kind         string             `json:"kind,omitempty"`
	Phase        string             `json:"phase,omitempty"`
	Method       string             `json:"method,omitempty"`
	ThreadID     string             `json:"thread_id,omitempty"`
	AgentID      string             `json:"agent_id,omitempty"`
	TurnID       string             `json:"turn_id,omitempty"`
	CallID       string             `json:"call_id,omitempty"`
	ClientKind   string             `json:"client_kind,omitempty"`
	ClientRoute  string             `json:"client_route,omitempty"`
	DurationMS   int64              `json:"duration_ms,omitempty"`
	Status       platformobs.Status `json:"status"`
	Error        string             `json:"error,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
}

func NewHandlers(svc *platformobs.Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"observability/status":          platformrpc.StrictHandler(statusHandler(svc)),
		"observability/frontend/ingest": platformrpc.StrictHandler(frontendIngestHandler(svc)),
	}}
}

func statusHandler(svc *platformobs.Service) func(context.Context, statusParams) (platformobs.ServiceStatus, error) {
	return func(context.Context, statusParams) (platformobs.ServiceStatus, error) {
		if svc == nil {
			return platformobs.ServiceStatus{}, fmt.Errorf("observability service is not wired")
		}
		return svc.Status(), nil
	}
}

func frontendIngestHandler(svc *platformobs.Service) func(context.Context, frontendIngestParams) (frontendIngestResponse, error) {
	return func(ctx context.Context, p frontendIngestParams) (frontendIngestResponse, error) {
		if svc == nil {
			return frontendIngestResponse{}, fmt.Errorf("observability service is not wired")
		}
		status := svc.Status()
		if !status.Enabled {
			return frontendIngestResponse{Enabled: false, Dropped: len(p.Events), DisabledReason: status.DisabledReason}, nil
		}
		limit := len(p.Events)
		dropped := 0
		if limit > maxFrontendIngestEvents {
			dropped = limit - maxFrontendIngestEvents
			limit = maxFrontendIngestEvents
		}
		recorded := 0
		for i := 0; i < limit; i++ {
			event, err := frontendEventFromRaw(p.Events[i])
			if err != nil {
				return frontendIngestResponse{}, fmt.Errorf("frontend trace event %d: %w", i, err)
			}
			if err := svc.Record(ctx, event); err != nil {
				return frontendIngestResponse{}, err
			}
			recorded++
		}
		return frontendIngestResponse{Enabled: true, Recorded: recorded, Dropped: dropped}, nil
	}
}

func frontendEventFromRaw(raw json.RawMessage) (platformobs.TraceEvent, error) {
	if len(raw) == 0 {
		return platformobs.TraceEvent{}, fmt.Errorf("event must be an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return platformobs.TraceEvent{}, err
	}
	for key := range fields {
		if !allowedFrontendTraceField(key) {
			return platformobs.TraceEvent{}, fmt.Errorf("field %q is not allowed", key)
		}
	}
	var in frontendTraceEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return platformobs.TraceEvent{}, err
	}
	if in.Status == "" {
		in.Status = platformobs.StatusOK
	}
	if in.Timestamp.IsZero() {
		in.Timestamp = time.Now().UTC()
	}
	return platformobs.TraceEvent{
		SchemaVersion: platformobs.SchemaVersion,
		Timestamp:     in.Timestamp,
		TraceID:       in.TraceID,
		SpanID:        in.SpanID,
		ParentSpanID:  in.ParentSpanID,
		Kind:          "frontend",
		Phase:         in.Phase,
		Method:        in.Method,
		ThreadID:      in.ThreadID,
		AgentID:       in.AgentID,
		TurnID:        in.TurnID,
		CallID:        in.CallID,
		ClientKind:    in.ClientKind,
		ClientRoute:   in.ClientRoute,
		DurationMS:    in.DurationMS,
		Status:        in.Status,
		Error:         in.Error,
		Metadata:      in.Metadata,
	}, nil
}

func allowedFrontendTraceField(key string) bool {
	switch key {
	case "ts", "trace_id", "span_id", "parent_span_id", "kind", "phase", "method", "thread_id", "agent_id", "turn_id", "call_id", "client_kind", "client_route", "duration_ms", "status", "error", "metadata":
		return true
	default:
		return false
	}
}
