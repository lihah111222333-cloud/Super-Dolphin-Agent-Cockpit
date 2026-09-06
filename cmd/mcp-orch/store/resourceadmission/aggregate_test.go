package resourceadmission

import "testing"

// TestResourceAdmissionStoreContract 聚合RESOURCE reserved-only store与字段/架构合同。
func TestResourceAdmissionStoreContract(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"policy_closed_vector", TestPublishEffectivePolicyClosedVectorAndDerivedDigests},
		{"policy_zero_dimensions", TestPublishEffectivePolicyRejectsEveryZeroDimension},
		{"policy_replay", TestPublishEffectivePolicyReplayConflictAndCanonicalCandidates},
		{"policy_revision_fence", TestPublishEffectivePolicyRevisionOwnerAndTimeFence},
		{"missing_policy", TestReserveMissingPolicyLeavesNoResourceRows},
		{"reservation_overlimit", TestReserveRejectsEveryOverLimitDimension},
		{"reservation_replay", TestReserveExactReplayReturnsCanonicalCandidateIDs},
		{"reservation_relations", TestReserveRevalidatesProfileBothBindingsAndContent},
		{"reservation_reopen", TestReserveCloseReopenPreservesSnapshot},
		{"resolve_drift", TestResolveReservedSnapshotFailsClosedOnAuthorityDrift},
		{"usage_disabled", TestResourceUsageHasNoWriterAndTriggerRejectsDirectInsert},
		{"field_architecture", TestResourceAdmissionFieldAndArchitectureGuards},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
