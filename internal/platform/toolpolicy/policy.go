package toolpolicy

// Stage 表示工具策略能识别的调用阶段；缺少权威阶段时必须用 Unknown 让调用方显式拒绝。
type Stage string

const (
	StageUnknown   Stage = ""
	StagePlan      Stage = "plan"
	StageReadOnly  Stage = "read_only"
	StageExecution Stage = "execution"
)

// TrustSource 表示工具分类信息来自哪里，外部自报和未知来源都不能作为放行依据。
type TrustSource string

const (
	TrustUnknown  TrustSource = ""
	TrustInternal TrustSource = "internal"
	TrustProvider TrustSource = "provider"
	TrustExternal TrustSource = "external"
)

// Capability 是工具能力的位集合；只描述策略原语，不承载 provider sandbox 或 MCP 生命周期状态。
type Capability uint64

const (
	CapabilityReadOnly Capability = 1 << iota
	CapabilityPlanSafe
	CapabilityWriter
	CapabilityProcessControl
	CapabilityMemoryMutation
	CapabilityApprovalFinalizer
	CapabilityWorkflowMutation
	CapabilityRecursiveAgent
	CapabilityConnector
	CapabilityShell
)

const readOnlyUnsafeCapabilities = CapabilityWriter |
	CapabilityProcessControl |
	CapabilityMemoryMutation |
	CapabilityApprovalFinalizer |
	CapabilityWorkflowMutation |
	CapabilityRecursiveAgent |
	CapabilityConnector |
	CapabilityShell

// Has 判断能力集合是否包含指定能力位。
func (c Capability) Has(flag Capability) bool {
	return c&flag == flag
}

// HasAny 判断能力集合是否包含任一指定能力位。
func (c Capability) HasAny(flags Capability) bool {
	return c&flags != 0
}

// ReadOnly 判断工具能力是否可被只读阶段接受；PlanSafe 在没有冲突能力时天然属于只读。
func (c Capability) ReadOnly() bool {
	if c.HasAny(readOnlyUnsafeCapabilities) {
		return false
	}
	return c.Has(CapabilityReadOnly) || c.Has(CapabilityPlanSafe)
}

// PlanSafe 判断工具能力是否可进入规划阶段；它比普通只读更窄，避免审批终结和状态变更混入。
func (c Capability) PlanSafe() bool {
	if c.HasAny(readOnlyUnsafeCapabilities) {
		return false
	}
	return c.Has(CapabilityPlanSafe)
}

// DecisionCode 是稳定的策略结果码，调用方可用它做审计和错误映射。
type DecisionCode string

const (
	CodeAllowed             DecisionCode = "allow"
	CodeUnknownStage        DecisionCode = "unknown_stage"
	CodeUntrustedSource     DecisionCode = "untrusted_source"
	CodeExternalHint        DecisionCode = "external_hint_untrusted"
	CodeCapabilityDenied    DecisionCode = "capability_denied"
	CodeShellSyntaxDenied   DecisionCode = "shell_syntax_denied"
	CodeShellCommandDenied  DecisionCode = "shell_command_denied"
	CodeShellArgumentDenied DecisionCode = "shell_argument_denied"
	CodeShellEmptyCommand   DecisionCode = "shell_empty_command"
)

// Decision 表示一次纯策略判断结果；拒绝结果必须带稳定 code 和可读 reason。
type Decision struct {
	Allow  bool
	Code   DecisionCode
	Reason string
}

// Assessment 是工具阶段、信任来源和能力集合的最小策略输入。
type Assessment struct {
	Stage             Stage
	Trust             TrustSource
	Capabilities      Capability
	ReadOnlyHint      bool
	ReadOnlyHintTrust TrustSource
}

// Decide 根据阶段、信任来源和能力集合做 fail-closed 判断。
// 它不读取运行时 stage，不替代 provider sandbox、MCP 生命周期或审批策略。
func Decide(assessment Assessment) Decision {
	if !knownStage(assessment.Stage) {
		return deny(CodeUnknownStage, "toolpolicy: stage is unknown")
	}
	if !trustedSource(assessment.Trust) {
		return deny(CodeUntrustedSource, "toolpolicy: trust source is unknown or external")
	}
	if assessment.ReadOnlyHint && assessment.ReadOnlyHintTrust != TrustInternal {
		return deny(CodeExternalHint, "toolpolicy: external read-only hint is not trusted")
	}

	switch assessment.Stage {
	case StagePlan:
		if !assessment.Capabilities.PlanSafe() {
			return deny(CodeCapabilityDenied, "toolpolicy: plan stage requires plan-safe read-only capability")
		}
	case StageReadOnly:
		if !assessment.Capabilities.ReadOnly() {
			return deny(CodeCapabilityDenied, "toolpolicy: read-only stage requires read-only capability")
		}
	case StageExecution:
		return allow("toolpolicy: execution stage leaves write gating to runtime owners")
	}
	return allow("toolpolicy: capability allowed for stage")
}

func knownStage(stage Stage) bool {
	switch stage {
	case StagePlan, StageReadOnly, StageExecution:
		return true
	default:
		return false
	}
}

func trustedSource(source TrustSource) bool {
	switch source {
	case TrustInternal, TrustProvider:
		return true
	default:
		return false
	}
}

func allow(reason string) Decision {
	return Decision{Allow: true, Code: CodeAllowed, Reason: reason}
}

func deny(code DecisionCode, reason string) Decision {
	return Decision{Code: code, Reason: reason}
}
