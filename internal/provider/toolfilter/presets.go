package toolfilter

import mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"

var reviewerAllowedTools = []string{
	"file", "grep", "inspect", "xref", "structure", "completion",
	"lsp_file", "lsp_grep", "lsp_inspect", "lsp_xref", "lsp_structure", "lsp_completion",
	"shared_file_read",
}

var reviewerDeniedTools = []string{
	"edit", "lsp_edit",
	"orchestration_launch_agent", "orchestration_stop_agent",
}

var workerDeniedTools = []string{
	"orchestration_launch_agent", "orchestration_send_message",
	"orchestration_stop_agent", "orchestration_list_agents",
	"orchestration_get_agent_report",
}

// ReviewerDecision returns a read-only tool preset for reviewer agents.
// Wiring (next phase):
// 1. Subscribe agent scope with HookRegistry.Subscribe("agent.tool.before", agentScope).
// 2. Return ReviewerDecision() from the peer callback handler.
// 3. Hook merge then intersects AllowedTools and unions DeniedTools automatically.
// ReviewerDecision 处理reviewerdecision。
func ReviewerDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:     mcp.HookDecisionAllow,
		AllowedTools: append([]string(nil), reviewerAllowedTools...),
		DeniedTools:  append([]string(nil), reviewerDeniedTools...),
	}
}

// WorkerDecision returns a preset that blocks orchestration tools for worker agents.
// WorkerDecision 处理workerdecision。
func WorkerDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:    mcp.HookDecisionAllow,
		DeniedTools: append([]string(nil), workerDeniedTools...),
	}
}

// FullAccessDecision returns an unrestricted preset.
// FullAccessDecision 处理fullaccessdecision。
func FullAccessDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}
}
