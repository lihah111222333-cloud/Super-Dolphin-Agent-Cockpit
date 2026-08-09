package gate

import (
	"slices"
	"testing"
)

func assertFrontendProgramsUsePinnedRuntimeInputs(t *testing.T, programs map[GateID]ExecutorProgram) {
	t.Helper()
	for _, id := range []GateID{GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendFullTest, GateIDFrontendBuild} {
		assertFrontendProgramUsesPinnedRuntimeInput(t, id, programs[id])
	}
	for _, id := range []GateID{GateIDFrontendTest, GateIDFrontendFullTest} {
		if !programs[id].NeedsGoSeed {
			t.Errorf("frontend test gate %q does not mount the original image Go seed", id)
		}
	}
	for _, id := range []GateID{GateIDFrontendLint, GateIDFrontendBuild} {
		if programs[id].NeedsGoSeed {
			t.Errorf("frontend non-test gate %q unexpectedly mounts the Go seed", id)
		}
	}
	assertFrontendProgramCommand(t, GateIDFrontendTest, programs[GateIDFrontendTest], []string{"npm", "run", "test:hook"})
	assertFrontendProgramCommand(t, GateIDFrontendFullTest, programs[GateIDFrontendFullTest], []string{"npm", "run", "test:full:body"})
}

func TestVitestProgramMountsOriginalImageRuntimeSeeds(t *testing.T) {
	target := "src/example.test.js"
	program, err := vitestExecutorProgram(GateIDFrontendTest, target)
	if err != nil {
		t.Fatal(err)
	}
	if !program.NeedsGoSeed || !program.NeedsFrontendSeed {
		t.Fatalf("Vitest seed contract = go:%t frontend:%t, want both original image seeds", program.NeedsGoSeed, program.NeedsFrontendSeed)
	}
	if !slices.Contains(program.RequiredPaths, "frontend-app/"+target) {
		t.Fatalf("Vitest required paths = %v, want target %q", program.RequiredPaths, target)
	}
}

func TestFrontendSuiteFallbackProgramsAreBodyOnlyAndPassNoTests(t *testing.T) {
	changedID, err := targetWorkloadID(GateIDFrontendTest, workloadTargetVitest, FrontendChangedSuiteCarrierTarget)
	if err != nil {
		t.Fatal(err)
	}
	_, changed, err := executorProgramForWorkload(GateID(changedID))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changed.Steps[0].Argv, []string{"npm", "run", "test:hook:core", "--", "--passWithNoTests"}) {
		t.Fatalf("changed suite argv = %v", changed.Steps[0].Argv)
	}
	fullID, err := targetWorkloadID(GateIDFrontendFullTest, workloadTargetVitest, FrontendFullSuiteCarrierTarget)
	if err != nil {
		t.Fatal(err)
	}
	_, full, err := executorProgramForWorkload(GateID(fullID))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(full.Steps[0].Argv, []string{"npm", "run", "test:full:body"}) {
		t.Fatalf("full suite argv = %v", full.Steps[0].Argv)
	}
}

func TestFrontendPreflightCarrierProgramsUseCanonicalCommands(t *testing.T) {
	for _, test := range []struct {
		target string
		script string
	}{
		{FrontendPreflightTargetCriticalGuards, "test:hook:preflight:critical-guards"},
		{FrontendPreflightTargetTurnContractVerify, "test:hook:preflight:turncontract-verify"},
		{FrontendPreflightTargetTurnContractFieldGuard, "test:hook:preflight:turncontract-field-guard"},
		{FrontendPreflightTargetCriticalTypecheck, "test:hook:preflight:critical-typecheck"},
		{FrontendPreflightTargetContractsVitest, "test:hook:preflight:contracts-check"},
		{FrontendPreflightTargetRPCAudit, "test:hook:preflight:rpc-audit"},
		{FrontendPreflightTargetDependencyContract, "test:hook:preflight:dependency-contract"},
	} {
		carrier, err := FrontendPreflightCarrierTarget(test.target)
		if err != nil {
			t.Fatal(err)
		}
		id, err := targetWorkloadID(GateIDFrontendTest, workloadTargetVitest, carrier)
		if err != nil {
			t.Fatal(err)
		}
		_, program, err := executorProgramForWorkload(GateID(id))
		if err != nil {
			t.Fatal(err)
		}
		assertFrontendProgramCommand(t, GateIDFrontendTest, program, []string{"npm", "run", test.script})
		if !slices.Contains(program.RequiredPaths, "frontend-app/"+carrier) {
			t.Fatalf("preflight carrier %q missing from required paths %v", carrier, program.RequiredPaths)
		}
	}
}

