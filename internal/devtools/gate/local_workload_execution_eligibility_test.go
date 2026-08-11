package gate

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestEvaluateLocalWorkloadExecutionEligibilityFrontendCommandIsExplicitlyIneligible(t *testing.T) {
	result, err := EvaluateLocalWorkloadExecutionEligibility(GateIDFrontendLint)
	if err != nil {
		t.Fatalf("EvaluateLocalWorkloadExecutionEligibility() error = %v", err)
	}
	if result.Eligible || result.WorkloadID != GateIDFrontendLint || result.CanonicalID != GateIDFrontendLint {
		t.Fatalf("frontend command eligibility = %#v, want ineligible canonical command", result)
	}
	if result.Strategy != ExecutorStrategyCommands || !strings.Contains(result.Reason, "explicitly ineligible") {
		t.Fatalf("frontend command eligibility metadata = %#v, want commands and explicit reason", result)
	}
}

func TestEvaluateLocalWorkloadExecutionEligibilityExpandedCommand(t *testing.T) {
	id, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoPackage, "./internal/devtools/gate")
	if err != nil {
		t.Fatalf("targetWorkloadID() error = %v", err)
	}
	result, err := EvaluateLocalWorkloadExecutionEligibility(GateID(id))
	if err != nil {
		t.Fatalf("EvaluateLocalWorkloadExecutionEligibility() error = %v", err)
	}
	if !result.Eligible || result.WorkloadID != GateID(id) || result.CanonicalID != GateIDBackendTestWithGuard {
		t.Fatalf("expanded command eligibility = %#v, want eligible canonical owner %q", result, GateIDBackendTestWithGuard)
	}
	if result.Strategy != ExecutorStrategyCommands {
		t.Fatalf("expanded command strategy = %q, want %q", result.Strategy, ExecutorStrategyCommands)
	}
}

func TestEvaluateLocalWorkloadExecutionEligibilityReleaseAttestationIsIneligible(t *testing.T) {
	result, err := EvaluateLocalWorkloadExecutionEligibility(GateIDReleaseLayeredCheck)
	if err != nil {
		t.Fatalf("EvaluateLocalWorkloadExecutionEligibility() error = %v", err)
	}
	if result.Eligible {
		t.Fatalf("release attestation eligibility = %#v, want ineligible", result)
	}
	if result.Strategy != ExecutorStrategyReleaseAttestation || !strings.Contains(result.Reason, "ineligible") {
		t.Fatalf("release attestation eligibility = %#v, want explicit ineligible reason", result)
	}
}

func TestEvaluateLocalWorkloadExecutionEligibilityKnownCanonicalWithoutMappingIsIneligible(t *testing.T) {
	for _, id := range []GateID{GateIDLSPChangedDiagnostics, GateIDBackendNilness} {
		t.Run(string(id), func(t *testing.T) {
			result, err := EvaluateLocalWorkloadExecutionEligibility(id)
			if err != nil {
				t.Fatalf("EvaluateLocalWorkloadExecutionEligibility() error = %v", err)
			}
			if result.Eligible || result.CanonicalID != id || !strings.Contains(result.Reason, "no local executor mapping") {
				t.Fatalf("known unmapped eligibility = %#v, want explicit local ineligible result", result)
			}
		})
	}
}

func TestEvaluateLocalWorkloadExecutionEligibilityBuiltInsAreIneligible(t *testing.T) {
	for _, id := range []GateID{
		GateIDSQLCVerify,
		GateIDWhitespaceCheck,
	} {
		t.Run(string(id), func(t *testing.T) {
			result, err := EvaluateLocalWorkloadExecutionEligibility(id)
			if err != nil {
				t.Fatalf("EvaluateLocalWorkloadExecutionEligibility() error = %v", err)
			}
			if result.Eligible || !strings.Contains(result.Reason, "ineligible") {
				t.Fatalf("built-in eligibility = %#v, want explicit ineligible result", result)
			}
		})
	}
}

func TestEvaluateLocalWorkloadExecutionEligibilityUnknownFailsFast(t *testing.T) {
	result, err := EvaluateLocalWorkloadExecutionEligibility(GateID("unknown:workload"))
	if err == nil || !strings.Contains(err.Error(), "unknown workload") {
		t.Fatalf("unknown workload result = %#v, error = %v, want fail-fast unknown error", result, err)
	}
}

func TestEvaluateLocalWorkloadExecutionEligibilityIsDeterministic(t *testing.T) {
	id, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoPackage, "./internal/devtools/gate")
	if err != nil {
		t.Fatalf("targetWorkloadID() error = %v", err)
	}
	first, firstErr := EvaluateLocalWorkloadExecutionEligibility(GateID(id))
	second, secondErr := EvaluateLocalWorkloadExecutionEligibility(GateID(id))
	if !reflect.DeepEqual(first, second) || (firstErr == nil) != (secondErr == nil) || (firstErr != nil && firstErr.Error() != secondErr.Error()) {
		t.Fatalf("eligibility results are not deterministic: first=%#v err=%v second=%#v err=%v", first, firstErr, second, secondErr)
	}
}

func TestLocalExecutorReceiptBoundWorkloadIDsIncludesEveryMappedWorkload(t *testing.T) {
	selected, err := LocalExecutorReceiptBoundWorkloadIDs([]GateID{
		GateIDCodemapCheck,
		GateIDFrontendLint,
		GateIDReleaseLayeredCheck,
		GateIDLSPChangedDiagnostics,
	})
	if err != nil {
		t.Fatalf("LocalExecutorReceiptBoundWorkloadIDs() error = %v", err)
	}
	if !slices.Equal(selected, []GateID{GateIDCodemapCheck, GateIDFrontendLint, GateIDReleaseLayeredCheck}) {
		t.Fatalf("receipt workload IDs = %#v, want every canonical executor-mapped ID", selected)
	}
}

func TestLocalExecutorExecutionWorkloadIDsOnlySelectsEligibleWorkloads(t *testing.T) {
	selected, err := LocalExecutorExecutionWorkloadIDs([]GateID{
		GateIDCodemapCheck,
		GateIDFrontendLint,
		GateIDReleaseLayeredCheck,
		GateIDLSPChangedDiagnostics,
	})
	if err != nil {
		t.Fatalf("LocalExecutorExecutionWorkloadIDs() error = %v", err)
	}
	if !slices.Equal(selected, []GateID{GateIDCodemapCheck}) {
		t.Fatalf("execution workload IDs = %#v, want only local-executable IDs", selected)
	}
}
