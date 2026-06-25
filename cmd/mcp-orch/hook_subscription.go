// Package main 是 mcp-orch 的入口，负责初始化运行时环境并启动编排进程。
package main

import (
	"context"
	"encoding/json"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const orchestrationHookSubscriptionID = "mcp-orch-agent-lifecycle"

var orchestrationHookTopics = []string{
	"agent.session.start",
	"agent.state.change",
	"agent.turn.after",
	"agent.turn.failed",
	"agent.turn.progress",
	"agent.process.exit",
}

// hookSubscriber 定义向主控订阅 hook 事件的接口。
type hookSubscriber interface {
	SubscribeHooks(context.Context, string, []string, mcp.Selector, json.RawMessage, string) (*mcp.HookSubscribeResponse, error)
}

// subscribeOrchestrationHooks 向主控注册编排生命周期 hook 订阅，client 为 nil 时直接返回。
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
