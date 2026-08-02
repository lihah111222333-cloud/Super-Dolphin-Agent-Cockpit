package remoteci

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteGoTestBootstrapEstimateIncludesProcessOverhead(t *testing.T) {
	parent, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./internal/example", 90_000)
	if err != nil {
		t.Fatal(err)
	}
	input := RunInput{
		Platform: "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain",
		LedgerSnapshot: gate.DurationLedgerSnapshot{Ledger: gate.DurationLedger{Version: 1, Samples: []gate.DurationSample{{
			Bucket: gate.DurationBucket{
				WorkloadID:    gate.GoTestDurationWorkloadID(parent.ID, "TestFast"),
				CommandDigest: gate.GoTestDurationCommandDigest(parent.CommandDigest, "TestFast"),
				Platform:      "linux/amd64", Runner: "runner", Toolchain: "toolchain",
			},
			Succeeded: true, DurationMS: 50,
			TargetKind: "go_test", ParentWorkloadID: parent.ID,
			ParentCommandDigest: parent.CommandDigest, TargetName: "TestFast", TargetStatus: "pass",
		}}}},
	}
	index := testRemoteDurationIndex(t, input)
	if got := remoteGoTestBootstrapEstimateMS(parent, "TestFast", 30, 90_000, index); got != remoteGoTestInvocationFloorMS {
		t.Fatalf("fast test estimate = %d, want process floor %d", got, remoteGoTestInvocationFloorMS)
	}
	input.LedgerSnapshot.Ledger.Samples[0].DurationMS = 20_000
	index = testRemoteDurationIndex(t, input)
	if got := remoteGoTestBootstrapEstimateMS(parent, "TestFast", 30, 90_000, index); got != 23_000 {
		t.Fatalf("slow test estimate = %d, want body plus process overhead", got)
	}
	if got := remoteGoTestBootstrapEstimateMS(parent, "TestUnknown", 30, 90_000, index); got != remoteGoTestInvocationFloorMS {
		t.Fatalf("unknown test estimate = %d, want process floor %d", got, remoteGoTestInvocationFloorMS)
	}
}

