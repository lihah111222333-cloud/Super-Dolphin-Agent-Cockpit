package mcpcontrol

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
)

type ContextProvider interface {
	GetContext(ctx context.Context, instance *ToolInstance, req dto.ContextRequest) (dto.ContextResponse, error)
}

type EventSink interface {
	HandleEvent(ctx context.Context, instance *ToolInstance, req dto.EventNotify) error
}

type LogSink interface {
	HandleLog(ctx context.Context, instance *ToolInstance, req dto.LogNotify) error
}

type ackResponse struct {
	OK bool `json:"ok"`
}

type controlEvent struct {
	Lease      dto.LeaseKey    `json:"lease"`
	EventID    string          `json:"event_id,omitempty"`
	EventType  string          `json:"event_type"`
	AuditClass string          `json:"audit_class,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

const controlEventType uint32 = 0xC7100001

func (controlEvent) Type() uint32 { return controlEventType }

func NewHandlers(p handlerDeps) rpc.HandlerMapResult {
	contextProvider := p.Context
	if contextProvider == nil {
		contextProvider = registryContextProvider{}
	}
	eventSink := p.Events
	if eventSink == nil {
		eventSink = defaultEventSink{dispatcher: p.Dispatcher, logger: p.Logger}
	}
	logSink := p.Logs
	if logSink == nil {
		logSink = defaultLogSink{logger: p.Logger}
	}
	runtimeReports := p.RuntimeReports
	if runtimeReports == nil {
		runtimeReports = defaultRuntimeReportHandler{orchestration: p.Orchestration}
	}
	completionReports := p.CompletionReports
	if completionReports == nil {
		completionReports = defaultCompletionReportHandler{orchestration: p.Orchestration}
	}

	return rpc.HandlerMapResult{Handlers: handler.Map{
		dto.MethodRegister: rpc.StrictHandler(func(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
			return p.Registry.Register(ctx, req)
		}),
		dto.MethodHeartbeat: rpc.StrictHandler(func(ctx context.Context, req dto.HeartbeatRequest) (dto.HeartbeatResponse, error) {
			return p.Registry.Heartbeat(ctx, req)
		}),
		dto.MethodContext: rpc.StrictHandler(func(ctx context.Context, req dto.ContextRequest) (dto.ContextResponse, error) {
			instance, err := resolveRegisteredInstance(p.Registry, req.Lease, false)
			if err != nil {
				return dto.ContextResponse{}, err
			}
			return contextProvider.GetContext(ctx, instance, req)
		}),
		dto.MethodEvent: rpc.StrictHandler(func(ctx context.Context, req dto.EventNotify) (ackResponse, error) {
			instance, err := resolveRegisteredInstance(p.Registry, req.Lease, false)
			if err != nil {
				return ackResponse{}, err
			}
			return ackResponse{OK: true}, eventSink.HandleEvent(ctx, instance, req)
		}),
		dto.MethodLog: rpc.StrictHandler(func(ctx context.Context, req dto.LogNotify) (ackResponse, error) {
			instance, err := resolveRegisteredInstance(p.Registry, req.Lease, false)
			if err != nil {
				return ackResponse{}, err
			}
			return ackResponse{OK: true}, logSink.HandleLog(ctx, instance, req)
		}),
		dto.MethodApproval: rpc.StrictHandler(func(ctx context.Context, req dto.ApprovalRequest) (dto.ApprovalResponse, error) {
			return requestApproval(ctx, p.Registry, p.Approvals, p.Bridge, req)
		}),
		dto.MethodReport: rpc.StrictHandler(func(ctx context.Context, req dto.ReportRequest) (dto.ReportResponse, error) {
			return handleReport(ctx, p.Registry, runtimeReports, completionReports, req)
		}),
	}}
}

func requestApproval(
	ctx context.Context,
	registry *ToolRegistry,
	approvals *rpc.ApprovalManager,
	bridge *rpc.PushBridge,
	req dto.ApprovalRequest,
) (dto.ApprovalResponse, error) {
	if approvals == nil {
		return dto.ApprovalResponse{}, errApprovalUnavailable("mcp approval manager is not configured")
	}
	instance, err := resolveRegisteredInstance(registry, req.Lease, false)
	if err != nil {
		return dto.ApprovalResponse{}, err
	}
	server, err := serverFromContext(ctx)
	if err != nil {
		return dto.ApprovalResponse{}, err
	}

	callCtx := ctx
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, timeDurationMillis(req.TimeoutMs))
		defer cancel()
	}
	payload := decodePayloadMap(req.Payload)
	decision, err := approvals.RequestApproval(callCtx, bridge, server, rpc.ApprovalRequest{
		CallID:       req.CallID,
		ApprovalID:   req.CallID,
		ToolName:     req.ToolName,
		AgentID:      instance.AgentID,
		ThreadID:     instance.ThreadID,
		Reason:       req.Reason,
		Kind:         req.Kind,
		SourceMethod: dto.MethodApproval,
		Payload:      payload,
	})
	if err != nil {
		return dto.ApprovalResponse{}, err
	}
	return dto.ApprovalResponse{
		Approved: decision.Approved,
		Reason:   decision.Reason,
		Detail:   append(json.RawMessage(nil), decision.Detail...),
	}, nil
}

type registryContextProvider struct{}

func (registryContextProvider) GetContext(_ context.Context, instance *ToolInstance, req dto.ContextRequest) (dto.ContextResponse, error) {
	target := cloneInstance(instance)
	if agentID := strings.TrimSpace(req.AgentID); agentID != "" {
		target.AgentID = agentID
	}
	payload, err := contextPayload(req.Scope, target)
	if err != nil {
		return dto.ContextResponse{}, err
	}
	return buildContextResponse(req.Scope, filterKeys(payload, req.Keys))
}

type defaultEventSink struct {
	dispatcher *event.Dispatcher
	logger     *slog.Logger
}

func (s defaultEventSink) HandleEvent(_ context.Context, instance *ToolInstance, req dto.EventNotify) error {
	if strings.TrimSpace(req.EventType) == "" {
		return errInvalidParams("mcp event_type is required")
	}
	if s.dispatcher != nil {
		event.Publish(s.dispatcher, controlEvent{
			Lease:      instance.Lease,
			EventID:    req.EventID,
			EventType:  req.EventType,
			AuditClass: req.AuditClass,
			Payload:    append(json.RawMessage(nil), req.Payload...),
		})
	}
	if s.logger != nil {
		s.logger.Info("mcp control event", "instance_id", instance.Lease.InstanceID, "generation", instance.Lease.Generation, "event_type", req.EventType)
	}
	return nil
}

type defaultLogSink struct {
	logger *slog.Logger
}

func (s defaultLogSink) HandleLog(_ context.Context, instance *ToolInstance, req dto.LogNotify) error {
	if strings.TrimSpace(req.Message) == "" {
		return errInvalidParams("mcp log message is required")
	}
	if s.logger == nil {
		return nil
	}
	s.logger.Info("mcp control log",
		"instance_id", instance.Lease.InstanceID,
		"generation", instance.Lease.Generation,
		"level", req.Level,
		"message", req.Message,
		"fields", req.Fields,
	)
	return nil
}

func filterKeys(payload map[string]any, keys []string) map[string]any {
	if len(keys) == 0 {
		return payload
	}
	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

func decodePayloadMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err == nil {
		return payload
	}
	return map[string]any{"payload": append(json.RawMessage(nil), raw...)}
}

func serverFromContext(ctx context.Context) (server *jrpc2.Server, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errPeerUnavailable("mcp control request must run inside a jrpc2 handler")
			server = nil
		}
	}()
	server = jrpc2.ServerFromContext(ctx)
	if server == nil {
		return nil, errPeerUnavailable("mcp control peer is not available")
	}
	return server, nil
}

func timeDurationMillis(timeoutMs int) time.Duration {
	if timeoutMs <= 0 {
		return defaultNotifyTimeout
	}
	return time.Duration(timeoutMs) * time.Millisecond
}
