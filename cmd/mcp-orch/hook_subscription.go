package main

import (
	"context"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
)

const orchestrationHookSubscriptionID = "mcp-orch-agent-lifecycle"

var orchestrationHookTopics = []string{
	"agent.session.start",
	"agent.state.change",
	"agent.process.exit",
}

func subscribeOrchestrationHooks(ctx context.Context, client *bootstrap.Client) error {
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
