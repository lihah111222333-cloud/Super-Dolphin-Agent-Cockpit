package gate

import (
	"os"
	"path/filepath"
	"reflect"
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
	args := []string{"run", "--gate", string(GateIDBackendTestGuardWithRace)}
	id, first, err := ParseExecutorCommand(args)
	if err != nil {
		t.Fatalf("ParseExecutorCommand: %v", err)
	}
	if id != GateIDBackendTestGuardWithRace {
		t.Fatalf("gate id = %q, want %q", id, GateIDBackendTestGuardWithRace)
	}
	first.Steps[0].Argv[0] = "mutated"
	first.Steps[0].Environment[0] = "mutated"
	_, second, err := ParseExecutorCommand(args)
	if err != nil {
		t.Fatalf("ParseExecutorCommand second call: %v", err)
	}
	if slices.Equal(first.Steps[0].Argv, second.Steps[0].Argv) || slices.Equal(first.Steps[0].Environment, second.Steps[0].Environment) {
		t.Fatal("executor program mutation leaked into the canonical table")
	}
}

func TestReleaseExecutorUsesSameProcessAttestationWithoutCommands(t *testing.T) {
	program := ExecutorPrograms()[GateIDReleaseLayeredCheck]
	if program.Strategy != ExecutorStrategyReleaseAttestation || len(program.Steps) != 0 ||
		program.NeedsGoSeed || program.NeedsFrontendSeed || len(program.RequiredPaths) != 0 {
		t.Fatalf("release attestation must be a command-free plan-executor strategy: %#v", program)
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

func TestBackendProgramBuildsFrontendEmbedInsideFreshContainer(t *testing.T) {
	backend := ExecutorPrograms()[GateIDBackendTestWithGuard]
	if !backend.NeedsGoSeed || !backend.NeedsFrontendSeed {
		t.Fatalf("backend gate seed contract = go:%t frontend:%t", backend.NeedsGoSeed, backend.NeedsFrontendSeed)
	}
	wantBuild := ExecutorStep{Directory: "frontend-app", Argv: []string{"npm", "run", "build"}}
	wantTest := ExecutorStep{
		Argv:        []string{"./scripts/test_with_guard.sh", "--canonical-backend", "./cmd/...", "./internal/...", "./pkg/...", "./scripts/..."},
		Environment: []string{"GOFLAGS=-p=2", "GOMAXPROCS=2", "GOMEMLIMIT=1GiB"},
	}
	if len(backend.Steps) != 2 || !reflect.DeepEqual(backend.Steps[0], wantBuild) ||
		!reflect.DeepEqual(backend.Steps[1], wantTest) {
		t.Fatalf("backend gate does not build its immutable frontend embed before canonical tests: %#v", backend.Steps)
	}
}

func TestRaceAndFrontendProgramsUsePinnedRuntimeInputs(t *testing.T) {
	programs := ExecutorPrograms()
	race := programs[GateIDBackendTestGuardWithRace]
	if !race.NeedsGoSeed {
		t.Fatal("race gate does not require the lock-bound Go seed")
	}
	if race.NeedsFrontendSeed {
		t.Fatal("race gate must not install or build frontend inputs")
	}
	wantRaceArgv := append([]string{"./scripts/test_with_guard.sh", "--race-only"}, RaceSensitivePackagePatterns()...)
	wantRaceArgv = append(wantRaceArgv, "-timeout=180s")
	wantRaceSteps := []ExecutorStep{
		{Argv: wantRaceArgv, Environment: []string{"GOFLAGS=-p=1", "GOMAXPROCS=1", "GOMEMLIMIT=1GiB"}},
	}
	if !reflect.DeepEqual(race.Steps, wantRaceSteps) {
		t.Fatalf("race executor is not bounded to registered concurrency surfaces: %#v", race.Steps)
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

func TestBackendRaceExecutorUsesOnlyRegisteredPackages(t *testing.T) {
	argv := ExecutorPrograms()[GateIDBackendTestGuardWithRace].Steps[0].Argv
	if slices.Contains(argv, "--") {
		t.Fatalf("race executor argv includes a duplicate normal-test segment: %v", argv)
	}
	packages := argv[2 : len(argv)-1]
	if !slices.Equal(packages, RaceSensitivePackagePatterns()) {
		t.Fatalf("race executor argv registry drifted: packages=%v", packages)
	}
}

func TestRaceSensitivePackageAndPathRegistriesStayAligned(t *testing.T) {
	patterns := RaceSensitivePackagePatterns()
	prefixes := RaceSensitivePathPrefixes()
	if len(patterns) == 0 || len(patterns) != len(prefixes) {
		t.Fatalf("race registry lengths = patterns:%d prefixes:%d", len(patterns), len(prefixes))
	}
	for index, prefix := range prefixes {
		exactPackage := "./" + strings.TrimSuffix(prefix, "/")
		recursivePackage := "./" + prefix + "..."
		if prefix == "" || !strings.HasSuffix(prefix, "/") ||
			(patterns[index] != exactPackage && patterns[index] != recursivePackage) {
			t.Fatalf("race registry entry %d = pattern:%q prefix:%q", index, patterns[index], prefix)
		}
	}
	if slices.Contains(patterns, "./cmd/...") || slices.Contains(patterns, "./cmd/agent-terminal/...") {
		t.Fatalf("race registry includes agent-terminal through an unbounded command pattern: %v", patterns)
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
