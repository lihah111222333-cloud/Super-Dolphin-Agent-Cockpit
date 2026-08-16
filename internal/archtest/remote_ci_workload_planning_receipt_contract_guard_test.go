package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIWorkloadPlanningReceiptContractGuard freezes the Slice A1 owner,
// accepted contract anchors, and the absence of persistence/orchestration scope.
func TestRemoteCIWorkloadPlanningReceiptContractGuard(t *testing.T) {
	root := findRepoRoot(t)
	receipt := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_planning_receipt.go"))
	packingEvidence := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_packing_evidence_validation.go"))
	codeContract := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/cicontract/workload_planning_receipt.go"))
	accepted := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs/契约/remote-ci-eci-imagecache-contract.md"))
	matrix := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs/guards/remote-ci-plan-contract-field-matrix.md"))
	for _, marker := range []string{
		"type WorkloadPlanningReceipt struct", "type WorkloadPlanningTierResolution struct",
		"EncodeCanonicalWorkloadPlanningReceipt", "DecodeCanonicalWorkloadPlanningReceipt",
		"WorkloadPlanningReceiptDigest", "ComputeWorkloadPlanningMappingRoot",
		"tier_resolutions", "packing_evidence",
	} {
		if !strings.Contains(receipt, marker) {
			t.Errorf("receipt owner is missing marker %q", marker)
		}
	}
	if !strings.Contains(packingEvidence, "WorkloadPlanningReserveBootstrapSolverModeID") {
		t.Error("packing evidence owner must enforce the canonical bootstrap solver")
	}
	for _, forbidden := range []string{"sqlite", "coordinator", "finalizer", "JobID"} {
		if strings.Contains(receipt, forbidden) {
			t.Errorf("receipt owner must not claim forbidden scope %q", forbidden)
		}
	}
	for _, marker := range []string{
		"WorkloadPlanningReceiptStatusPlanned", "WorkloadPlanningReceiptReasonPrePlanningFailure",
		"WorkloadPlanningReceiptTierResolutionMeasured", "WorkloadPlanningMappingDomain",
		"ValidateWorkloadPlanningReceiptDigest", "remote-ci-workload-planning-receipt/v1",
		"remote-ci-workload-planning-map/v1",
	} {
		if !strings.Contains(codeContract, marker) {
			t.Errorf("receipt cicontract is missing marker %q", marker)
		}
	}
	if !strings.Contains(accepted, "workload-planning-receipt/v1") {
		t.Error("accepted contract must anchor workload planning receipt schema")
	}
	if !strings.Contains(matrix, "mapping root") {
		t.Error("field guard must anchor planning mapping root")
	}
}
