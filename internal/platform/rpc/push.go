package rpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
)

// PushBridge bridges internal events into jrpc2 server push APIs.
type PushBridge struct {
	dispatcher *event.Dispatcher
	logger     *slog.Logger
}

func NewPushBridge(dispatcher *event.Dispatcher, logger *slog.Logger) *PushBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &PushBridge{dispatcher: dispatcher, logger: logger}
}

func (b *PushBridge) NotifyClient(ctx context.Context, server *jrpc2.Server, method string, params any) error {
	if server == nil {
		return ErrInvalidState("rpc push server is nil")
	}
	return server.Notify(ctx, method, params)
}

func (b *PushBridge) CallbackClient(ctx context.Context, server *jrpc2.Server, method string, params any) (json.RawMessage, error) {
	if server == nil {
		return nil, ErrInvalidState("rpc push server is nil")
	}
	resp, err := server.Callback(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, ErrInvalidState("rpc callback response is nil")
	}
	var raw json.RawMessage
	if err := resp.UnmarshalResult(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func BindEventToNotify[T event.Event](bridge *PushBridge, server *jrpc2.Server, method string) context.CancelFunc {
	if bridge == nil || bridge.dispatcher == nil || server == nil {
		return func() {}
	}
	logger := bridge.logger
	if logger == nil {
		logger = slog.Default()
	}
	return event.Subscribe(bridge.dispatcher, func(ev T) {
		if err := bridge.NotifyClient(context.Background(), server, method, ev); err != nil {
			logger.Warn("rpc push notify failed", "method", method, "error", err)
		}
	})
}

func subscribeCoreEventPushes(bridge *PushBridge, server *Server, logger *slog.Logger) []context.CancelFunc {
	if bridge == nil || bridge.dispatcher == nil || server == nil {
		return nil
	}
	cancels := eventsurface.Bind(bridge.dispatcher, logger, func(method string, payload any) {
		for _, notification := range eventsurface.ExpandNotifications(method, payload) {
			server.NotifyAll(context.Background(), bridge, notification.Method, notification.Payload)
		}
	})
	cancels = append(cancels, subscribeRawProviderEventPushes(bridge, server, logger))
	return cancels
}

var typedPushMethods = map[string]struct{}{
	strings.ToLower(eventsurface.MethodUIStateChanged):   {},
	strings.ToLower(eventsurface.MethodTurnStarted):      {},
	strings.ToLower(eventsurface.MethodTurnCompleted):    {},
	strings.ToLower(eventsurface.MethodThreadStarted):    {},
	strings.ToLower(eventsurface.MethodThreadStopped):    {},
	strings.ToLower(eventsurface.MethodThreadMessages):   {},
	strings.ToLower(eventsurface.MethodThreadCompacted):  {},
	strings.ToLower(eventsurface.MethodSkillsChanged):    {},
	strings.ToLower(eventsurface.MethodUIThreadPatch):    {},
	strings.ToLower(eventsurface.MethodAgentLaunched):    {},
	strings.ToLower(eventsurface.MethodAgentStopped):     {},
}

func subscribeRawProviderEventPushes(bridge *PushBridge, server *Server, logger *slog.Logger) context.CancelFunc {
	if bridge == nil || bridge.dispatcher == nil || server == nil {
		return func() {}
	}
	return platformbus.ResilientSubscribe(bridge.dispatcher, func(raw providerdto.BusRawProviderEvent) {
		for _, notification := range providerPushNotifications(raw.Event) {
			server.NotifyAll(context.Background(), bridge, notification.Method, notification.Payload)
		}
	}, logger)
}

func providerPushNotifications(raw providerdto.RawProviderEvent) []eventsurface.Notification {
	method := normalizeRawProviderPushMethod(raw.EventType)
	if !shouldPushRawProviderMethod(method) {
		return nil
	}
	return eventsurface.ExpandNotifications(method, raw.Data)
}

func normalizeRawProviderPushMethod(method string) string {
	switch strings.TrimSpace(method) {
	case legacyApprovalEventMethod:
		return approvalCallbackMethodCommandExecution
	default:
		return strings.TrimSpace(method)
	}
}

func shouldPushRawProviderMethod(method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	if _, ok := typedPushMethods[strings.ToLower(method)]; ok {
		return false
	}
	switch {
	case strings.HasPrefix(method, "item/"),
		strings.HasPrefix(method, "turn/plan/"),
		strings.HasPrefix(method, "turn/diff/"),
		strings.HasPrefix(method, "agent/event/"),
		strings.HasPrefix(method, "account/"),
		strings.HasPrefix(method, "app/list/"),
		strings.HasPrefix(method, "fuzzyFileSearch/"),
		strings.HasSuffix(method, "/requestApproval"):
		return true
	}
	switch method {
	case "approval/request",
		"tool/approval/request",
		"error",
		"configWarning",
		"deprecationNotice",
		"thread/name/updated",
		"thread/tokenUsage/updated",
		"thread/tokenusage/updated":
		return true
	default:
		return false
	}
}
