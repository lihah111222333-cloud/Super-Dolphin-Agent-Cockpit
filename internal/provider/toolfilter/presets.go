package toolfilter

import (
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolpolicy"
)

var workerDeniedTools = []string{
	"orchestration_launch_agent", "orchestration_send_message",
	"orchestration_stop_agent", "orchestration_list_agents",
	"orchestration_get_agent_report",
}

// ReviewerDecision 返回审查 agent 的只读工具白名单。
// 允许 LSP/文件读取类工具，显式拒绝 writer、workflow/process、planning、递归 agent 和 connector 工具。
func ReviewerDecision() mcp.BeforeDecision {
	return mcp.BeforeDecision{
		Decision:     mcp.HookDecisionAllow,
		AllowedTools: reviewerAllowedTools(),
		DeniedTools:  reviewerDeniedTools(),
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

// reviewerToolCandidate 是 reviewer preset 交给 toolpolicy 判断的最小输入。
// preset 只维护候选工具名和能力标签，是否进入 allow/deny surface 由 toolpolicy 决定。
type reviewerToolCandidate struct {
	name              string
	trust             toolpolicy.TrustSource
	capabilities      toolpolicy.Capability
	readOnlyHint      bool
	readOnlyHintTrust toolpolicy.TrustSource
}

func reviewerAllowedTools() []string {
	return reviewerPolicyAllowedTools([]reviewerToolCandidate{
		trustedReadOnlyTool("file"),
		trustedReadOnlyTool("grep"),
		trustedReadOnlyTool("inspect"),
		trustedReadOnlyTool("xref"),
		trustedReadOnlyTool("structure"),
		trustedReadOnlyTool("completion"),
		trustedReadOnlyTool("lsp_file"),
		trustedReadOnlyTool("lsp_grep"),
		trustedReadOnlyTool("lsp_inspect"),
		trustedReadOnlyTool("lsp_xref"),
		trustedReadOnlyTool("lsp_structure"),
		trustedReadOnlyTool("lsp_completion"),
		trustedReadOnlyTool("shared_file_read"),
	})
}

// reviewerDeniedTools 返回 reviewer 必须精确拒绝的工具名。
// DeniedTools 没有前缀匹配语义，新增工具族时必须在这里列出真实工具名。
func reviewerDeniedTools() []string {
	return reviewerPolicyDeniedTools([]reviewerToolCandidate{
		deniedReviewerTool("edit", toolpolicy.CapabilityWriter),
		deniedReviewerTool("lsp_edit", toolpolicy.CapabilityWriter),
		deniedReviewerTool("shared_file_write", toolpolicy.CapabilityWriter),
		deniedReviewerTool("memory_write", toolpolicy.CapabilityMemoryMutation),
		deniedReviewerTool("task_create_dag", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("task_dag_apply_ops", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("task_update_node", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("task_dispatch_node", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("task_start_dag", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("task_terminate_dag", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("task_delete_dag", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("task_workflow_recovery_action", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("workspace_create_run", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityWriter),
		deniedReviewerTool("workspace_merge_run", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityWriter),
		deniedReviewerTool("workspace_abort_run", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("workflow_template_save", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityWriter),
		deniedReviewerTool("workflow_template_rollback", toolpolicy.CapabilityWorkflowMutation|toolpolicy.CapabilityWriter),
		deniedReviewerTool("wait", toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("bash_output", toolpolicy.CapabilityShell),
		deniedReviewerTool("BashOutput", toolpolicy.CapabilityShell),
		deniedReviewerTool("update_plan", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("todo_write", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("TodoWrite", toolpolicy.CapabilityWorkflowMutation),
		deniedReviewerTool("complete_step", toolpolicy.CapabilityApprovalFinalizer),
		deniedReviewerTool("launch_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("send_message", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("stop_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("recover_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("interrupt_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("list_agents", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("get_agent_report", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("get_agent_reports", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("orchestration_launch_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("orchestration_send_message", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("orchestration_stop_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("orchestration_recover_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("orchestration_interrupt_agent", toolpolicy.CapabilityRecursiveAgent|toolpolicy.CapabilityProcessControl),
		deniedReviewerTool("orchestration_list_agents", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("orchestration_get_agent_report", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("orchestration_get_agent_reports", toolpolicy.CapabilityRecursiveAgent),
		deniedReviewerTool("connect_tool_source", toolpolicy.CapabilityConnector),
	})
}

func trustedReadOnlyTool(name string) reviewerToolCandidate {
	return reviewerToolCandidate{
		name:         name,
		trust:        toolpolicy.TrustInternal,
		capabilities: toolpolicy.CapabilityReadOnly,
	}
}

func deniedReviewerTool(name string, capabilities toolpolicy.Capability) reviewerToolCandidate {
	return reviewerToolCandidate{
		name:         name,
		trust:        toolpolicy.TrustInternal,
		capabilities: capabilities,
	}
}

func reviewerPolicyAllowedTools(candidates []reviewerToolCandidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if reviewerToolPolicyDecision(candidate).Allow {
			names = append(names, candidate.name)
		}
	}
	return names
}

func reviewerPolicyDeniedTools(candidates []reviewerToolCandidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !reviewerToolPolicyDecision(candidate).Allow {
			names = append(names, candidate.name)
		}
	}
	return names
}

func reviewerToolPolicyDecision(candidate reviewerToolCandidate) toolpolicy.Decision {
	return toolpolicy.Decide(toolpolicy.Assessment{
		Stage:             toolpolicy.StageReadOnly,
		Trust:             candidate.trust,
		Capabilities:      candidate.capabilities,
		ReadOnlyHint:      candidate.readOnlyHint,
		ReadOnlyHintTrust: candidate.readOnlyHintTrust,
	})
}
