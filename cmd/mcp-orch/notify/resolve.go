// Package notify 提供 mcp-orch 侧通知装配。
// 它复用 internal/platform/notify 的传输和安全逻辑，但在 orch 进程内独立提供 notifier、flusher 和 bus 订阅。
// 这样编排进程不需要 import core module 的通知模块，也能共享同一套渠道解析与 webhook 防护。
package notify

import (
	"encoding/json"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
)

// terminalNodeStatuses 列出值得发送通知的 taskdag 终态。
// running、pending 等中间态不发送，避免通知队列被不可行动的状态变更刷屏。
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

// isTerminalNodeStatus 归一化并判断目标状态是否为终态。
// 这里统一 trim/lower，避免存储层大小写差异泄漏到订阅器控制流。
func isTerminalNodeStatus(status string) bool {
	return terminalNodeStatuses[strings.ToLower(strings.TrimSpace(status))]
}

// resolveNodeAlias 按节点优先的顺序解析通知渠道别名：
//
//	node.config.notify_channel > dag.metadata.notify_channel > drop/error
//
// 返回空字符串表示丢弃该事件；DAG 通知不使用全局默认渠道，避免把用户 DAG 结果误发到不明确目标。
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

// extractNotifyChannel 从用户可编辑 JSON 对象里提取 notify_channel。
// 非对象、缺字段或解析失败都返回空字符串，表示该对象不参与通知路由；大小写和空白保持宽容。
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

// nodeTerminalTitle 为节点终态事件生成面向用户的短标题。
// 标题刻意保持短，适配钉钉/飞书这类只清晰展示前几十个字符的卡片头部。
func nodeTerminalTitle(ev taskdto.TaskNodeStatusChanged) string {
	node := strings.TrimSpace(ev.NodeKey)
	if node == "" {
		node = "(unnamed node)"
	}
	return "DAG node " + strings.ToLower(strings.TrimSpace(ev.NewStatus)) + ": " + node
}
