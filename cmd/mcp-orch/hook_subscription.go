// Package main 是 mcp-orch 的入口，负责初始化运行时环境并启动编排进程。
package main

import (
	"context"
	"encoding/json"
	"strings"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// orchestrationHookSubscriptionID 是 mcp-orch 注册到主控的固定订阅标识。
const orchestrationHookSubscriptionID = "mcp-orch-agent-lifecycle"

// orchestrationHookTopics 是编排服务需要消费的 agent 生命周期事件集合。
var orchestrationHookTopics = []string{
	"agent.session.start",
	"agent.state.change",
	"agent.turn.after",
	"agent.turn.failed",
	"agent.turn.progress",
	"agent.process.exit",
}

// hookSubscriber 是 bootstrap client 订阅 hook 的窄接口，便于测试替换。
type hookSubscriber interface {
	SubscribeHooks(context.Context, string, []string, mcp.Selector, json.RawMessage, string) (*mcp.HookSubscribeResponse, error)
}

// subscribeOrchestrationHooks 向主控注册编排生命周期 hook 订阅。
// client 为 nil 或 topic 列表为空时直接返回，保证独立模式不被 hook 订阅阻断。
func subscribeOrchestrationHooks(ctx context.Context, client hookSubscriber) error {
	if client == nil {
		return nil
	}
	topics := make([]string, 0, len(orchestrationHookTopics))
	for _, topic := range orchestrationHookTopics {
		if topic = strings.TrimSpace(topic); topic != "" {
			topics = append(topics, topic)
		}
	}
	if len(topics) == 0 {
		return nil
	}
	_, err := client.SubscribeHooks(ctx, orchestrationHookSubscriptionID, topics, mcp.Selector{}, nil, "sync")
	return err
}
