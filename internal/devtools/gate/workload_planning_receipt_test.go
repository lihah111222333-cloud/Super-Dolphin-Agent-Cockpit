package gate

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestCanonicalWorkloadPlanningReceiptRoundTrip(t *testing.T) {
	planningPolicyDigest, err := cicontract.WorkloadPlanningPolicyDigest()
	if err != nil {
		t.Fatalf("planning policy digest: %v", err)
	}
	receipt := WorkloadPlanningReceipt{
		Status: cicontract.WorkloadPlanningReceiptStatusPlanned,
		Planned: &WorkloadPlanningReceiptPlanned{
			ReceiptSchemaVersion:    cicontract.WorkloadPlanningReceiptSchemaVersion,
			PlanSchemaVersion:       cicontract.WorkloadExecutionPlanSchemaVersion,
			AlgorithmID:             cicontract.WorkloadPlanningAlgorithmID,
			ObjectiveDigest:         cicontract.WorkloadPlanningObjectiveDigest(),
			PlanningPolicyDigest:    planningPolicyDigest,
			EstimationPolicyDigest:  "sha256:" + strings.Repeat("1", 64),
			GatePlanDigest:          "sha256:" + strings.Repeat("2", 64),
			CatalogDigest:           "sha256:" + strings.Repeat("3", 64),
			LedgerGeneration:        1,
			ExecutionWorkloadDigest: "sha256:" + strings.Repeat("4", 64),
			PlanDigest:              "sha256:" + strings.Repeat("5", 64),
			TierResolutions:         canonicalReceiptTierResolutionsForTest(),
			PackingEvidence:         canonicalReceiptPackingEvidenceForTest(),
			MappingRoot:             "sha256:" + strings.Repeat("6", 64),
			PlannedWorkloadCount:    1,
			PlannedShardCount:       1,
		},
	}
	encoded, err := EncodeCanonicalWorkloadPlanningReceipt(receipt)
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	decoded, err := DecodeCanonicalWorkloadPlanningReceipt(encoded)
	if err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if _, err := WorkloadPlanningReceiptDigest(decoded); err != nil {
		t.Fatalf("digest receipt: %v", err)
	}
}

func TestDecodeCanonicalWorkloadPlanningReceiptRejectsNonCanonicalInputs(t *testing.T) {
	receipt := canonicalReceiptForTest(t)
	encoded, err := EncodeCanonicalWorkloadPlanningReceipt(receipt)
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	unknown := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"unknown":true}`)...)
	doublePayload := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"not_applicable":{}}`)...)
	duplicateStatus := bytesReplace(encoded, `{"status":"planned",`, `{"status":"planned","status":"planned",`)
	duplicateTier := bytesReplace(encoded, `"resource_tier":2`, `"resource_tier":1`)
	for name, payload := range map[string][]byte{
		"empty":            nil,
		"null":             []byte("null"),
		"trailing":         append(append([]byte(nil), encoded...), []byte("{}")...),
		"leading_space":    append([]byte(" "), encoded...),
		"unknown":          unknown,
		"double_payload":   doublePayload,
		"duplicate_status": duplicateStatus,
		"duplicate_tier":   duplicateTier,
		"malformed_digest": bytesReplace(encoded, `sha256:`, `SHA256:`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCanonicalWorkloadPlanningReceipt(payload); err == nil {
				t.Fatalf("decoder accepted %s input", name)
			}
		})
	}
}

func TestWorkloadPlanningMappingRootIsCanonicalAndBounded(t *testing.T) {
	entries := []WorkloadPlanningMappingEntry{
		{WorkloadID: string(GateIDFrontendTest), ShardIdentity: "sha256:" + strings.Repeat("a", 64)},
		{WorkloadID: string(GateIDFrontendLint), ShardIdentity: "sha256:" + strings.Repeat("b", 64)},
		{WorkloadID: string(GateIDFrontendBuild), ShardIdentity: "sha256:" + strings.Repeat("c", 64)},
	}
	first, err := ComputeWorkloadPlanningMappingRoot(entries)
	if err != nil {
		t.Fatalf("compute mapping root: %v", err)
	}
	reordered, err := ComputeWorkloadPlanningMappingRoot([]WorkloadPlanningMappingEntry{entries[2], entries[0], entries[1]})
	if err != nil || reordered != first {
		t.Fatalf("mapping root is not order-independent: first=%q reordered=%q err=%v", first, reordered, err)
	}
	for name, invalid := range [][]WorkloadPlanningMappingEntry{
		{{WorkloadID: "", ShardIdentity: "sha256:" + strings.Repeat("a", 64)}},
		{{WorkloadID: string(GateIDFrontendLint), ShardIdentity: "sha256:" + strings.Repeat("a", 64)}, {WorkloadID: string(GateIDFrontendLint), ShardIdentity: "sha256:" + strings.Repeat("b", 64)}},
		{{WorkloadID: "bad\xff", ShardIdentity: "sha256:" + strings.Repeat("a", 64)}},
		{{WorkloadID: string(GateIDFrontendTest), ShardIdentity: "SHA256:" + strings.Repeat("a", 64)}},
	} {
		if _, err := ComputeWorkloadPlanningMappingRoot(invalid); err == nil {
			t.Fatalf("mapping input %d unexpectedly accepted", name)
		}
	}
	if _, err := ComputeWorkloadPlanningMappingRoot(nil); err == nil {
		t.Fatal("empty mapping unexpectedly accepted")
	}
	changed, err := ComputeWorkloadPlanningMappingRoot(append(entries, WorkloadPlanningMappingEntry{WorkloadID: string(GateIDFrontendE2E), ShardIdentity: "sha256:" + strings.Repeat("d", 64)}))
	if err != nil || changed == first {
		t.Fatalf("mapping root did not bind leaf count: changed=%q first=%q err=%v", changed, first, err)
	}
}

