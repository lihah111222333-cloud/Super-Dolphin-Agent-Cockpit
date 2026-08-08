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
	if len(programs) != len(registry)-1 {
		t.Fatalf("executor programs = %d, registry gates = %d (one expansion-only gate)", len(programs), len(registry))
	}
	for _, spec := range registry {
		if isExpansionOnlyGate(spec.ID) {
			if _, ok := programs[spec.ID]; ok {
				t.Errorf("expansion-only gate %q unexpectedly has a canonical executor", spec.ID)
			}
			continue
		}
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
	if !slices.Equal(program.RequiredExecutables, []string{ExecutorSQLCBinaryPath, ExecutorBashBinaryPath}) {
		t.Fatalf("sqlc required executables = %v, want fixed runtime binary", program.RequiredExecutables)
	}
	if !slices.Contains(program.RequiredPaths, "scripts/sqlc_postprocess.sh") {
		t.Fatalf("sqlc required paths = %v, want canonical postprocessor", program.RequiredPaths)
	}
	if len(program.Steps) != 0 {
		t.Fatalf("sqlc executor unexpectedly delegates to mapped commands: %v", program.Steps)
	}
}

func TestAIMaintenanceExecutorRunsPinnedActionlintInsideContainer(t *testing.T) {
	program := ExecutorPrograms()[GateIDAIMaintenanceSelfTest]
	if len(program.Steps) < 1 || !slices.Equal(program.Steps[0].Argv, []string{"actionlint"}) {
		t.Fatalf("AI maintenance executor does not run actionlint first: %#v", program.Steps)
	}
	if !slices.Contains(program.RequiredExecutables, ExecutorActionlintBinaryPath) {
		t.Fatalf("AI maintenance executor does not require pinned actionlint: %v", program.RequiredExecutables)
	}
}

func TestProjectMapExecutorUsesTrustedCLIWithoutRepositoryGenerator(t *testing.T) {
	program := ExecutorPrograms()[GateIDProjectMapCheck]
	want := []string{ExecutorSelfCommandName, "project-map", "check", "--tree-from-index"}
	if len(program.Steps) != 1 || !slices.Equal(program.Steps[0].Argv, want) {
		t.Fatalf("project map executor steps = %#v, want trusted compiled CLI check", program.Steps)
	}
	wantPaths := []string{".git", "scripts/codemap_policy.txt", "docs/doc/codemap/project-map"}
	if !slices.Equal(program.RequiredPaths, wantPaths) {
		t.Fatalf("project map executor data inputs = %#v, want %#v", program.RequiredPaths, wantPaths)
	}
	if slices.Contains(program.RequiredPaths, "Makefile") || slices.Contains(program.RequiredPaths, "scripts/generate_ai_project_map.mjs") {
		t.Fatalf("project map executor trusts candidate repository entrypoints: %#v", program.RequiredPaths)
	}
	resolved, err := resolveStepExecutable(t.TempDir(), ExecutorSelfCommandName, ExecutorSearchPath)
	if err != nil {
		t.Fatalf("resolve current candidate gate executable: %v", err)
	}
	wantExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantExecutable, err = filepath.EvalSymlinks(wantExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != wantExecutable {
		t.Fatalf("resolved candidate gate = %q, want current executable %q", resolved, wantExecutable)
	}
	searchPath := filepath.SplitList(ExecutorSearchPath)
	assertImageSystemToolsAreAddressable(t, searchPath)
}

func assertImageSystemToolsAreAddressable(t *testing.T, searchPath []string) {
	t.Helper()
	for _, required := range []string{"/usr/local/go/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		if !slices.Contains(searchPath, required) {
			t.Fatalf("executor search path = %q, missing image system path %q", ExecutorSearchPath, required)
		}
	}
}

func TestBackendProgramUsesExecutorFrontendEmbedSeed(t *testing.T) {
	backend := ExecutorPrograms()[GateIDBackendTestWithGuard]
	if !backend.NeedsGoSeed || backend.NeedsFrontendSeed || !backend.NeedsFrontendEmbedSeed {
		t.Fatalf("backend gate seed contract = go:%t frontend:%t frontend_embed:%t", backend.NeedsGoSeed, backend.NeedsFrontendSeed, backend.NeedsFrontendEmbedSeed)
	}
	wantTest := ExecutorStep{
		Argv:        []string{"./scripts/test_with_guard.sh", "--canonical-backend", "./..."},
		Environment: []string{"GOFLAGS=-p=4", "GOMAXPROCS=4", "GOMEMLIMIT=6GiB"},
	}
	if !reflect.DeepEqual(backend.Steps, []ExecutorStep{wantTest}) {
		t.Fatalf("backend gate steps = %#v, want only canonical Go test", backend.Steps)
	}
	if !slices.Equal(backend.RequiredPaths, []string{"scripts/test_with_guard.sh", "scripts/check_nested_go_modules.sh"}) {
		t.Fatalf("backend gate required paths = %v", backend.RequiredPaths)
	}
}

