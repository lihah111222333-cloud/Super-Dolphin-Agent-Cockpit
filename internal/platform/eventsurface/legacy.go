package eventsurface

import (
	"encoding/json"
	"fmt"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	MethodUIThreadChanged  = "ui/thread/changed"
	MethodUISidebarChanged = "ui/sidebar/changed"
)

// Notification describes one UI event method and payload emitted from the event surface.
type Notification struct {
	Method  string
	Payload any
}

// ExpandNotifications 处理expandnotifications。
func ExpandNotifications(method string, payload any) []Notification {
	method = strings.TrimSpace(method)
	if method == "" {
		return nil
	}
	out := []Notification{{Method: method, Payload: payload}}
	return append(out, legacyRefreshNotifications(method, payloadMap(payload))...)
}

func legacyRefreshNotifications(method string, payload map[string]any) []Notification {
	thread := shouldEmitThreadRefresh(method, payload)
	sidebar := shouldEmitSidebarRefresh(method, payload)
	if !thread && !sidebar {
		return nil
	}
	refreshPayload := buildRefreshPayload(method, payload)
	out := make([]Notification, 0, 2)
	if thread {
		out = append(out, Notification{Method: MethodUIThreadChanged, Payload: refreshPayload})
	}
	if sidebar {
		out = append(out, Notification{Method: MethodUISidebarChanged, Payload: refreshPayload})
	}
	return out
}

func shouldEmitThreadRefresh(method string, payload map[string]any) bool {
	if suppressLegacyRefresh(method) {
		return false
	}
	if isWorkspaceRunMethod(method) {
		return false
	}
	return refreshIdentity(payload) != ""
}

func shouldEmitSidebarRefresh(method string, payload map[string]any) bool {
	if suppressLegacyRefresh(method) {
		return false
	}
	if isWorkspaceRunMethod(method) {
		return true
	}
	return refreshIdentity(payload) != ""
}

func isWorkspaceRunMethod(method string) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	return strings.HasPrefix(method, "workspace/run/")
}

func suppressLegacyRefresh(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case MethodThreadTokenUsage,
		MethodThreadCompacted,
		MethodUIThreadPatch,
		"item/agentmessage/delta",
		"item/reasoning/textdelta",
		"item/commandexecution/outputdelta",
		"turn/output/delta":
		return true
	default:
		return false
	}
}

func buildRefreshPayload(method string, payload map[string]any) map[string]any {
	out := map[string]any{"source": strings.TrimSpace(method)}
	if threadID := firstRefreshField(payload, "threadId", "thread_id"); threadID != "" {
		out["threadId"] = threadID
	}
	if agentID := firstRefreshField(payload, "agent_id", "agentId"); agentID != "" {
		out["agent_id"] = agentID
	}
	return out
}

func refreshIdentity(payload map[string]any) string {
	return firstRefreshField(payload, "threadId", "thread_id", "agent_id", "agentId")
}

func firstRefreshField(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := refreshString(payload[key]); text != "" {
			return text
		}
	}
	return ""
}

func refreshString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func payloadMap(payload any) map[string]any {
	switch typed := payload.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return typed
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		pkglogger.Warn("eventsurface: payloadMap marshal failed, dropping legacy refresh payload",
			pkglogger.String("payload_type", fmt.Sprintf("%T", payload)),
			pkglogger.String("error", err.Error()),
		)
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		pkglogger.Warn("eventsurface: payloadMap unmarshal failed, dropping legacy refresh payload",
			pkglogger.String("payload_type", fmt.Sprintf("%T", payload)),
			pkglogger.String("error", err.Error()),
		)
		return map[string]any{}
	}
	return out
}
