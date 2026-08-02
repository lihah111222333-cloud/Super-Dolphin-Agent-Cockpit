package main

import (
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestSelectAutoTestBackendUsesCacheBeforeLocalOrECI(t *testing.T) {
	backend, decisions, err := selectAutoTestBackend(
		remoteci.WorkloadCacheProbeResult{ReusedWorkloads: []gatecontract.GateID{"cached"}},
		remoteci.RunInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if backend != autoTestBackendRemoteCache || decisions != nil {
		t.Fatalf("all-hit backend=%q decisions=%v", backend, decisions)
	}
}

func TestSelectAutoTestBackendAllowsOnlyCloudTimedExactGoTests(t *testing.T) {
	workload, input := autoTestLocalPolicyFixture(t)
	selection := remoteci.WorkloadCacheProbeResult{CacheMissWorkloads: []gatecontract.Workload{workload}}
	backend, decisions, err := selectAutoTestBackend(selection, input)
	if err != nil {
		t.Fatal(err)
	}
	if backend != autoTestBackendLocalLight || !decisions[workload.ID].Eligible {
		t.Fatalf("exact test backend=%q decisions=%+v", backend, decisions)
	}

	benchmark, err := gatecontract.NewGoBenchmarkWorkload(
		gatecontract.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		"BenchmarkRedact_NoMatch",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	selection.CacheMissWorkloads = append(selection.CacheMissWorkloads, benchmark)
	backend, _, err = selectAutoTestBackend(selection, input)
	if err != nil {
		t.Fatal(err)
	}
	if backend != autoTestBackendRemoteECI {
		t.Fatalf("benchmark backend=%q, want %q", backend, autoTestBackendRemoteECI)
	}
}

func TestSelectAutoTestBackendRoutesMultipleLightMissesToECI(t *testing.T) {
	first, input := autoTestLocalPolicyFixture(t)
	second, err := gatecontract.NewGoTestWorkload(
		gatecontract.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		"TestRedactAgain",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := gatecontract.NewGoPackageWorkload(
		gatecontract.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	input.LedgerSnapshot.Ledger.Samples = append(input.LedgerSnapshot.Ledger.Samples, gatecontract.DurationSample{
		Bucket: gatecontract.DurationBucket{
			WorkloadID:    gatecontract.GoTestDurationWorkloadID(parent.ID, "TestRedactAgain"),
			CommandDigest: gatecontract.GoTestDurationCommandDigest(parent.CommandDigest, "TestRedactAgain"),
			Platform:      input.Platform,
			Runner:        input.RunnerIdentityDigest,
			Toolchain:     input.ToolchainDigest,
		},
		Succeeded:           true,
		DurationMS:          100,
		TargetKind:          gatecontract.WorkloadKindGoTest,
		ParentWorkloadID:    parent.ID,
		ParentCommandDigest: parent.CommandDigest,
		TargetName:          "TestRedactAgain",
		TargetStatus:        gatecontract.GoTestStatusPass,
	})
	selection := remoteci.WorkloadCacheProbeResult{
		CacheMissWorkloads: []gatecontract.Workload{first, second},
	}
	backend, decisions, err := selectAutoTestBackend(selection, input)
	if err != nil {
		t.Fatal(err)
	}
	if backend != autoTestBackendRemoteECI || decisions != nil {
		t.Fatalf("multiple light misses backend=%q decisions=%v", backend, decisions)
	}
}

func TestExecuteLocalLightTestsRejectsUnfilteredBatch(t *testing.T) {
	workload, input := autoTestLocalPolicyFixture(t)
	result, err := executeLocalLightTests(
		input,
		remoteci.WorkloadCacheProbeResult{
			CacheMissWorkloads: []gatecontract.Workload{workload, workload},
		},
		map[string]remoteci.LocalLightTestDecision{
			workload.ID: {Eligible: true},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("execute unfiltered batch error = %v", err)
	}
	if result.Status != gatecontract.ResultStatusFailed {
		t.Fatalf("execute unfiltered batch status = %q", result.Status)
	}
}

func TestLocalLightTestEnvironmentDisablesPreparation(t *testing.T) {
	environment := "\n" + strings.Join(localLightTestEnvironment(), "\n") + "\n"
	for _, required := range []string{
		"\nGOENV=off\n",
		"\nGOFLAGS=-p=1 -mod=readonly\n",
		"\nGOMAXPROCS=1\n",
		"\nGOMEMLIMIT=768MiB\n",
		"\nGOPROXY=off\n",
		"\nGOSUMDB=off\n",
		"\nGOTOOLCHAIN=local\n",
		"\nSUPER_DOLPHIN_TEST_BACKEND=local-light\n",
	} {
		if !strings.Contains(environment, required) {
			t.Fatalf("local light environment omitted %q", strings.TrimSpace(required))
		}
	}
	if localLightTestTimeout > 3*time.Second {
		t.Fatalf("local light timeout = %s, want at most 3s", localLightTestTimeout)
	}
}

func TestParseAutoTestRunOptionsRequiresSelectorsAndOwnsScenario(t *testing.T) {
	options, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--test", "./internal/module/turn#TestRedact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Scenario != "test" || len(options.Tests) != 1 {
		t.Fatalf("auto test options = %#v", options)
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
	}); err == nil {
		t.Fatal("test command accepted no selectors")
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--scenario", "test",
		"--test", "./internal/module/turn#TestRedact",
	}); err == nil {
		t.Fatal("test command accepted a caller-owned scenario")
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--state", "/tmp/config.baseline-state.json",
		"--test", "./internal/module/turn#TestRedact",
	}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("legacy JSON state option error = %v", err)
	}
}

func TestLocalTestTailKeepsOnlyPlainTextTail(t *testing.T) {
	tail := newLocalTestTail(8)
	if _, err := tail.Write([]byte("first-")); err != nil {
		t.Fatal(err)
	}
	if _, err := tail.Write([]byte("last-line")); err != nil {
		t.Fatal(err)
	}
	if tail.String() != "ast-line" || !tail.Truncated() {
		t.Fatalf("tail=%q truncated=%v", tail.String(), tail.Truncated())
	}
}

func autoTestLocalPolicyFixture(t *testing.T) (gatecontract.Workload, remoteci.RunInput) {
	t.Helper()
	workload, err := gatecontract.NewGoTestWorkload(
		gatecontract.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		"TestRedact",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := gatecontract.NewGoPackageWorkload(
		gatecontract.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	input := remoteci.RunInput{
		Platform:             "linux/amd64",
		RunnerIdentityDigest: digest,
		ToolchainDigest:      digest,
	}
	input.LedgerSnapshot.Ledger.Samples = []gatecontract.DurationSample{
		{
			Bucket: gatecontract.DurationBucket{
				WorkloadID:    gatecontract.GoTestDurationWorkloadID(parent.ID, "TestRedact"),
				CommandDigest: gatecontract.GoTestDurationCommandDigest(parent.CommandDigest, "TestRedact"),
				Platform:      input.Platform,
				Runner:        input.RunnerIdentityDigest,
				Toolchain:     input.ToolchainDigest,
			},
			Succeeded:           true,
			DurationMS:          100,
			TargetKind:          gatecontract.WorkloadKindGoTest,
			ParentWorkloadID:    parent.ID,
			ParentCommandDigest: parent.CommandDigest,
			TargetName:          "TestRedact",
			TargetStatus:        gatecontract.GoTestStatusPass,
		},
		{
			Bucket: gatecontract.DurationBucket{
				WorkloadID:    parent.ID,
				CommandDigest: parent.CommandDigest,
				Platform:      input.Platform,
				Runner:        input.RunnerIdentityDigest,
				Toolchain:     input.ToolchainDigest,
			},
			Succeeded:  true,
			DurationMS: 500,
		},
	}
	return workload, input
}
