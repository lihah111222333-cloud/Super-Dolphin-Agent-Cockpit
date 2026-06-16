package rpc

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
)

const embeddedThreadPatchField = "_threadPatch"

// embedThreadPatchRequests enriches matching source notifications with a
// compatibility copy of ui/thread/patch. The standalone patch notification is
// intentionally preserved until the frontend consumes _threadPatch directly.
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

// embedThreadPatchRequest 处理embed线程补丁请求。
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

type threadPatchTarget struct {
	notification *eventsurface.Notification
	payload      map[string]any
}

// uniqueThreadPatchTarget 处理unique线程补丁target。
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

func visitThreadPatchTargets(notifications []eventsurface.Notification, visit func(*eventsurface.Notification) bool) bool {
	for i := len(notifications) - 1; i >= 0; i-- {
		if !visit(&notifications[i]) {
			return false
		}
	}
	return true
}

// matchingThreadPatchTarget 处理matching线程补丁target。
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

func isThreadPatchNotification(method string) bool {
	return strings.TrimSpace(method) == eventsurface.MethodUIThreadPatch
}

func isThreadPatchEmbedTarget(method string) bool {
	switch strings.TrimSpace(method) {
	case "", eventsurface.MethodUIThreadPatch, eventsurface.MethodUIThreadChanged, eventsurface.MethodUISidebarChanged:
		return false
	default:
		return true
	}
}

// threadPatchSourceMatchesMethod 处理线程补丁sourcematchesmethod。
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

func normalizedPatchSourceKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func compactPatchSourceKey(value string) string {
	replacer := strings.NewReplacer("/", "", "_", "", "-", "", " ", "")
	return replacer.Replace(normalizedPatchSourceKey(value))
}

func clonePayloadMap(payload any) (map[string]any, bool) {
	switch typed := payload.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return cloneStringAnyMap(typed), true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return map[string]any{}, false
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}, false
	}
	return out, true
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type payloadIdentities struct {
	threadIDs map[string]struct{}
	agentIDs  map[string]struct{}
}

func payloadIdentitiesFrom(payload map[string]any) payloadIdentities {
	return payloadIdentities{
		threadIDs: payloadIdentitySet(payload, "threadId", "thread_id"),
		agentIDs:  payloadIdentitySet(payload, "agentId", "agent_id"),
	}
}

func (ids payloadIdentities) empty() bool {
	return len(ids.threadIDs) == 0 && len(ids.agentIDs) == 0
}

func payloadIdentitySet(payload map[string]any, keys ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, key := range keys {
		if text := payloadString(payload, key); text != "" {
			out[text] = struct{}{}
		}
	}
	return out
}

func payloadIdentitiesMatch(source, patch payloadIdentities) bool {
	if payloadIdentitySetsIntersect(source.threadIDs, patch.threadIDs) {
		return true
	}
	return len(source.threadIDs) == 0 &&
		len(patch.threadIDs) == 0 &&
		payloadIdentitySetsIntersect(source.agentIDs, patch.agentIDs)
}

// payloadIdentitySetsIntersect 处理载荷身份setsintersect。
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
