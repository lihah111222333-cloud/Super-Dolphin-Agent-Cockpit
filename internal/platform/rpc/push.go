package rpc

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
)

const (
	methodEventAgentStateChanged = "ui/state/changed"
	methodEventTurnStarted       = "turn/started"
	methodEventTurnCompleted     = "turn/completed"
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

	dispatcher := bridge.dispatcher
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
			server.NotifyAll(context.Background(), bridge, methodEventAgentStateChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStarted) {
			server.NotifyAll(context.Background(), bridge, methodEventTurnStarted, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
			server.NotifyAll(context.Background(), bridge, methodEventTurnCompleted, ev)
		}, logger),
	}
}
