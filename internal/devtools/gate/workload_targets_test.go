package gate

import (
	"reflect"
	"testing"
)

func TestParsePlaywrightE2ETargetBindsSpecAndDescribe(t *testing.T) {
	for _, test := range []struct {
		target string
		spec   string
		grep   string
	}{
		{playwrightBusinessReadSurfacesTarget, "tests/e2e/business-flows.spec.js", "business-read-surfaces"},
		{playwrightBusinessChatBridgeTarget, "tests/e2e/business-flows.spec.js", "business-chat-bridge"},
		{playwrightDesktopShellTarget, "tests/e2e/desktop-wide.spec.js", "desktop-shell"},
		{playwrightDesktopBusinessPagesTarget, "tests/e2e/desktop-wide.spec.js", "desktop-business-pages"},
		{playwrightDesktopReadSettingsTarget, "tests/e2e/desktop-wide.spec.js", "desktop-read-settings"},
	} {
		spec, grep, err := ParsePlaywrightE2ETarget(test.target)
		if err != nil {
			t.Fatalf("ParsePlaywrightE2ETarget(%q): %v", test.target, err)
		}
		if spec != test.spec || grep != test.grep {
			t.Fatalf("ParsePlaywrightE2ETarget(%q) = (%q, %q), want (%q, %q)", test.target, spec, grep, test.spec, test.grep)
		}
	}
	for _, target := range []string{
		"tests/e2e/business-flows.spec.js",
		"tests/e2e/business-flows.spec.js#unknown",
		"tests/e2e/business-flows.spec.js#business-read-surfaces#extra",
		"tests/e2e/business-flows.spec.js#business-read-surfaces|.*",
		"tests/e2e/business-flows.spec.js#business-read-surfaces --project=evil",
	} {
		if _, _, err := ParsePlaywrightE2ETarget(target); err == nil {
			t.Fatalf("invalid Playwright target %q was accepted", target)
		}
	}
}

func TestGoPackageTargetForSourceUsesModuleAgnosticCanonicalPaths(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{file: "build/gate/closure_test.go", want: "./build/gate"},
		{file: "build/gate/closure/runtime_deps_test.go", want: "./build/gate/closure"},
		{file: "cmd/super-dolphin-gate/main.go", want: "./cmd/super-dolphin-gate"},
		{file: "internal/devtools/remoteci/inventory.go", want: "./internal/devtools/remoteci"},
		{file: "new-root/worker/worker_test.go", want: "./new-root/worker"},
		{file: "root_test.go", want: "./."},
		{file: "build/gate/runtime-tools/tools.go", want: "./build/gate/runtime-tools"},
		{file: "build/gate/closure/testdata/fixture.go"},
		{file: "testdata/fixture.go"},
		{file: ".generated/fixture.go"},
		{file: "../internal/escape.go"},
	}
	for _, tc := range cases {
		got, ok := GoPackageTargetForSource(tc.file)
		if ok != (tc.want != "") || got != tc.want {
			t.Errorf("GoPackageTargetForSource(%q) = (%q, %v), want (%q, %v)", tc.file, got, ok, tc.want, tc.want != "")
		}
	}
	for _, target := range []string{"./build/gate", "./build/gate/closure", "./new-root/worker"} {
		if _, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, target, 1); err != nil {
			t.Fatalf("NewGoPackageWorkload(%q) error = %v", target, err)
		}
	}
	for _, target := range []string{"../escape", "./internal/../cmd", "./bad path", "./internal/..."} {
		if _, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, target, 1); err == nil {
			t.Fatalf("invalid Go package workload %q was accepted", target)
		}
	}
}

func TestGoTestWorkloadBindsPackageTestAndExecutionSemantics(t *testing.T) {
	workload := mustGoTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	parent, kind, target, targeted := mustParseTargetedWorkload(t, workload.ID)
	parsed, err := ParseGoTestTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	assertGoTestWorkloadTarget(t, parent, kind, targeted, parsed)
	program := mustExecutorProgramForWorkload(t, workload.ID)
	want := []string{"./scripts/test_with_guard.sh", "--ci-package-test", "./internal/archtest", "TestBoundary"}
	assertSingleExecutorStep(t, program, want)
	other := mustGoTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestOther")
	if workload.ID == other.ID || workload.CommandDigest == other.CommandDigest {
		t.Fatal("different Go tests share workload or execution identity")
	}
}

