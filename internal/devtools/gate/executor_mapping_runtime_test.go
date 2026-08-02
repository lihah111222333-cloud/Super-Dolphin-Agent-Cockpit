package gate

import (
	"slices"
	"testing"
)

func assertFrontendProgramsUsePinnedRuntimeInputs(t *testing.T, programs map[GateID]ExecutorProgram) {
	t.Helper()
	for _, id := range []GateID{GateIDFrontendLint, GateIDFrontendPreflight, GateIDFrontendTest, GateIDFrontendFullTest, GateIDFrontendBuild} {
		assertFrontendProgramUsesPinnedRuntimeInput(t, id, programs[id])
	}
	for _, id := range []GateID{GateIDFrontendPreflight, GateIDFrontendTest, GateIDFrontendFullTest} {
		if !programs[id].NeedsGoSeed {
			t.Errorf("frontend test gate %q does not mount the original image Go seed", id)
		}
	}
	for _, id := range []GateID{GateIDFrontendLint, GateIDFrontendBuild} {
		if programs[id].NeedsGoSeed {
			t.Errorf("frontend non-test gate %q unexpectedly mounts the Go seed", id)
		}
	}
	assertFrontendProgramCommand(t, GateIDFrontendPreflight, programs[GateIDFrontendPreflight], []string{"npm", "run", "test:hook:preflight"}, []string{"npm", "run", "test:hook:dependency-integrity"})
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

func TestPlaywrightShardProgramsMountPinnedFrontendSeed(t *testing.T) {
	for _, test := range []struct {
		spec   string
		script string
		config string
	}{
		{playwrightBusinessFlowsSpec, "test:e2e:business", "playwright.business-flows.config.js"},
		{playwrightDesktopWideSpec, "test:e2e:desktop-wide", "playwright.desktop-wide.config.js"},
	} {
		t.Run(test.spec, func(t *testing.T) {
			id, err := targetWorkloadID(GateIDFrontendE2E, workloadTargetPlaywright, test.spec)
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
			assertFrontendProgramCommand(t, parent, program, []string{"npm", "run", test.script})
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
