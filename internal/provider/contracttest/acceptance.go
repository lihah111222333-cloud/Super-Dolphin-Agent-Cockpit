package contracttest

import (
	"errors"
	"fmt"
	"strings"
)

// AcceptanceCriterion 是 provider scaffold 机器验收项，直接别名到既有 CaseKey。
type AcceptanceCriterion = CaseKey

const (
	// AcceptanceEventTranslation 要求 provider 声明事件翻译与事件矩阵证据。
	AcceptanceEventTranslation          AcceptanceCriterion = CaseEventMatrix
	AcceptanceEventMatrix               AcceptanceCriterion = CaseEventMatrix
	AcceptancePromptSnapshotParity      AcceptanceCriterion = CasePromptParity
	AcceptancePromptParity              AcceptanceCriterion = CasePromptParity
	AcceptancePromptMaterializedCarrier AcceptanceCriterion = CasePromptMaterializedCarrier
	AcceptancePromptMaterialized        AcceptanceCriterion = CasePromptMaterializedCarrier
	AcceptanceApproval                  AcceptanceCriterion = CaseApproval
	AcceptanceInterrupt                 AcceptanceCriterion = CaseInterrupt
	AcceptanceForceComplete             AcceptanceCriterion = CaseForceComplete
	AcceptanceResume                    AcceptanceCriterion = CaseResume
	AcceptanceToolbridge                AcceptanceCriterion = CaseToolbridge
	AcceptanceRuntimeReport             AcceptanceCriterion = CaseRuntimeReport
)

// RequiredAcceptanceCriteria 从 provider 已声明的 RequiredCases 投影验收清单。
// 新增 provider 只能通过现有 CaseKey 进入验收面，避免维护一份会漂移的平行注册表。
func RequiredAcceptanceCriteria(spec Spec) []AcceptanceCriterion {
	criteria := make([]AcceptanceCriterion, 0, len(requiredCaseOrder)+1)
	for _, key := range requiredCaseOrder {
		criteria = append(criteria, AcceptanceCriterion(key))
	}
	for _, key := range promptCaseAlternatives {
		if c, ok := spec.RequiredCases[key]; ok && strings.TrimSpace(c.Name) != "" && c.Run != nil {
			criteria = append(criteria, AcceptanceCriterion(key))
			break
		}
	}
	return criteria
}

// ValidateAcceptanceSpec 在执行 provider 行为前校验机器可验收清单。
// 它复用 suite 的必需 case 与 prompt 二选一规则，缺项时立即阻断 scaffold 通过。
func ValidateAcceptanceSpec(spec Spec) error {
	if err := validateAcceptanceEventTranslation(spec.EventCases); err != nil {
		return fmt.Errorf("provider acceptance criteria: %w", err)
	}
	if err := validateAcceptancePromptCaseAlternative(spec.RequiredCases); err != nil {
		return fmt.Errorf("provider acceptance criteria: %w", err)
	}
	if err := validateRequiredCaseSet(spec.RequiredCases); err != nil {
		return fmt.Errorf("provider acceptance criteria: %w", err)
	}
	return nil
}

func validateAcceptanceEventTranslation(cases []Case) error {
	if len(cases) == 0 {
		return errors.New("event_translation case is required")
	}
	if err := validateEventCases(cases); err != nil {
		return fmt.Errorf("event_translation case is incomplete: %w", err)
	}
	return nil
}

func validateAcceptancePromptCaseAlternative(cases map[CaseKey]Case) error {
	if err := validatePromptCaseAlternative(cases); err != nil {
		return fmt.Errorf("requires prompt_snapshot_parity or prompt_materialized_carrier: %w", err)
	}
	return nil
}
