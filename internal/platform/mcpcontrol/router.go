package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// NotifyConfigChanged 处理notify配置changed。
func (r *ToolRegistry) NotifyConfigChanged(ctx context.Context, topic string, scope *dto.SelectorScope, configVersion int64, payload json.RawMessage) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errInvalidParams("mcp config topic is required")
	}
	sel := dto.Selector{Subscription: topic}
	normalizedScope := shared.NormalizeSelectorScope(scope)
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

// NotifyBySelector 按selector处理notify。
func (r *ToolRegistry) NotifyBySelector(ctx context.Context, sel dto.Selector, method string, params any) error {
	return r.notifyTargets(ctx, r.IntersectTargets(sel), method, params)
}

// CallbackHookBefore 处理callbackhookbefore。
func (r *ToolRegistry) CallbackHookBefore(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookBefore, payload)
}

// CallbackHookCheck 处理callbackhookcheck。
func (r *ToolRegistry) CallbackHookCheck(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookCheck, payload)
}

// CallbackHookAfter 处理callbackhook后置。
func (r *ToolRegistry) CallbackHookAfter(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookAfter, payload)
}

// CallbackBefore 处理callbackbefore。
func (r *ToolRegistry) CallbackBefore(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.BeforeDecision, error) {
	return callbackHookDecision[dto.BeforeDecision](ctx, r, lease, dto.MethodHookBefore, payload)
}

// CallbackCheck 处理callbackcheck。
func (r *ToolRegistry) CallbackCheck(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.CheckDecision, error) {
	return callbackHookDecision[dto.CheckDecision](ctx, r, lease, dto.MethodHookCheck, payload)
}

// CallbackAfter 处理callback后置。
func (r *ToolRegistry) CallbackAfter(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.AfterDecision, error) {
	return callbackHookDecision[dto.AfterDecision](ctx, r, lease, dto.MethodHookAfter, payload)
}

func (r *ToolRegistry) callbackHookTopic(ctx context.Context, topic, method string, payload dto.HookPayload) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errInvalidParams("mcp hook topic is required")
	}
	payload = shared.CloneHookPayload(payload)
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
	payload = shared.CloneHookPayload(payload)
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
