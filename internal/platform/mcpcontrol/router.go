package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func (r *ToolRegistry) NotifyConfigChanged(ctx context.Context, topic string, scope *dto.SelectorScope, configVersion int64, payload json.RawMessage) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errInvalidParams("mcp config topic is required")
	}
	sel := dto.Selector{Subscription: topic}
	normalizedScope := normalizeSelectorScope(scope)
	if normalizedScope != (dto.SelectorScope{}) {
		sel.Scope = &normalizedScope
	}
	return r.NotifyBySelector(ctx, sel, dto.MethodConfigChanged, dto.ConfigChangedNotify{
		Selector:      sel,
		Scope:         configChangeScope(topic),
		ConfigVersion: configVersion,
		Payload:       append(json.RawMessage(nil), payload...),
	})
}

func (r *ToolRegistry) NotifyBySelector(ctx context.Context, sel dto.Selector, method string, params any) error {
	return r.notifyTargets(ctx, r.IntersectTargets(sel), method, params)
}

func (r *ToolRegistry) CallbackHookBefore(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookBefore, payload)
}

func (r *ToolRegistry) CallbackHookCheck(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookCheck, payload)
}

func (r *ToolRegistry) CallbackHookAfter(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookAfter, payload)
}

func (r *ToolRegistry) CallbackBefore(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.BeforeDecision, error) {
	return callbackHookDecision[dto.BeforeDecision](ctx, r, lease, dto.MethodHookBefore, payload)
}

func (r *ToolRegistry) CallbackCheck(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.CheckDecision, error) {
	return callbackHookDecision[dto.CheckDecision](ctx, r, lease, dto.MethodHookCheck, payload)
}

func (r *ToolRegistry) CallbackAfter(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.AfterDecision, error) {
	return callbackHookDecision[dto.AfterDecision](ctx, r, lease, dto.MethodHookAfter, payload)
}

func (r *ToolRegistry) callbackHookTopic(ctx context.Context, topic, method string, payload dto.HookPayload) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errInvalidParams("mcp hook topic is required")
	}
	payload = cloneHookPayload(payload)
	payload.Topic = topic
	return r.callbackTargets(ctx, r.snapshotTargets(r.bySubscription, topic), method, payload)
}

func callbackHookDecision[T any](
	ctx context.Context,
	registry *ToolRegistry,
	lease dto.LeaseKey,
	method string,
	payload dto.HookPayload,
) (T, error) {
	var decision T
	instance, err := resolveRegisteredInstance(registry, lease, false)
	if err != nil {
		return decision, err
	}
	if instance.Peer == nil {
		return decision, errPeerUnavailable("mcp peer %s/%d is not available", instance.Lease.InstanceID, instance.Lease.Generation)
	}
	payload = cloneHookPayload(payload)
	err = registry.invokeFanoutTarget(ctx, sendTarget{key: instance.Lease, peer: instance.Peer}, fanoutOperation{
		name: "callback",
		invoke: func(ctx context.Context, peer Peer) error {
			return peer.Callback(ctx, method, payload, &decision)
		},
	})
	return decision, err
}

func (r *ToolRegistry) callbackTargets(ctx context.Context, targets []sendTarget, method string, params any) error {
	return r.fanoutTargets(ctx, targets, method, fanoutOperation{
		name: "callback",
		invoke: func(ctx context.Context, peer Peer) error {
			return peer.Callback(ctx, method, params, nil)
		},
	})
}

func cloneHookPayload(payload dto.HookPayload) dto.HookPayload {
	cloned := payload
	cloned.Context = append(json.RawMessage(nil), payload.Context...)
	return cloned
}
