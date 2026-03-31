package codexapp

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

func agentSessionHeader(payload map[string]any) shared.AgentSessionHeader {
	agentID := stringValue(payload, "agentId", "agent_id")
	// Compatibility fix made during P1 execution to satisfy the existing
	// agentSessionHeader test; this is not part of the original P1 plan scope.
	threadID := firstNonEmpty(agentID, stringValue(payload, "threadId", "thread_id"), stringValue(nestedValue(payload, "thread"), "id"))
	return shared.AgentSessionHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{
				EventHeader: shared.EventHeader{Timestamp: eventTime(payload)},
				ThreadID:    threadID,
			},
			AgentID: agentID,
		},
		SessionID: firstNonEmpty(stringValue(payload, "sessionId", "session_id"), threadID),
	}
}

func turnHeader(payload map[string]any) shared.TurnHeader {
	return shared.TurnHeader{
		AgentHeader: agentSessionHeader(payload).AgentHeader,
		TurnIDHeader: shared.TurnIDHeader{
			TurnID: firstNonEmpty(
				stringValue(payload, "turnId", "turn_id"),
				stringValue(nestedValue(payload, "turn"), "id"),
			),
		},
	}
}

func toolCallHeader(payload map[string]any) shared.ToolCallHeader {
	return shared.ToolCallHeader{
		TurnHeader: turnHeader(payload),
		CallID:     firstNonEmpty(stringValue(payload, "callId", "call_id"), stringValue(nestedValue(payload, "item"), "callId")),
		ToolName:   firstNonEmpty(stringValue(payload, "toolName", "tool_name", "tool"), stringValue(nestedValue(payload, "item"), "toolName", "tool")),
	}
}

func toolApprovalHeader(payload map[string]any) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: toolCallHeader(payload),
		ApprovalID:     stringValue(payload, "approvalId", "approval_id"),
	}
}