func TestRaceProgramUsesExecutorFrontendEmbedSeed(t *testing.T) {
	programs := ExecutorPrograms()
	race := programs[GateIDBackendTestGuardWithRace]
	if !race.NeedsGoSeed || race.NeedsFrontendSeed || !race.NeedsFrontendEmbedSeed {
		t.Fatalf("race gate seed contract = go:%t frontend:%t frontend_embed:%t", race.NeedsGoSeed, race.NeedsFrontendSeed, race.NeedsFrontendEmbedSeed)
	}
	wantRaceArgv := append([]string{"./scripts/test_with_guard.sh", "--with-race"}, RaceSensitivePackagePatterns()...)
	wantRaceArgv = append(wantRaceArgv, "--")
	wantRaceArgv = append(wantRaceArgv, canonicalBackendPackagePatterns()...)
	wantRaceArgv = append(wantRaceArgv, "-count=1", "-timeout=180s")
	wantRaceStep := ExecutorStep{Argv: wantRaceArgv, Environment: []string{"GOFLAGS=-p=4", "GOMAXPROCS=4", "GOMEMLIMIT=6GiB"}}
	if !reflect.DeepEqual(race.Steps, []ExecutorStep{wantRaceStep}) {
		t.Fatalf("race executor steps = %#v, want only bounded Go race test", race.Steps)
	}
	if !slices.Equal(race.RequiredPaths, []string{"scripts/test_with_guard.sh", "scripts/check_nested_go_modules.sh"}) {
		t.Fatalf("race executor required paths = %v", race.RequiredPaths)
	}
}

func TestNilnessProgramUsesBoundedGoResources(t *testing.T) {
	if _, ok := ExecutorPrograms()[GateIDBackendNilness]; ok {
		t.Fatal("nilness gate unexpectedly retained a canonical executor")
	}
	if _, _, err := executorProgramForWorkload(GateIDBackendNilness); err == nil || !strings.Contains(err.Error(), "expanded workload") {
		t.Fatalf("canonical nilness executor error = %v, want expansion-only failure", err)
	}
}

func TestNilnessPackageProgramUsesTheAnalyzerForOnePackage(t *testing.T) {
	id, err := targetWorkloadID(GateIDBackendNilness, workloadTargetGoPackage, "./internal/alpha")
	if err != nil {
		t.Fatalf("targetWorkloadID() error = %v", err)
	}
	parent, program, err := executorProgramForWorkload(GateID(id))
	if err != nil {
		t.Fatalf("executorProgramForWorkload() error = %v", err)
	}
	if parent != GateIDBackendNilness {
		t.Fatalf("parent gate = %q, want %q", parent, GateIDBackendNilness)
	}
	want := ExecutorStep{
		Argv:        []string{"go", "run", "./scripts/nilness_guard.go", "-test=false", "--", "./internal/alpha"},
		Environment: []string{"GOFLAGS=-p=2", "GOMAXPROCS=2", "GOMEMLIMIT=3GiB"},
	}
	if !reflect.DeepEqual(program.Steps, []ExecutorStep{want}) {
		t.Fatalf("nilness package steps = %#v, want one package analyzer invocation", program.Steps)
	}
	if !program.NeedsGoSeed || program.NeedsFrontendSeed || program.NeedsFrontendEmbedSeed {
		t.Fatalf("nilness package seeds = go:%t frontend:%t frontend_embed:%t", program.NeedsGoSeed, program.NeedsFrontendSeed, program.NeedsFrontendEmbedSeed)
	}
	if !slices.Equal(program.RequiredPaths, []string{"scripts/nilness_guard.go"}) {
		t.Fatalf("nilness package required paths = %v", program.RequiredPaths)
	}
}

func TestFrontendProgramsUsePinnedRuntimeInputs(t *testing.T) {
	programs := ExecutorPrograms()
	assertFrontendProgramsUsePinnedRuntimeInputs(t, programs)
}

func TestFrontendPreflightProgramRunsPreflightAndDependencyIntegrityOnly(t *testing.T) {
	program := ExecutorPrograms()[GateIDFrontendPreflight]
	want := [][]string{
		{"npm", "run", "test:hook:preflight"},
		{"npm", "run", "test:hook:dependency-integrity"},
	}
	if len(program.Steps) != len(want) {
		t.Fatalf("frontend preflight steps = %#v, want %v", program.Steps, want)
	}
	for index, step := range program.Steps {
		if !slices.Equal(step.Argv, want[index]) || step.Directory != "frontend-app" {
			t.Fatalf("frontend preflight step %d = %#v, want frontend-app %v", index, step, want[index])
		}
		if slices.Contains(step.Argv, "playwright") || slices.Contains(step.Argv, "e2e") {
			t.Fatalf("frontend preflight step %d entered E2E execution: %v", index, step.Argv)
		}
	}
}

func TestBackendRaceExecutorCombinesCanonicalAndRegisteredPackages(t *testing.T) {
	race := ExecutorPrograms()[GateIDBackendTestGuardWithRace]
	if len(race.Steps) != 1 {
		t.Fatalf("race executor steps = %d, want only race test", len(race.Steps))
	}
	argv := race.Steps[0].Argv
	separator := slices.Index(argv, "--")
	if separator < 0 {
		t.Fatalf("race executor argv omits the canonical normal-test segment: %v", argv)
	}
	packages := argv[2:separator]
	if !slices.Equal(packages, RaceSensitivePackagePatterns()) {
		t.Fatalf("race executor argv registry drifted: packages=%v", packages)
	}
	wantNormal := append(canonicalBackendPackagePatterns(), "-count=1", "-timeout=180s")
	if !slices.Equal(argv[separator+1:], wantNormal) {
		t.Fatalf("race executor canonical package segment = %v, want %v", argv[separator+1:], wantNormal)
	}
}

func TestRaceSensitivePackageAndPathRegistriesStayAligned(t *testing.T) {
	assertRaceSensitiveRegistries(t, RaceSensitivePackagePatterns(), RaceSensitivePathPrefixes())
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
