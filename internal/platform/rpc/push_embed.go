package rpc

import (
	"encoding/json"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
)

// embeddedThreadPatchField 是过渡期嵌入源通知的 thread patch 字段名。
const embeddedThreadPatchField = "_threadPatch"

// embedThreadPatchRequests 将匹配的 ui/thread/patch 兼容副本嵌入源通知。
// 独立 patch 通知仍保留，直到前端完全消费 _threadPatch 字段。
func embedThreadPatchRequests(reqs []pushRequest) []pushRequest {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]pushRequest, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, embedThreadPatchRequest(req, out))
	}
	return out
}

// embedThreadPatchRequest 在单个 push batch 中尝试嵌入 thread patch。
func embedThreadPatchRequest(req pushRequest, previous []pushRequest) pushRequest {
	next := req
	next.notifications = make([]eventsurface.Notification, 0, len(req.notifications))
	for _, notification := range req.notifications {
		if isThreadPatchNotification(notification.Method) {
			if patch, ok := clonePayloadMap(notification.Payload); ok {
				embedThreadPatch(previous, next.notifications, patch)
			}
		}
		next.notifications = append(next.notifications, notification)
	}
	return next
}

// embedThreadPatch 查找唯一源通知并写入 thread patch 副本。
func embedThreadPatch(previous []pushRequest, current []eventsurface.Notification, patch map[string]any) bool {
	patchIdentities := payloadIdentitiesFrom(patch)
	if patchIdentities.empty() {
		return false
	}
	source := payloadString(patch, "source")
	if source == "" {
		return false
	}
	target, ok := uniqueThreadPatchTarget(previous, current, patchIdentities, source)
	if !ok {
		return false
	}
	target.payload[embeddedThreadPatchField] = patch
	target.notification.Payload = target.payload
	return true
}

// threadPatchTarget 表示可被嵌入 patch 的通知及其可修改 payload 副本。
type threadPatchTarget struct {
	notification *eventsurface.Notification
	payload      map[string]any
}

// uniqueThreadPatchTarget 在当前和已处理 batch 中查找唯一可嵌入目标。
// 找到多个候选或目标已占用时返回 false，避免把 patch 塞到错误通知。
func uniqueThreadPatchTarget(previous []pushRequest, current []eventsurface.Notification, identities payloadIdentities, source string) (threadPatchTarget, bool) {
	var target threadPatchTarget
	found := false
	visit := func(notification *eventsurface.Notification) bool {
		candidate, occupied, ok := matchingThreadPatchTarget(notification, identities, source)
		if !ok {
			return true
		}
		if !found && occupied {
			return false
		}
		if occupied {
			return true
		}
		if found {
			target = threadPatchTarget{}
			return false
		}
		target = candidate
		found = true
		return true
	}
	if !visitThreadPatchTargets(current, visit) {
		return threadPatchTarget{}, false
	}
	for i := len(previous) - 1; i >= 0; i-- {
		if !visitThreadPatchTargets(previous[i].notifications, visit) {
			return threadPatchTarget{}, false
		}
	}
	return target, found
}

// visitThreadPatchTargets 从后往前遍历通知，优先匹配最近的源事件。
func visitThreadPatchTargets(notifications []eventsurface.Notification, visit func(*eventsurface.Notification) bool) bool {
	for i := len(notifications) - 1; i >= 0; i-- {
		if !visit(&notifications[i]) {
			return false
		}
	}
	return true
}

// matchingThreadPatchTarget 判断单个通知是否是当前 patch 的嵌入目标。
func matchingThreadPatchTarget(notification *eventsurface.Notification, identities payloadIdentities, source string) (threadPatchTarget, bool, bool) {
	if notification == nil || !isThreadPatchEmbedTarget(notification.Method) {
		return threadPatchTarget{}, false, false
	}
	if !threadPatchSourceMatchesMethod(source, notification.Method) {
		return threadPatchTarget{}, false, false
	}
	payload, ok := clonePayloadMap(notification.Payload)
	if !ok || !payloadIdentitiesMatch(payloadIdentitiesFrom(payload), identities) {
		return threadPatchTarget{}, false, false
	}
	if _, exists := payload[embeddedThreadPatchField]; exists {
		return threadPatchTarget{notification: notification, payload: payload}, true, true
	}
	return threadPatchTarget{notification: notification, payload: payload}, false, true
}

// isThreadPatchNotification 判断通知是否为独立 thread patch。
func isThreadPatchNotification(method string) bool {
	return strings.TrimSpace(method) == eventsurface.MethodUIThreadPatch
}

