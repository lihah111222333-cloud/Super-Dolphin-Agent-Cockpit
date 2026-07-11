package contracttest

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// OutcomeEvidence 通过真实动作或 typed unsupported 结果证明 provider 行为。
type OutcomeEvidence struct {
	ObservedActionID       string
	StateBefore            string
	StateAfter             string
	ExpectedDependencyName string
	DependencyName         string
	Profile                contract.DependencyProfile
	Unsupported            *UnsupportedOutcomeEvidence
}

// UnsupportedOutcomeEvidence 只能由 CaptureUnsupportedOutcome 从 provider 操作返回值生成。
type UnsupportedOutcomeEvidence struct {
	err            error
	dependencyName string
	profile        contract.DependencyProfile
	operationID    string
	captured       bool
	booleanOnly    bool
}

// CaptureUnsupportedOutcome 捕获真实 provider 操作返回的 dependency-mode error。
func CaptureUnsupportedOutcome(
	t testing.TB,
	operationID string,
	dependencyName string,
	profile contract.DependencyProfile,
	run func() error,
) *UnsupportedOutcomeEvidence {
	t.Helper()
	if strings.TrimSpace(operationID) == "" {
		t.Fatal("unsupported outcome operation id is required")
	}
	if run == nil {
		t.Fatal("unsupported outcome capture requires a provider operation")
	}
	unsupported := &UnsupportedOutcomeEvidence{
		err:            run(),
		dependencyName: strings.TrimSpace(dependencyName),
		profile:        profile,
		operationID:    strings.TrimSpace(operationID),
		captured:       true,
	}
	if err := validateUnsupportedOutcome(unsupported); err != nil {
		t.Fatalf("unsupported outcome %s did not return the required dependency-mode error: %v", operationID, err)
	}
	return unsupported
}

// RecordOutcome 记录 approval、interrupt、force-complete 或 toolbridge 的 typed outcome 证据。
func (e *CaseEvidence) RecordOutcome(t *testing.T, key EvidenceKey, outcome OutcomeEvidence) {
	t.Helper()
	if err := validateOutcomeEvidenceKey(key); err != nil {
		t.Fatal(err)
	}
	if err := validateOutcomeEvidence(key, &outcome); err != nil {
		e.invalid = append(e.invalid, err.Error())
		return
	}
	e.assertions[key] = fmt.Sprintf(
		"%s/%s/%s/%s/%s/%t",
		outcome.ObservedActionID,
		outcome.StateBefore,
		outcome.StateAfter,
		outcome.DependencyName,
		outcome.Profile,
		outcome.Unsupported != nil,
	)
}

func validateOutcomeEvidenceKey(key EvidenceKey) error {
	switch key {
	case EvidenceApprovalOutcome, EvidenceInterruptOutcome, EvidenceForceCompleteOutcome, EvidenceToolbridgeDependency:
		return nil
	default:
		return fmt.Errorf("unsupported outcome evidence key %s", key)
	}
}

// validateOutcomeEvidence 校验 outcome 必须来自真实动作或 typed unsupported 结果。
func validateOutcomeEvidence(key EvidenceKey, outcome *OutcomeEvidence) error {
	if strings.TrimSpace(outcome.StateAfter) == "" {
		return fmt.Errorf("%s outcome evidence must include the observed final state", key)
	}
	if strings.TrimSpace(outcome.ObservedActionID) == "" {
		if err := validateUnsupportedOutcome(outcome.Unsupported); err != nil {
			return fmt.Errorf("%s outcome evidence must include an observed action id or typed unsupported result: %w", key, err)
		}
	}
	applyUnsupportedDependency(outcome)
	if key == EvidenceToolbridgeDependency && (strings.TrimSpace(outcome.DependencyName) == "" || outcome.Profile == "") {
		return errors.New("toolbridge dependency evidence must include dependency name and profile")
	}
	return validateExpectedUnsupportedDependency(key, outcome)
}

func applyUnsupportedDependency(outcome *OutcomeEvidence) {
	if outcome.Unsupported == nil {
		return
	}
	outcome.DependencyName = outcome.Unsupported.dependencyName
	outcome.Profile = outcome.Unsupported.profile
}

func validateExpectedUnsupportedDependency(key EvidenceKey, outcome *OutcomeEvidence) error {
	if outcome.Unsupported == nil {
		return nil
	}
	if strings.TrimSpace(outcome.ExpectedDependencyName) == "" {
		return fmt.Errorf("%s unsupported outcome must declare the expected dependency name", key)
	}
	if outcome.ExpectedDependencyName != outcome.Unsupported.dependencyName {
		return fmt.Errorf("%s unsupported dependency = %s, want %s", key, outcome.Unsupported.dependencyName, outcome.ExpectedDependencyName)
	}
	return nil
}

// validateUnsupportedOutcome 校验 unsupported 证据必须来自捕获到的 dependency-mode 错误。
func validateUnsupportedOutcome(unsupported *UnsupportedOutcomeEvidence) error {
	switch {
	case unsupported == nil:
		return errors.New("typed unsupported evidence is required")
	case unsupported.booleanOnly:
		return errors.New("boolean unsupported marker is not typed dependency-mode error evidence")
	case strings.TrimSpace(unsupported.operationID) == "":
		return errors.New("observed provider operation id is required")
	case !unsupported.captured:
		return errors.New("unsupported outcome must come from an observed provider operation")
	case strings.TrimSpace(unsupported.dependencyName) == "" || unsupported.profile == "":
		return errors.New("dependency name and profile are required")
	}
	return validateDependencyModeError(unsupported)
}

// validateDependencyModeError 校验 dependency-mode 错误的依赖名、profile 和哨兵错误。
func validateDependencyModeError(unsupported *UnsupportedOutcomeEvidence) error {
	modeErr, ok := concreteDependencyModeError(unsupported.err)
	if !ok {
		return fmt.Errorf("unsupported outcome error = %v, want concrete dependency mode error in unwrap chain", unsupported.err)
	}
	if modeErr.Name != unsupported.dependencyName || modeErr.Profile != unsupported.profile {
		return fmt.Errorf(
			"unsupported outcome dependency = %s/%s, want %s/%s",
			modeErr.Name,
			modeErr.Profile,
			unsupported.dependencyName,
			unsupported.profile,
		)
	}
	if !errors.Is(modeErr.Err, contract.ErrUnsupportedDependencyMode) && !errors.Is(modeErr.Err, contract.ErrDependencyDeferred) {
		return fmt.Errorf("unsupported outcome error = %v, want dependency mode error", unsupported.err)
	}
	return nil
}

// concreteDependencyModeError 沿标准 Unwrap 链查找真实 DependencyModeError。
func concreteDependencyModeError(err error) (contract.DependencyModeError, bool) {
	for err != nil {
		switch typed := err.(type) {
		case contract.DependencyModeError:
			return typed, true
		case *contract.DependencyModeError:
			if typed != nil {
				return *typed, true
			}
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return contract.DependencyModeError{}, false
		}
		err = unwrapped.Unwrap()
	}
	return contract.DependencyModeError{}, false
}
