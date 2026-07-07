package contracttest

// AcceptanceCriterion 标识新 provider 必须满足的机器验收项。
type AcceptanceCriterion = CaseKey

const (
	// AcceptanceEventTranslation 要求 provider 声明关键事件翻译矩阵。
	AcceptanceEventTranslation AcceptanceCriterion = CaseEventMatrix
	// AcceptancePromptSnapshotParity 要求 provider 证明 prompt snapshot parity。
	AcceptancePromptSnapshotParity AcceptanceCriterion = CasePromptParity
	// AcceptancePromptMaterialized 要求只暴露物化 prompt carrier 的 provider 证明 materialized payload。
	AcceptancePromptMaterialized AcceptanceCriterion = CasePromptMaterializedCarrier
	// AcceptanceApproval 要求 provider 证明 approval bridge 或 approval policy。
	AcceptanceApproval AcceptanceCriterion = CaseApproval
	// AcceptanceInterrupt 要求 provider 证明 interrupt 行为。
	AcceptanceInterrupt AcceptanceCriterion = CaseInterrupt
	// AcceptanceForceComplete 要求 provider 证明 force-complete 行为。
	AcceptanceForceComplete AcceptanceCriterion = CaseForceComplete
	// AcceptanceResume 要求 provider 证明 resume 使用 provider identity。
	AcceptanceResume AcceptanceCriterion = CaseResume
	// AcceptanceToolbridge 要求 provider 证明 toolbridge 或 provider-native tool governance。
	AcceptanceToolbridge AcceptanceCriterion = CaseToolbridge
	// AcceptanceRuntimeReport 要求 provider 证明 runtime report 行为。
	AcceptanceRuntimeReport AcceptanceCriterion = CaseRuntimeReport
)

// RequiredAcceptanceCriteria 从既有 CaseKey 切片投影当前 spec 声明的验收项。
func RequiredAcceptanceCriteria(spec Spec) []AcceptanceCriterion {
	promptCriteria := declaredPromptAcceptanceCriteria(spec.RequiredCases)
	criteria := make([]AcceptanceCriterion, 0, len(requiredCaseOrder)+len(promptCriteria))
	for _, key := range requiredCaseOrder {
		criteria = append(criteria, AcceptanceCriterion(key))
		if key == CaseEventMatrix {
			criteria = append(criteria, promptCriteria...)
		}
	}
	return criteria
}

// ValidateAcceptanceSpec 校验 provider contract spec 是否覆盖机器验收面。
func ValidateAcceptanceSpec(spec Spec) error {
	if err := validatePromptCaseAlternative(spec.RequiredCases); err != nil {
		return err
	}
	return validateRequiredCaseSet(spec.RequiredCases)
}

func declaredPromptAcceptanceCriteria(cases map[CaseKey]Case) []AcceptanceCriterion {
	criteria := make([]AcceptanceCriterion, 0, len(promptCaseAlternatives))
	for _, key := range promptCaseAlternatives {
		if _, ok := cases[key]; ok {
			criteria = append(criteria, AcceptanceCriterion(key))
		}
	}
	return criteria
}
