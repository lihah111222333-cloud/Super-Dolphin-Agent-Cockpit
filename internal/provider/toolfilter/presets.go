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

// ReviewerDecision 返回审查 agent 的只读工具白名单。
// 允许 LSP/文件读取类工具，显式拒绝编辑和 orchestration 生命周期工具，避免 review 角色越权修改。
func ReviewerDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:     mcp.HookDecisionAllow,
		AllowedTools: append([]string(nil), reviewerAllowedTools...),
		DeniedTools:  append([]string(nil), reviewerDeniedTools...),
	}
}

// WorkerDecision 返回 worker agent 的工具限制策略。
// 默认允许普通工具，但拒绝 orchestration 控制类工具，防止 worker 自行拉起或操控其他 agent。
func WorkerDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:    mcp.HookDecisionAllow,
		DeniedTools: append([]string(nil), workerDeniedTools...),
	}
}

// FullAccessDecision 返回不追加限制的允许决策。
// 该 preset 只用于明确受信任的控制面，调用方仍可在外层叠加 hook 规则。
func FullAccessDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}
}