func TestPlaywrightShardProgramsMountPinnedFrontendSeed(t *testing.T) {
	for _, test := range []struct {
		target string
		spec   string
		grep   string
		script string
		config string
	}{
		{playwrightBusinessReadSurfacesTarget, "tests/e2e/business-flows.spec.js", "business-read-surfaces", "test:e2e:business", "playwright.business-flows.config.js"},
		{playwrightBusinessChatBridgeTarget, "tests/e2e/business-flows.spec.js", "business-chat-bridge", "test:e2e:business", "playwright.business-flows.config.js"},
		{playwrightDesktopShellTarget, "tests/e2e/desktop-wide.spec.js", "desktop-shell", "test:e2e:desktop-wide", "playwright.desktop-wide.config.js"},
		{playwrightDesktopBusinessPagesTarget, "tests/e2e/desktop-wide.spec.js", "desktop-business-pages", "test:e2e:desktop-wide", "playwright.desktop-wide.config.js"},
		{playwrightDesktopReadSettingsTarget, "tests/e2e/desktop-wide.spec.js", "desktop-read-settings", "test:e2e:desktop-wide", "playwright.desktop-wide.config.js"},
	} {
		t.Run(test.target, func(t *testing.T) {
			id, err := targetWorkloadID(GateIDFrontendE2E, workloadTargetPlaywright, test.target)
			if err != nil {
				t.Fatal(err)
			}
			parent, program, err := executorProgramForWorkload(GateID(id))
			if err != nil {
				t.Fatal(err)
			}
			if parent != GateIDFrontendE2E || !program.NeedsFrontendSeed || program.NeedsGoSeed {
				t.Fatalf("Playwright shard contract = parent:%q go:%t frontend:%t", parent, program.NeedsGoSeed, program.NeedsFrontendSeed)
			}
			assertFrontendProgramCommand(t, parent, program, []string{"npm", "run", test.script, "--", "--grep", test.grep})
			for _, path := range []string{"frontend-app/" + test.spec, "frontend-app/" + test.config} {
				if !slices.Contains(program.RequiredPaths, path) {
					t.Errorf("Playwright required paths = %v, want %q", program.RequiredPaths, path)
				}
			}
		})
	}
}

func assertFrontendProgramUsesPinnedRuntimeInput(t *testing.T, id GateID, program ExecutorProgram) {
	t.Helper()
	if !program.NeedsFrontendSeed {
		t.Errorf("frontend gate %q does not require the lock-bound node_modules seed", id)
	}
	for _, step := range program.Steps {
		if slices.Contains(step.Argv, "ci") {
			t.Errorf("frontend gate %q runs npm ci against an empty cache: %v", id, step.Argv)
		}
	}
}

func assertFrontendProgramCommand(t *testing.T, id GateID, program ExecutorProgram, wantArgv ...[]string) {
	t.Helper()
	if len(program.Steps) != len(wantArgv) {
		t.Fatalf("frontend test program %q = %#v, want %d frontend-app steps", id, program, len(wantArgv))
	}
	for index, want := range wantArgv {
		if !slices.Equal(program.Steps[index].Argv, want) || program.Steps[index].Directory != "frontend-app" {
			t.Fatalf("frontend test program %q step %d = %#v, want frontend-app %v", id, index, program.Steps[index], want)
		}
	}
}
