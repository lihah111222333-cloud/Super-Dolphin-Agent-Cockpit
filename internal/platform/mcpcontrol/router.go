package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// NotifyConfigChanged 向订阅配置 topic 的 peer 广播版本变更，payload 会复制后再发送。
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

// NotifyBySelector 根据完整 selector 找到 active peer 并走统一 fanout 通知路径。
func (r *ToolRegistry) NotifyBySelector(ctx context.Context, sel dto.Selector, method string, params any) error {
	return r.notifyTargets(ctx, r.IntersectTargets(sel), method, params)
}

// CallbackHookBefore 向订阅 topic 的 hook peer 广播 before 回调，不聚合决策。
func (r *ToolRegistry) CallbackHookBefore(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookBefore, payload)
}

// CallbackHookCheck 向订阅 topic 的 hook peer 广播 check 回调，不聚合决策。
func (r *ToolRegistry) CallbackHookCheck(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookCheck, payload)
}

// CallbackHookAfter 向订阅 topic 的 hook peer 广播 after 回调，用于通知清理或审计。
func (r *ToolRegistry) CallbackHookAfter(ctx context.Context, topic string, payload dto.HookPayload) error {
	return r.callbackHookTopic(ctx, topic, dto.MethodHookAfter, payload)
}

// CallbackBefore 对指定租约执行 before 回调并返回该 peer 的决策结果。
func (r *ToolRegistry) CallbackBefore(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.BeforeDecision, error) {
	return callbackHookDecision[dto.BeforeDecision](ctx, r, lease, dto.MethodHookBefore, payload)
}

// CallbackCheck 对指定租约执行 check 回调并返回该 peer 的决策结果。
func (r *ToolRegistry) CallbackCheck(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.CheckDecision, error) {
	return callbackHookDecision[dto.CheckDecision](ctx, r, lease, dto.MethodHookCheck, payload)
}

// CallbackAfter 对指定租约执行 after 回调并返回该 peer 的结果。
func (r *ToolRegistry) CallbackAfter(ctx context.Context, lease dto.LeaseKey, payload dto.HookPayload) (dto.AfterDecision, error) {
	return callbackHookDecision[dto.AfterDecision](ctx, r, lease, dto.MethodHookAfter, payload)
}

// callbackHookTopic 按 topic 广播 hook payload，发送前会复制 payload 并写入规范化 topic。
func (r *ToolRegistry) callbackHookTopic(ctx context.Context, topic, method string, payload dto.HookPayload) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errInvalidParams("mcp hook topic is required")
	}
	payload = shared.CloneHookPayload(payload)
	payload.Topic = topic
	return r.callbackTargets(ctx, r.snapshotTargets(r.bySubscription, topic), method, payload)
}

// callbackHookDecision 对单个租约执行带返回值的 hook callback，失败计数复用 fanout 目标调用逻辑。
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
	err = registry.invokeFanoutTarget(ctx, sendTarget{key: instance.Lease, peer: instance.Peer, runtime: instance.runtime}, fanoutOperation{
		name: "callback",
		invoke: func(ctx context.Context, peer Peer) error {
			return peer.Callback(ctx, method, payload, &decision)
		},
	})
	return decision, err
}

// callbackTargets 把无返回值 hook callback 包装成通用 fanoutOperation。
func (r *ToolRegistry) callbackTargets(ctx context.Context, targets []sendTarget, method string, params any) error {
	return r.fanoutTargets(ctx, targets, method, fanoutOperation{
		name: "callback",
		invoke: func(ctx context.Context, peer Peer) error {
			return peer.Callback(ctx, method, params, nil)
		},
	})
}
