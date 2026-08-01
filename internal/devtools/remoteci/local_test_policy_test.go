package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestDecideLocalLightTestRequiresComparableFastCloudEvidence(t *testing.T) {
	workload, input := localLightPolicyFixture(t, 320, 640)
	decision, err := DecideLocalLightTest(workload, input)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Eligible || decision.ObservedDurationMS != 640 {
		t.Fatalf("local light decision = %+v", decision)
	}

	input.LedgerSnapshot.Ledger.Samples[1].DurationMS = LocalLightTestMaxDurationMS + 1
	decision, err = DecideLocalLightTest(workload, input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Eligible || !strings.Contains(decision.Reason, "exceeds") {
		t.Fatalf("slow local light decision = %+v", decision)
	}
}

func TestDecideLocalLightTestRejectsUnknownPackageRaceBenchmarkAndForce(t *testing.T) {
	workload, input := localLightPolicyFixture(t, 200, 400)
	input.LedgerSnapshot.Ledger.Samples = nil
	assertLocalTestRejected(t, workload, input, "no comparable cloud PASS timing")

	packageWorkload, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalTestRejected(t, packageWorkload, input, "only one exact Go test")

	raceWorkload, err := gate.NewGoTestWorkload(
		gate.GateIDBackendTestGuardWithRace,
		"./internal/module/turn",
		"TestRedact",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalTestRejected(t, raceWorkload, input, "race")

	benchmark, err := gate.NewGoBenchmarkWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		"BenchmarkRedact_NoMatch",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalTestRejected(t, benchmark, input, "only one exact Go test")

	input.ForceRerun = true
	assertLocalTestRejected(t, workload, input, "refresh remote proof")
}

func localLightPolicyFixture(
	t *testing.T,
	targetDuration int64,
	totalDuration int64,
) (gate.Workload, RunInput) {
	t.Helper()
	workload, err := gate.NewGoTestWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		"TestRedact",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	packageWorkload, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := RunInput{
		Platform:             "linux/amd64",
		RunnerIdentityDigest: "runner",
		ToolchainDigest:      "toolchain",
	}
	input.LedgerSnapshot.Ledger.Samples = []gate.DurationSample{
		{
			Bucket: gate.DurationBucket{
				WorkloadID:    gate.GoTestDurationWorkloadID(packageWorkload.ID, "TestRedact"),
				CommandDigest: gate.GoTestDurationCommandDigest(packageWorkload.CommandDigest, "TestRedact"),
				Platform:      input.Platform,
				Runner:        input.RunnerIdentityDigest,
				Toolchain:     input.ToolchainDigest,
			},
			Succeeded:           true,
			DurationMS:          targetDuration,
			TargetKind:          gate.WorkloadKindGoTest,
			ParentWorkloadID:    packageWorkload.ID,
			ParentCommandDigest: packageWorkload.CommandDigest,
			TargetName:          "TestRedact",
			TargetStatus:        gate.GoTestStatusPass,
		},
		{
			Bucket: gate.DurationBucket{
				WorkloadID:    packageWorkload.ID,
				CommandDigest: packageWorkload.CommandDigest,
				Platform:      input.Platform,
				Runner:        input.RunnerIdentityDigest,
				Toolchain:     input.ToolchainDigest,
			},
			Succeeded:  true,
			DurationMS: totalDuration,
		},
	}
	return workload, input
}

func assertLocalTestRejected(
	t *testing.T,
	workload gate.Workload,
	input RunInput,
	wantReason string,
) {
	t.Helper()
	decision, err := DecideLocalLightTest(workload, input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Eligible || !strings.Contains(decision.Reason, wantReason) {
		t.Fatalf("local light decision = %+v, want reason containing %q", decision, wantReason)
	}
}
