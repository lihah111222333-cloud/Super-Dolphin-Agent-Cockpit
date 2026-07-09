package tools

import (
	goldentest "github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func handleSendMessageWithStub(stub *goldentest.OrchestrationStub) ToolHandler {
	return HandleSendMessage(SendMessagePorts{
		Snapshots: stub,
		Reports:   stub,
		Turns:     stub,
	})
}

func handleListAgentsWithStub(stub *goldentest.OrchestrationStub) ToolHandler {
	return HandleListAgents(AgentListPorts{
		Snapshots: stub,
		Reports:   stub,
	})
}
