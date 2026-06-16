package unified

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/runtimeconfig"
)

func (c *Client) recordProviderTrace(ctx context.Context, event observability.TraceEvent) {
	if c != nil {
		providershared.RecordTrace(ctx, c.tracer, event, "", observability.CodeAnchor{File: "internal/provider/unified/client.go", Function: "unified.(*Client).open", Line: 48})
	}
}

func (c *Client) wrapSession(provider string, session contract.Session) contract.Session {
	if c == nil || c.tracer == nil || session == nil {
		return session
	}
	return &tracedSession{Session: session, provider: provider, tracer: c.tracer}
}

func providerSessionEvent(method, provider, agentID, threadID string, elapsed time.Duration, err error) observability.TraceEvent {
	status := providershared.TraceStatus(err)
	return observability.TraceEvent{Method: method, AgentID: agentID, ThreadID: threadID, DurationMS: elapsed.Milliseconds(), Status: status, Error: providershared.ErrorSummary(status), Metadata: map[string]any{"provider": provider}}
}

type tracedSession struct {
	contract.Session
	provider string
	tracer   *observability.Service
}

func (s *tracedSession) RuntimeConfigSnapshot() map[string]any {
	reader, ok := s.Session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return nil
	}
	return reader.RuntimeConfigSnapshot()
}

func (s *tracedSession) StartTurn(ctx context.Context, req dto.TurnRequest) (handle contract.TurnHandle, err error) {
	started := time.Now()
	defer func() {
		providershared.RecordTrace(ctx, s.tracer, providerTurnEvent(s.provider, req, handle, time.Since(started), err), "", observability.CodeAnchor{File: "internal/provider/unified/observability_trace.go", Function: "unified.(*tracedSession).StartTurn", Line: 33})
	}()
	return s.Session.StartTurn(ctx, req)
}

func providerTurnEvent(provider string, req dto.TurnRequest, handle contract.TurnHandle, elapsed time.Duration, err error) observability.TraceEvent {
	status := providershared.TraceStatus(err)
	providerTurnID := ""
	if handle != nil {
		providerTurnID = handle.ProviderID()
	}
	return observability.TraceEvent{Method: "provider.turn.run", ThreadID: req.ThreadID, TurnID: firstTraceString(req.LocalID, providerTurnID), DurationMS: elapsed.Milliseconds(), Status: status, Error: providershared.ErrorSummary(status), Metadata: map[string]any{"provider": provider, "provider_turn_id_set": providerTurnID != "", "input_count": int64(len(req.Inputs))}}
}

func firstTraceString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
