package policy

// Decision 是 Policy Gate 对一次敏感操作给出的结果。
type Decision string

const (
	// DecisionAllow 表示操作满足当前最小权限和风险约束。
	DecisionAllow Decision = "allow"
	// DecisionDeny 表示调用方权限不足，操作应被拒绝。
	DecisionDeny Decision = "deny"
	// DecisionFailFast 表示请求本身违反安全边界，必须立即失败且不能降级。
	DecisionFailFast Decision = "fail-fast"
)

// RiskClass 描述操作风险等级。命令执行、共享文件写入和 workflow 写操作必须显式标高危。
type RiskClass string

const (
	RiskLow    RiskClass = "low"
	RiskMedium RiskClass = "medium"
	RiskHigh   RiskClass = "high"
)

// Permission 是 Policy Gate 使用的最小权限标签。
type Permission string

const (
	PermissionWorkflowWrite            Permission = "workflow.write"
	PermissionSharedFileWrite          Permission = "shared_file.write"
	PermissionCommandExecute           Permission = "command.execute"
	PermissionProviderIdentityOverride Permission = "provider_identity.override"
)

// Operation 是 Policy Gate 当前覆盖的敏感操作集合。
type Operation string

const (
	OperationWorkflowWrite            Operation = "workflow_write"
	OperationCommandExecution         Operation = "command_execution"
	OperationSharedFileWrite          Operation = "shared_file_write"
	OperationProviderIdentityOverride Operation = "provider_identity_override"
)

// Request 是一次策略判定的最小上下文。审批 MVP 未实现前 ApprovalRequired 只能触发 fail-fast。
type Request struct {
	Operation        Operation
	RiskClass        RiskClass
	Permission       Permission
	ApprovalRequired bool
}

// Result 携带策略判定和给日志/调用方看的短原因，避免把底层细节暴露给外部。
type Result struct {
	Decision Decision
	Reason   string
}

// Decide 对 workflow 写、命令执行、sharedfile 写和 provider 身份覆盖做 fail-closed 判定。
func Decide(req Request) Result {
	if req.ApprovalRequired {
		return failFast("approval flow is not available")
	}
	if req.Operation == "" || req.RiskClass == "" || req.Permission == "" {
		return failFast("policy request is incomplete")
	}
	switch req.Operation {
	case OperationWorkflowWrite:
		return requireHighRiskPermission(req, PermissionWorkflowWrite)
	case OperationCommandExecution:
		if req.RiskClass != RiskHigh {
			return failFast("command execution requires high risk policy")
		}
		return requirePermission(req, PermissionCommandExecute)
	case OperationSharedFileWrite:
		return requireHighRiskPermission(req, PermissionSharedFileWrite)
	case OperationProviderIdentityOverride:
		return requireHighRiskPermission(req, PermissionProviderIdentityOverride)
	default:
		return failFast("unknown policy operation")
	}
}

func requireHighRiskPermission(req Request, permission Permission) Result {
	if req.RiskClass != RiskHigh {
		return failFast("operation requires high risk policy")
	}
	return requirePermission(req, permission)
}

func requirePermission(req Request, permission Permission) Result {
	if req.Permission != permission {
		return Result{Decision: DecisionDeny, Reason: "permission denied"}
	}
	return Result{Decision: DecisionAllow, Reason: "policy allowed"}
}

func failFast(reason string) Result {
	return Result{Decision: DecisionFailFast, Reason: reason}
}
