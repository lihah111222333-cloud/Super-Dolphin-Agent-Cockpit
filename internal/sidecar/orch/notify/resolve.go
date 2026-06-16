// Package notify hosts the orch-side notifier wiring. It reuses the
// shared platform library (internal/platform/notify) and provides its
// own notifier + flusher implementations with its own fx Provide set
// and bus subscribers so the dual-Fx tree rule from the P21 P2 plan
// is respected. The concrete Notifier / Flusher are defined locally
// to avoid importing internal/module/notify (mcp-service-convention
// S3.1).
package notify

import (
	"encoding/json"
	"strings"

	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/taskdag"
)

// terminalNodeStatuses lists the taskdag statuses that justify firing
// a notification. Non-terminal transitions (running, pending, ...) are
// ignored because they produce noise without actionable content.
var terminalNodeStatuses = map[string]bool{
	"done":      true,
	"succeeded": true,
	"completed": true,
	"failed":    true,
	"error":     true,
	"cancelled": true,
	"canceled":  true,
	"skipped":   true,
}

// isTerminalNodeStatus normalises and checks the transition target. We
// lowercase once so any storage-layer canonicalisation quirks don't
// leak into the subscriber's control flow.
func isTerminalNodeStatus(status string) bool {
	return terminalNodeStatuses[strings.ToLower(strings.TrimSpace(status))]
}

// resolveNodeAlias implements the P2 plan's strict alias precedence:
//
//	node.config.notify_channel > dag.metadata.notify_channel > drop/error
//
// Returning an empty string means "drop" — the scheduler must not fall
// back to NOTIFY_DEFAULT_CHANNEL for DAG events. Callers that get
// empty simply skip enqueue.
func resolveNodeAlias(node *taskdag.Node, dag *taskdag.DAG) string {
	if node != nil {
		if v := extractNotifyChannel(node.Config); v != "" {
			return v
		}
	}
	if dag != nil {
		if v := extractNotifyChannel(dag.Metadata); v != "" {
			return v
		}
	}
	return ""
}

// extractNotifyChannel decodes a JSON object and returns the string
// value at the "notify_channel" key. Non-object or missing keys
// produce empty. Kept permissive (tolerates trailing whitespace,
// mixed-case keys) because both node.Config and dag.Metadata are
// user-authored in practice.
// extractNotifyChannel 提取notifychannel。
func extractNotifyChannel(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for k, v := range m {
		if !strings.EqualFold(strings.TrimSpace(k), "notify_channel") {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		return strings.TrimSpace(s)
	}
	return ""
}

// nodeTerminalTitle builds a user-facing headline for a terminal node
// event. We keep the title short because Dingtalk / Feishu headers
// show only the first ~40 chars legibly.
func nodeTerminalTitle(ev taskdto.TaskNodeStatusChanged) string {
	node := strings.TrimSpace(ev.NodeKey)
	if node == "" {
		node = "(unnamed node)"
	}
	return "DAG node " + strings.ToLower(strings.TrimSpace(ev.NewStatus)) + ": " + node
}
