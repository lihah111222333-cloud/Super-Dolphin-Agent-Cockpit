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
	assertFrontendProgramCommand(t, GateIDFrontendFullTest, programs[GateIDFrontendFullTest], []string{"npm", "test"})
}

func TestVitestProgramMountsOriginalImageRuntimeSeeds(t *testing.T) {
	target := "src/example.test.js"
	program := vitestExecutorProgram(target)
	if !program.NeedsGoSeed || !program.NeedsFrontendSeed {
		t.Fatalf("Vitest seed contract = go:%t frontend:%t, want both original image seeds", program.NeedsGoSeed, program.NeedsFrontendSeed)
	}
	if !slices.Contains(program.RequiredPaths, "frontend-app/"+target) {
		t.Fatalf("Vitest required paths = %v, want target %q", program.RequiredPaths, target)
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

func assertFrontendProgramCommand(t *testing.T, id GateID, program ExecutorProgram, wantArgv []string) {
	t.Helper()
	if len(program.Steps) != 1 || !slices.Equal(program.Steps[0].Argv, wantArgv) || program.Steps[0].Directory != "frontend-app" {
		t.Fatalf("frontend test program %q = %#v, want frontend-app %v", id, program, wantArgv)
	}
}
