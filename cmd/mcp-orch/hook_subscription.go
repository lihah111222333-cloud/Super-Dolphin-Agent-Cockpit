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

type hookSubscriber interface {
	SubscribeHooks(context.Context, string, []string, mcp.Selector, json.RawMessage, string) (*mcp.HookSubscribeResponse, error)
}

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
