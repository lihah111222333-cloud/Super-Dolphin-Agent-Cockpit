package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestExecutorProgramsCoverCanonicalGateRegistry(t *testing.T) {
	programs := ExecutorPrograms()
	registry := GateRegistry()
	if len(programs) != len(registry) {
		t.Fatalf("executor programs = %d, registry gates = %d", len(programs), len(registry))
	}
	for _, spec := range registry {
		program, ok := programs[spec.ID]
		if !ok {
			t.Errorf("canonical gate %q has no executor program", spec.ID)
			continue
		}
		if err := validateExecutorProgram(program); err != nil {
			t.Errorf("canonical gate %q has invalid executor program: %v", spec.ID, err)
		}
		delete(programs, spec.ID)
	}
	for id := range programs {
		t.Errorf("executor program %q is not in the canonical registry", id)
	}
}

func TestParseExecutorCommandFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing all"},
		{name: "missing gate", args: []string{"run", "--gate"}},
		{name: "missing flag", args: []string{"run", string(GateIDCodemapCheck)}},
		{name: "wrong verb", args: []string{"plan", "--gate", string(GateIDCodemapCheck)}},
		{name: "wrong flag", args: []string{"run", "--profile", string(GateIDCodemapCheck)}},
		{name: "equals syntax", args: []string{"run", "--gate=" + string(GateIDCodemapCheck)}},
		{name: "duplicate flag", args: []string{"run", "--gate", string(GateIDCodemapCheck), "--gate", string(GateIDCodemapCheck)}},
		{name: "extra argument", args: []string{"run", "--gate", string(GateIDCodemapCheck), "extra"}},
		{name: "unknown", args: []string{"run", "--gate", "unknown:gate"}},
		{name: "injection", args: []string{"run", "--gate", "codemap:check;touch /tmp/pwned"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := ParseExecutorCommand(test.args); err == nil {
				t.Fatal("ParseExecutorCommand unexpectedly accepted invalid arguments")
			}
		})
	}
}

func TestParseExecutorCommandReturnsDefensiveProgramCopy(t *testing.T) {
	args := []string{"run", "--gate", string(GateIDFrontendLint)}
	id, first, err := ParseExecutorCommand(args)
	if err != nil {
		t.Fatalf("ParseExecutorCommand: %v", err)
	}
	if id != GateIDFrontendLint {
		t.Fatalf("gate id = %q, want %q", id, GateIDFrontendLint)
	}
	first.Steps[0].Argv[0] = "mutated"
	_, second, err := ParseExecutorCommand(args)
	if err != nil {
		t.Fatalf("ParseExecutorCommand second call: %v", err)
	}
	if slices.Equal(first.Steps[0].Argv, second.Steps[0].Argv) {
		t.Fatal("executor program mutation leaked into the canonical table")
	}
}

func TestReleaseExecutorUsesCanonicalLayeredTargetWithoutClaudeOverride(t *testing.T) {
	program := ExecutorPrograms()[GateIDReleaseLayeredCheck]
	want := []string{"make", "ci-l3-release"}
	if len(program.Steps) != 1 || !slices.Equal(program.Steps[0].Argv, want) {
		t.Fatalf("release executor argv = %v, want %v", program.Steps, want)
	}
}

func TestSQLCExecutorUsesPinnedRuntimeBinary(t *testing.T) {
	program := ExecutorPrograms()[GateIDSQLCVerify]
	if program.Strategy != ExecutorStrategySQLCVerify {
		t.Fatalf("sqlc executor strategy = %q, want %q", program.Strategy, ExecutorStrategySQLCVerify)
	}
	if !slices.Equal(program.RequiredExecutables, []string{ExecutorSQLCBinaryPath}) {
		t.Fatalf("sqlc required executables = %v, want fixed runtime binary", program.RequiredExecutables)
	}
	if len(program.Steps) != 0 {
		t.Fatalf("sqlc executor unexpectedly delegates to mapped commands: %v", program.Steps)
	}
}

func TestRaceAndFrontendProgramsUsePinnedRuntimeInputs(t *testing.T) {
	programs := ExecutorPrograms()
	race := programs[GateIDBackendTestGuardWithRace]
	if len(race.Steps) != 1 || !slices.Contains(race.Steps[0].Argv, "--with-race") || slices.Contains(race.Steps[0].Argv, "make") {
		t.Fatalf("race executor is not a real guard+race command: %v", race.Steps)
	}
	for _, id := range []GateID{GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild} {
		program := programs[id]
		if !program.NeedsFrontendSeed {
			t.Errorf("frontend gate %q does not require the lock-bound node_modules seed", id)
		}
		for _, step := range program.Steps {
			if slices.Contains(step.Argv, "ci") {
				t.Errorf("frontend gate %q runs npm ci against an empty cache: %v", id, step.Argv)
			}
		}
	}
	if programs[GateIDLSPChangedDiagnostics].Strategy != ExecutorStrategyChangedDiagnostics {
		t.Fatal("LSP gate does not derive changed targets from snapshot Git truth")
	}
}

func TestExecutorProgramRepositoryEntrypointsExist(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the test file")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	for id, program := range ExecutorPrograms() {
		assertExecutorProgramEntrypoints(t, repositoryRoot, id, program)
	}
}

func assertExecutorProgramEntrypoints(t *testing.T, repositoryRoot string, id GateID, program ExecutorProgram) {
	t.Helper()
	for _, relative := range program.RequiredPaths {
		if _, err := os.Lstat(filepath.Join(repositoryRoot, relative)); err != nil {
			t.Errorf("gate %q required path %q: %v", id, relative, err)
		}
	}
	for _, step := range program.Steps {
		assertRepositoryCommand(t, repositoryRoot, id, step)
	}
}

func assertRepositoryCommand(t *testing.T, repositoryRoot string, id GateID, step ExecutorStep) {
	t.Helper()
	if len(step.Argv) == 0 || !strings.HasPrefix(step.Argv[0], "./") {
		return
	}
	info, err := os.Stat(filepath.Join(repositoryRoot, filepath.Clean(step.Argv[0])))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("gate %q repository command %q is missing or not executable", id, step.Argv[0])
	}
}