// isThreadPatchEmbedTarget 判断方法是否允许承载嵌入的 thread patch。
func isThreadPatchEmbedTarget(method string) bool {
	switch strings.TrimSpace(method) {
	case "", eventsurface.MethodUIThreadPatch, eventsurface.MethodUIThreadChanged, eventsurface.MethodUISidebarChanged:
		return false
	default:
		return true
	}
}

// threadPatchSourceMatchesMethod 判断 patch source 是否对应目标通知方法。
func threadPatchSourceMatchesMethod(source, method string) bool {
	sourceKey := normalizedPatchSourceKey(source)
	methodKey := normalizedPatchSourceKey(method)
	if sourceKey == "" || methodKey == "" {
		return false
	}
	if sourceKey == methodKey || compactPatchSourceKey(sourceKey) == compactPatchSourceKey(methodKey) {
		return true
	}
	for _, alias := range patchSourceMethodAliases[sourceKey] {
		aliasKey := normalizedPatchSourceKey(alias)
		if methodKey == aliasKey || compactPatchSourceKey(methodKey) == compactPatchSourceKey(aliasKey) {
			return true
		}
	}
	return false
}

// patchSourceMethodAliases 维护 patch source 与事件方法之间的兼容别名。
var patchSourceMethodAliases = map[string][]string{
	"turn/outputdelta": {
		eventsurface.MethodAgentMessageDelta,
		eventsurface.MethodTurnOutputDelta,
	},
	"tool/call": {
		eventsurface.MethodToolCall,
	},
	"tool/completed": {
		eventsurface.MethodItemCompleted,
	},
	"tool/approvalrequested": {
		eventsurface.MethodCommandApprovalRequested,
		eventsurface.MethodFileApprovalRequested,
		eventsurface.MethodSkillApprovalRequested,
	},
	"tool/approvalresolved": {
		eventsurface.MethodApprovalResolved,
	},
	"tool/diffupdated": {
		"turn/diff/updated",
	},
	"agent/statechanged": {
		eventsurface.MethodUIStateChanged,
	},
}

// normalizedPatchSourceKey 标准化 patch source 或方法名。
func normalizedPatchSourceKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// compactPatchSourceKey 移除分隔符，用于兼容不同命名风格。
func compactPatchSourceKey(value string) string {
	replacer := strings.NewReplacer("/", "", "_", "", "-", "", " ", "")
	return replacer.Replace(normalizedPatchSourceKey(value))
}

// clonePayloadMap 将任意 JSON-like payload 复制为 map，避免修改原对象。
func clonePayloadMap(payload any) (map[string]any, bool) {
	switch typed := payload.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return cloneStringAnyMap(typed), true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

// cloneStringAnyMap 浅复制 string-any map。
func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// payloadIdentities 提取用于匹配 patch 归属的 thread/agent 身份集合。
type payloadIdentities struct {
	threadIDs map[string]struct{}
	agentIDs  map[string]struct{}
}

// payloadIdentitiesFrom 从 payload 中读取 threadID 和 agentID 身份。
func payloadIdentitiesFrom(payload map[string]any) payloadIdentities {
	return payloadIdentities{
		threadIDs: payloadIdentitySet(payload, "threadId", "thread_id"),
		agentIDs:  payloadIdentitySet(payload, "agentId", "agent_id"),
	}
}

// empty 判断身份集合是否完全为空。
func (ids payloadIdentities) empty() bool {
	return len(ids.threadIDs) == 0 && len(ids.agentIDs) == 0
}

// payloadIdentitySet 从多个候选 key 中收集非空身份值。
func payloadIdentitySet(payload map[string]any, keys ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, key := range keys {
		if text := payloadString(payload, key); text != "" {
			out[text] = struct{}{}
		}
	}
	return out
}

// payloadIdentitiesMatch 优先按 threadID 匹配，缺失 threadID 时才按 agentID 匹配。
func payloadIdentitiesMatch(source, patch payloadIdentities) bool {
	if payloadIdentitySetsIntersect(source.threadIDs, patch.threadIDs) {
		return true
	}
	return len(source.threadIDs) == 0 &&
		len(patch.threadIDs) == 0 &&
		payloadIdentitySetsIntersect(source.agentIDs, patch.agentIDs)
}

// payloadIdentitySetsIntersect 判断两个身份集合是否有交集。
func payloadIdentitySetsIntersect(left, right map[string]struct{}) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	if len(left) > len(right) {
		left, right = right, left
	}
	for key := range left {
		if _, ok := right[key]; ok {
			return true
		}
	}
	return false
}

// payloadString 从 payload 中读取并裁剪字符串字段。
func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	text, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
