package tools

import (
	goldentest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
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
