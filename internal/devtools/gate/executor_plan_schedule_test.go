package gate

import (
	"slices"
	"testing"
)

func localFastExecutorPlanLanes() [][]GateID {
	return [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendTest, GateIDBackendTestWithGuard},
		{GateIDFrontendLint, GateIDFrontendBuild, GateIDFrontendEmbedVerify, GateIDSQLCVerify, GateIDCodemapCheck,
			GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
}

func releaseExecutorPlanLanes() [][]GateID {
	return [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendPreflight, GateIDFrontendE2E, GateIDFrontendFullTest, GateIDBackendTestWithGuard,
			GateIDBackendTestGuardWithRace, GateIDBackendNilness},
		{GateIDFrontendLint, GateIDFrontendBuild, GateIDFrontendEmbedVerify, GateIDSQLCVerify, GateIDCodemapCheck,
			GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
}

func assertExecutorPlanSchedule(t *testing.T, profile Profile, wantLanes [][]GateID) {
	t.Helper()
	request := testExecutorPlanRequestForProfile(t, profile)
	prerequisites, requiresAttestation, err := planExecutionPrerequisites(request)
	if err != nil {
		t.Fatal(err)
	}
	if requiresAttestation != (profile == ProfileRelease) {
		t.Fatalf("profile %q release attestation requirement = %t", profile, requiresAttestation)
	}
	lanes, err := executorPlanLanes(prerequisites)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutorPlanLaneExactSet(t, prerequisites, lanes)
	if len(lanes) != len(wantLanes) {
		t.Fatalf("profile %q lane count = %d, want %d", profile, len(lanes), len(wantLanes))
	}
	for index, want := range wantLanes {
		if !slices.Equal(lanes[index], want) {
			t.Fatalf("profile %q lane %d = %v, want %v", profile, index, lanes[index], want)
		}
	}
}