func TestGoTestWorkloadUsesRaceEntrypointAndRejectsInvalidNames(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestGuardWithRace, "./internal/archtest", "TestBoundary", 250)
	if err != nil {
		t.Fatal(err)
	}
	_, program, err := executorProgramForWorkload(GateID(workload.ID))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./scripts/test_with_guard.sh", "--ci-race-package-test", "./internal/archtest", "TestBoundary"}
	if len(program.Steps) != 1 || !reflect.DeepEqual(program.Steps[0].Argv, want) {
		t.Fatalf("race Go test executor argv = %#v, want %#v", program.Steps, want)
	}
	for _, name := range []string{"helper", "Testlower", "TestA/B", "Test A"} {
		if _, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", name, 250); err == nil {
			t.Fatalf("invalid Go test name %q was accepted", name)
		}
	}
}

func TestGoBenchmarkWorkloadIsExplicitAndUsesRemoteBenchmarkEntrypoint(t *testing.T) {
	workload := mustGoBenchmarkWorkload(t, GateIDBackendTestWithGuard)
	parent, kind, target, targeted := mustParseTargetedWorkload(t, workload.ID)
	parsed, err := ParseGoBenchmarkTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	assertGoBenchmarkWorkloadTarget(t, parent, kind, targeted, parsed)
	program := mustExecutorProgramForWorkload(t, workload.ID)
	want := []string{
		"./scripts/test_with_guard.sh",
		"--ci-package-benchmark",
		"./internal/module/turn",
		"BenchmarkRedact_NoMatch",
	}
	assertSingleExecutorStep(t, program, want)
	if _, err := NewGoBenchmarkWorkload(
		GateIDBackendTestGuardWithRace,
		"./internal/module/turn",
		"BenchmarkRedact_NoMatch",
		5000,
	); err == nil {
		t.Fatal("race benchmark workload was accepted")
	}
}

func TestSplitGoGuardWorkloadsUseFixedEntrypoints(t *testing.T) {
	nestedRuntimeProxy, err := NestedGoModuleGuardTarget("build/gate/runtime-proxy")
	if err != nil {
		t.Fatal(err)
	}
	nestedCustom, err := NestedGoModuleGuardTarget("tools/custom-check")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		target            string
		wantArgv          []string
		wantFrontendEmbed bool
	}{
		{GoGuardTargetSource, []string{"./scripts/test_with_guard.sh", "--ci-guard-source"}, true},
		{GoGuardTargetSourceRawGoTest, []string{"./scripts/forbid_raw_go_test.sh"}, false},
		{GoGuardTargetCopylocksProvider, []string{"./scripts/test_with_guard.sh", "--ci-copylocks", "provider"}, false},
		{GoGuardTargetCopylocksPlatform, []string{"./scripts/test_with_guard.sh", "--ci-copylocks", "platform"}, false},
		{GoGuardTargetCopylocksThread, []string{"./scripts/test_with_guard.sh", "--ci-copylocks", "thread"}, false},
		{nestedRuntimeProxy, []string{"./scripts/test_with_guard.sh", "--ci-nested-module", "build/gate/runtime-proxy"}, false},
		{nestedCustom, []string{"./scripts/test_with_guard.sh", "--ci-nested-module", "tools/custom-check"}, false},
	}
	for _, test := range tests {
		t.Run(test.target, func(t *testing.T) {
			workloadID, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoGuard, test.target)
			if err != nil {
				t.Fatal(err)
			}
			program := mustExecutorProgramForWorkload(t, workloadID)
			assertSingleExecutorStep(t, program, test.wantArgv)
			if program.NeedsFrontendEmbedSeed != test.wantFrontendEmbed {
				t.Fatalf("frontend embed seed = %t, want %t", program.NeedsFrontendEmbedSeed, test.wantFrontendEmbed)
			}
		})
	}
	if _, err := targetWorkloadID(GateIDBackendTestGuardWithRace, workloadTargetGoGuard, GoGuardTargetSource); err == nil {
		t.Fatal("race gate accepted a duplicated split Go guard")
	}
	legacy := []struct {
		parent   GateID
		wantArgv []string
	}{
		{GateIDBackendTestWithGuard, []string{"./scripts/test_with_guard.sh", "--ci-guard"}},
		{GateIDBackendTestGuardWithRace, []string{"./scripts/test_with_guard.sh", "--ci-race-guard"}},
	}
	for _, test := range legacy {
		workloadID, err := targetWorkloadID(test.parent, workloadTargetGoGuard, GoGuardTargetCanonical)
		if err != nil {
			t.Fatalf("legacy canonical guard %q: %v", test.parent, err)
		}
		assertSingleExecutorStep(t, mustExecutorProgramForWorkload(t, workloadID), test.wantArgv)
	}
}