func TestWorkloadPlanningReceiptTierResolutionBoundaries(t *testing.T) {
	receipt := canonicalReceiptForTest(t)
	receipt.Planned.TierResolutions[0].Resolution = cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable
	if _, err := EncodeCanonicalWorkloadPlanningReceipt(receipt); err == nil {
		t.Fatal("normal receipt accepted mixed not_applicable tier resolution")
	}
	for index := range receipt.Planned.TierResolutions {
		tier := cicontract.WorkloadResourceTier(index + 1)
		receipt.Planned.TierResolutions[index] = WorkloadPlanningTierResolution{ResourceTier: tier, Resolution: cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable}
		receipt.Planned.PackingEvidence[index] = WorkloadPackingEvidence{ResourceTier: tier, Resolution: cicontract.WorkloadResourceTierReserveResolution(cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable), SolverMode: cicontract.WorkloadPlanningReceiptNotApplicableEvidence}
	}
	receipt.Planned.PlannedWorkloadCount, receipt.Planned.PlannedShardCount = 0, 0
	if _, err := EncodeCanonicalWorkloadPlanningReceipt(receipt); err != nil {
		t.Fatalf("canonical calibration receipt rejected: %v", err)
	}
}

func canonicalReceiptForTest(t *testing.T) WorkloadPlanningReceipt {
	t.Helper()
	planningPolicyDigest, err := cicontract.WorkloadPlanningPolicyDigest()
	if err != nil {
		t.Fatalf("planning policy digest: %v", err)
	}
	return WorkloadPlanningReceipt{Status: cicontract.WorkloadPlanningReceiptStatusPlanned, Planned: &WorkloadPlanningReceiptPlanned{
		ReceiptSchemaVersion: cicontract.WorkloadPlanningReceiptSchemaVersion, PlanSchemaVersion: cicontract.WorkloadExecutionPlanSchemaVersion,
		AlgorithmID: cicontract.WorkloadPlanningAlgorithmID, ObjectiveDigest: cicontract.WorkloadPlanningObjectiveDigest(), PlanningPolicyDigest: planningPolicyDigest,
		EstimationPolicyDigest: "sha256:" + strings.Repeat("1", 64), GatePlanDigest: "sha256:" + strings.Repeat("2", 64), CatalogDigest: "sha256:" + strings.Repeat("3", 64),
		LedgerGeneration: 1, ExecutionWorkloadDigest: "sha256:" + strings.Repeat("4", 64), PlanDigest: "sha256:" + strings.Repeat("5", 64),
		TierResolutions: canonicalReceiptTierResolutionsForTest(), PackingEvidence: canonicalReceiptPackingEvidenceForTest(), MappingRoot: "sha256:" + strings.Repeat("6", 64),
		PlannedWorkloadCount: 1, PlannedShardCount: 1,
	}}
}

func bytesReplace(value []byte, old, replacement string) []byte {
	return []byte(strings.Replace(string(value), old, replacement, 1))
}

func canonicalReceiptTierResolutionsForTest() []WorkloadPlanningTierResolution {
	return []WorkloadPlanningTierResolution{
		{ResourceTier: cicontract.WorkloadResourceTierFast, Resolution: cicontract.WorkloadPlanningReceiptTierResolutionBootstrap},
		{ResourceTier: cicontract.WorkloadResourceTierMedium, Resolution: cicontract.WorkloadPlanningReceiptTierResolutionBootstrap},
		{ResourceTier: cicontract.WorkloadResourceTierSlow, Resolution: cicontract.WorkloadPlanningReceiptTierResolutionBootstrap},
	}
}

func canonicalReceiptPackingEvidenceForTest() []WorkloadPackingEvidence {
	return []WorkloadPackingEvidence{
		{ResourceTier: cicontract.WorkloadResourceTierFast, Resolution: cicontract.WorkloadResourceTierReserveResolutionBootstrap, SolverMode: cicontract.WorkloadPlanningReserveBootstrapSolverModeID, PlannedShards: 1, LowerBoundShards: 1, PackableUnitCount: 1},
		{ResourceTier: cicontract.WorkloadResourceTierMedium, Resolution: cicontract.WorkloadResourceTierReserveResolutionBootstrap, SolverMode: cicontract.WorkloadPlanningReserveBootstrapSolverModeID},
		{ResourceTier: cicontract.WorkloadResourceTierSlow, Resolution: cicontract.WorkloadResourceTierReserveResolutionBootstrap, SolverMode: cicontract.WorkloadPlanningReserveBootstrapSolverModeID},
	}
}