func TestRemoteRequiredGoTestCacheEntriesSkipChildrenOfCachedPackage(t *testing.T) {
	parent, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/example",
		1_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	child, err := gate.NewGoTestWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/example",
		"TestValue",
		1_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	entries := []remoteWorkloadCacheEntry{{workloadID: child.ID}}
	misses, err := remoteRequiredGoTestCacheEntries(
		entries,
		map[string]gate.PlanGateExecution{
			parent.ID: {
				GateID: gate.GateID(parent.ID),
				Status: gate.ResultStatusPassed,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(misses) != 0 {
		t.Fatalf("cached package left %d compatible child migrations", len(misses))
	}
	misses, err = remoteRequiredGoTestCacheEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(misses) != 1 || misses[0].workloadID != child.ID {
		t.Fatalf("uncached package migration entries = %#v", misses)
	}
}

func TestRemoteGoTestHasOverTargetFailureRequiresComparableBucket(t *testing.T) {
	workload, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	input := RunInput{
		Platform: "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain",
		LedgerSnapshot: gate.DurationLedgerSnapshot{Ledger: gate.DurationLedger{Version: 1, Samples: []gate.DurationSample{{
			Bucket: gate.DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: "linux/amd64", Runner: "runner", Toolchain: "toolchain",
			},
			Succeeded: false, DurationMS: gate.FullCITargetDurationMS + 1,
		}}}},
	}
	if !remoteGoTestHasOverTargetFailure(workload, testRemoteDurationIndex(t, input)) {
		t.Fatal("matching over-target failure did not force test-level splitting")
	}
	input.LedgerSnapshot.Ledger.Samples[0].Bucket.Runner = "other-runner"
	if remoteGoTestHasOverTargetFailure(workload, testRemoteDurationIndex(t, input)) {
		t.Fatal("incomparable runner failure forced test-level splitting")
	}
}

func TestCalibrationNeverInvalidatesVerifiedPassWithoutDurationSample(t *testing.T) {
	parent, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestGuardWithRace, "./internal/archtest", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	testWorkload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestGuardWithRace, "./internal/archtest", "TestBoundary", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	cached := map[string]gate.PlanGateExecution{
		parent.ID:       {GateID: gate.GateID(parent.ID), Status: gate.ResultStatusPassed},
		testWorkload.ID: {GateID: gate.GateID(testWorkload.ID), Status: gate.ResultStatusPassed},
	}
	resume := remoteGoTestResumeSet{
		workloadsByParent:    map[string][]gate.Workload{parent.ID: {testWorkload}},
		forcedSplitParents:   map[string]struct{}{parent.ID: {}},
		wholePackageRequired: make(map[string]struct{}),
	}
	input := RunInput{Calibration: true}
	if err := enforceRemoteCalibrationEvidence(
		[]gate.Workload{parent},
		cached,
		&resume,
		input,
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := cached[parent.ID]; !ok {
		t.Fatal("calibration discarded a verified parent PASS")
	}
	if _, ok := cached[testWorkload.ID]; !ok {
		t.Fatal("calibration discarded a verified child PASS")
	}
	if len(resume.wholePackageRequired) != 0 {
		t.Fatal("missing duration sample forced a passed package back into execution")
	}
}

func TestAppendProjectedRemoteGoTestWorkloadDropsUnavailablePackage(t *testing.T) {
	workload, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./ignored", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	effective := gate.WorkloadCatalog{}
	appendProjectedRemoteGoTestWorkload(
		&effective,
		map[string]gate.PlanGateExecution{},
		map[string]string{},
		workload,
		map[string]gate.PlanGateExecution{},
		map[string]gate.PlanGateExecution{},
		remoteGoTestResumeSet{excludedParents: map[string]struct{}{workload.ID: {}}},
		map[string]string{},
	)
	if len(effective.Workloads) != 0 {
		t.Fatalf("unavailable package remained in catalog: %+v", effective.Workloads)
	}
}

func TestAppendProjectedRemoteGoTestWorkloadProjectsAllIndependentTestHits(t *testing.T) {
	parent, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./internal/example", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	testWorkload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/example", "TestOnly", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	effective := gate.WorkloadCatalog{}
	resume := remoteGoTestResumeSet{
		workloadsByParent:    map[string][]gate.Workload{parent.ID: {testWorkload}},
		wholePackageRequired: make(map[string]struct{}),
	}
	appendProjectedRemoteGoTestWorkload(
		&effective,
		map[string]gate.PlanGateExecution{},
		map[string]string{},
		parent,
		map[string]gate.PlanGateExecution{},
		map[string]gate.PlanGateExecution{testWorkload.ID: {GateID: gate.GateID(testWorkload.ID), Status: gate.ResultStatusPassed}},
		resume,
		map[string]string{parent.ID: "parent", testWorkload.ID: "test"},
	)
	if len(effective.Workloads) != 1 || effective.Workloads[0].ID != testWorkload.ID {
		t.Fatalf("independent child PASS was not projected as the effective workload: %+v", effective.Workloads)
	}
	if _, ok := resume.wholePackageRequired[parent.ID]; ok {
		t.Fatal("independently verified child PASS unexpectedly required package execution")
	}
}

func TestAppendRemoteGoTestResumeParentRequiresIndependentDigest(t *testing.T) {
	parent, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./internal/example", 4_000)
	if err != nil {
		t.Fatal(err)
	}
	input := RunInput{Platform: "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain"}
	set := remoteGoTestResumeSet{
		workloadsByParent:         make(map[string][]gate.Workload),
		estimatedDurationByParent: make(map[string]int64),
		inputDigests:              make(map[string]string),
		durationIndex:             testRemoteDurationIndex(t, input),
	}
	if err := appendRemoteGoTestResumeParent(
		&set,
		parent,
		map[string][]string{parent.ID: {"TestBoundary"}},
		map[string]string{parent.ID: "parent-digest"},
	); err == nil {
		t.Fatal("missing independent Go test digest fell back to parent package digest")
	}
}

func testRemoteDurationIndex(t *testing.T, input RunInput) gate.DurationSampleIndex {
	t.Helper()
	index, err := gate.BuildDurationSampleIndex(
		input.LedgerSnapshot.Ledger,
		remotePlanningContext(input),
	)
	if err != nil {
		t.Fatal(err)
	}
	return index
}