func TestAIMaintenanceGuardWorkloadsUseIndependentEntrypoints(t *testing.T) {
	tests := []struct {
		target string
		argv   []string
	}{
		{GoGuardTargetAIMaintenanceUnit, []string{"go", "test", "./scripts/ai_maintenance", "-count=1"}},
		{GoGuardTargetAIMaintenanceGate, []string{"go", "test", "./scripts", "-run", "^TestAIMaintenanceGate", "-count=1"}},
	}
	for _, test := range tests {
		workloadID, err := targetWorkloadID(GateIDAIMaintenanceSelfTest, workloadTargetGoGuard, test.target)
		if err != nil {
			t.Fatal(err)
		}
		program := mustExecutorProgramForWorkload(t, workloadID)
		assertSingleExecutorStep(t, program, test.argv)
		if !program.NeedsGoSeed || len(program.RequiredExecutables) != 1 || program.RequiredExecutables[0] != ExecutorActionlintBinaryPath {
			t.Fatalf("AI maintenance target %q prerequisites = %#v", test.target, program)
		}
	}
	if _, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoGuard, GoGuardTargetAIMaintenanceUnit); err == nil {
		t.Fatal("backend gate accepted an AI maintenance workload")
	}
}

func mustGoTestWorkload(
	t *testing.T,
	parent GateID,
	packageTarget string,
	testName string,
) Workload {
	t.Helper()
	workload, err := NewGoTestWorkload(parent, packageTarget, testName, 250)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func mustGoBenchmarkWorkload(t *testing.T, parent GateID) Workload {
	t.Helper()
	workload, err := NewGoBenchmarkWorkload(
		parent,
		"./internal/module/turn",
		"BenchmarkRedact_NoMatch",
		5000,
	)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func mustParseTargetedWorkload(t *testing.T, workloadID string) (GateID, WorkloadTargetKind, string, bool) {
	t.Helper()
	parent, kind, target, targeted, err := ParseWorkloadID(workloadID)
	if err != nil {
		t.Fatal(err)
	}
	return parent, kind, target, targeted
}

func mustExecutorProgramForWorkload(t *testing.T, workloadID string) ExecutorProgram {
	t.Helper()
	_, program, err := executorProgramForWorkload(GateID(workloadID))
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func assertSingleExecutorStep(t *testing.T, program ExecutorProgram, want []string) {
	t.Helper()
	if len(program.Steps) != 1 || !reflect.DeepEqual(program.Steps[0].Argv, want) {
		t.Fatalf("executor argv = %#v, want %#v", program.Steps, want)
	}
}

func assertGoTestWorkloadTarget(
	t *testing.T,
	parent GateID,
	kind WorkloadTargetKind,
	targeted bool,
	target GoTestTarget,
) {
	t.Helper()
	if parent != GateIDBackendTestWithGuard || kind != WorkloadTargetGoTest || !targeted ||
		target.Package != "./internal/archtest" || target.Name != "TestBoundary" {
		t.Fatalf("parsed Go test workload = parent=%q kind=%q targeted=%v target=%+v", parent, kind, targeted, target)
	}
}

func assertGoBenchmarkWorkloadTarget(
	t *testing.T,
	parent GateID,
	kind WorkloadTargetKind,
	targeted bool,
	target GoTestTarget,
) {
	t.Helper()
	if parent != GateIDBackendTestWithGuard || kind != WorkloadTargetGoBenchmark || !targeted ||
		target.Package != "./internal/module/turn" || target.Name != "BenchmarkRedact_NoMatch" {
		t.Fatalf("parsed benchmark workload = parent=%q kind=%q targeted=%v target=%+v", parent, kind, targeted, target)
	}
}
