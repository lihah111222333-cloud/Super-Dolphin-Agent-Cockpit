package gate

import (
	"errors"
	"fmt"
	"slices"
)

// ExecutorStrategy identifies the fixed execution behavior for a gate.
type ExecutorStrategy string

const (
	ExecutorStrategyCommands           ExecutorStrategy = "commands"
	ExecutorStrategyChangedDiagnostics ExecutorStrategy = "changed-diagnostics"
	ExecutorStrategyFullTreeWhitespace ExecutorStrategy = "full-tree-whitespace"
	ExecutorStrategySQLCVerify         ExecutorStrategy = "sqlc-verify"
	ExecutorStrategyReleaseAttestation ExecutorStrategy = "release-attestation"
)

// ExecutorStep is one shell-free command in a gate program.
type ExecutorStep struct {
	Directory   string
	Argv        []string
	Environment []string
}

const (
	executorNormalGoFlagsResourceBound    = "GOFLAGS=-p=2"
	executorNormalGoMaxProcsResourceBound = "GOMAXPROCS=2"
	executorRaceGoFlagsResourceBound      = "GOFLAGS=-p=1"
	executorRaceGoMaxProcsResourceBound   = "GOMAXPROCS=1"
	executorGoMemoryLimitResourceBound    = "GOMEMLIMIT=1GiB"
)

var raceSensitiveSurfaces = []struct {
	packagePattern string
	pathPrefix     string
}{
	{packagePattern: "./cmd/mcp-ida/...", pathPrefix: "cmd/mcp-ida/"},
	{packagePattern: "./cmd/mcp-lsp/...", pathPrefix: "cmd/mcp-lsp/"},
	{packagePattern: "./cmd/mcp-orch/...", pathPrefix: "cmd/mcp-orch/"},
	{packagePattern: "./cmd/agent-terminal", pathPrefix: "cmd/agent-terminal/"},
	{packagePattern: "./cmd/super-dolphin-gate/...", pathPrefix: "cmd/super-dolphin-gate/"},
	{packagePattern: "./cmd/super-dolphin-updater", pathPrefix: "cmd/super-dolphin-updater/"},
	{packagePattern: "./internal/app/...", pathPrefix: "internal/app/"},
	{packagePattern: "./internal/contract/...", pathPrefix: "internal/contract/"},
	{packagePattern: "./internal/devtools/gate/...", pathPrefix: "internal/devtools/gate/"},
	{packagePattern: "./internal/devtools/localci/...", pathPrefix: "internal/devtools/localci/"},
	{packagePattern: "./internal/mcpserver/...", pathPrefix: "internal/mcpserver/"},
	{packagePattern: "./internal/module/...", pathPrefix: "internal/module/"},
	{packagePattern: "./internal/platform/...", pathPrefix: "internal/platform/"},
	{packagePattern: "./internal/provider/...", pathPrefix: "internal/provider/"},
	{packagePattern: "./internal/store/...", pathPrefix: "internal/store/"},
	{packagePattern: "./internal/ui/...", pathPrefix: "internal/ui/"},
	{packagePattern: "./internal/util/...", pathPrefix: "internal/util/"},
	{packagePattern: "./pkg/...", pathPrefix: "pkg/"},
	{packagePattern: "./scripts/lsp_diagnostics_gate/...", pathPrefix: "scripts/lsp_diagnostics_gate/"},
}

// RaceSensitivePackagePatterns 返回已登记的生产并发包范围。
func RaceSensitivePackagePatterns() []string {
	patterns := make([]string, len(raceSensitiveSurfaces))
	for index, surface := range raceSensitiveSurfaces {
		patterns[index] = surface.packagePattern
	}
	return patterns
}

// RaceSensitivePathPrefixes 返回并发包登记表对应的文件前缀。
func RaceSensitivePathPrefixes() []string {
	prefixes := make([]string, len(raceSensitiveSurfaces))
	for index, surface := range raceSensitiveSurfaces {
		prefixes[index] = surface.pathPrefix
	}
	return prefixes
}

// ExecutorProgram is the immutable command mapping for one canonical GateID.
type ExecutorProgram struct {
	Strategy               ExecutorStrategy
	Steps                  []ExecutorStep
	RequiredPaths          []string
	RequiredExecutables    []string
	NeedsGoSeed            bool
	NeedsFrontendSeed      bool
	NeedsFrontendEmbedSeed bool
}

var executorPrograms = map[GateID]ExecutorProgram{
	GateIDAIMaintenanceSelfTest: requireExecutables(withGoSeed(requirePaths(commandProgram(
		[]string{"actionlint"},
		[]string{"go", "test", "./scripts/ai_maintenance", "-count=1"},
		[]string{"go", "test", "./scripts", "-run", "^TestAIMaintenanceGate", "-count=1"},
	), ".github/workflows", "scripts/ai_maintenance/main.go", "scripts/ai_maintenance_gates_guard_test.go")), ExecutorActionlintBinaryPath),
	GateIDFrontendLint: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "lint"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendTest: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "test:hook"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendFullTest: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "test"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendBuild: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "build"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendEmbedVerify: withFrontendSeed(requirePaths(commandProgram(
		[]string{"make", "frontend-embed-verify"},
	), "Makefile", "scripts/frontend_embed_verify.sh")),
	GateIDBackendTestWithGuard: withFrontendEmbedSeed(withGoSeed(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps: []ExecutorStep{
			normalGoExecutorStep(append([]string{"./scripts/test_with_guard.sh", "--canonical-backend"}, canonicalBackendPackagePatterns()...)),
		},
	}, "scripts/test_with_guard.sh", "scripts/check_nested_go_modules.sh"))),
	GateIDLSPChangedDiagnostics: requireExecutables(withFrontendSeed(withGoSeed(ExecutorProgram{
		Strategy: ExecutorStrategyChangedDiagnostics,
		RequiredPaths: []string{
			"scripts/lsp_diagnostics_gate/main.go",
		},
	})), ExecutorSqruffBinaryPath),
	GateIDBackendTestGuardWithRace: backendRaceExecutorProgram(),
	GateIDBackendNilness: withGoSeed(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps: []ExecutorStep{
			normalGoExecutorStep([]string{"go", "run", "./scripts/nilness_guard.go", "-test=false", "--", "./..."}),
		},
	}, "scripts/nilness_guard.go")),
	GateIDSQLCVerify: requireExecutables(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategySQLCVerify,
	}, "sqlc.yaml", "cmd/mcp-orch/sqlc.yaml"), ExecutorSQLCBinaryPath),
	GateIDCodemapCheck: withGoSeed(requirePaths(commandProgram(
		[]string{"make", "codemap-check"},
	), "Makefile", "scripts/codemap_index.go", "scripts/archtestmap/main.go")),
	GateIDProjectMapCheck: requirePaths(commandProgram(
		[]string{"make", "project-map-check", "PROJECT_MAP_ARGS="},
	), "Makefile", "scripts/generate_ai_project_map.mjs"),
	GateIDCapabilityContractCheck: withGoSeed(requirePaths(commandProgram(
		[]string{"make", "capcontract-check"},
	), "Makefile", "scripts/capcontract/main.go")),
	GateIDWhitespaceCheck: {
		Strategy:      ExecutorStrategyFullTreeWhitespace,
		RequiredPaths: []string{".git"},
	},
	GateIDReleaseLayeredCheck: {Strategy: ExecutorStrategyReleaseAttestation},
}

// ParseExecutorCommand 只接受 run --gate <canonical GateID> 这一种调用形态。
func ParseExecutorCommand(args []string) (GateID, ExecutorProgram, error) {
	if len(args) != 3 || args[0] != "run" || args[1] != "--gate" {
		return "", ExecutorProgram{}, errors.New("usage: run --gate <canonical GateID>")
	}
	id := GateID(args[2])
	program, ok := executorPrograms[id]
	if !ok {
		return "", ExecutorProgram{}, fmt.Errorf("unknown gate id %q", args[2])
	}
	return id, cloneExecutorProgram(program), nil
}

// ExecutorPrograms 返回覆盖完整 registry 的防御性门禁映射副本。
func ExecutorPrograms() map[GateID]ExecutorProgram {
	programs := make(map[GateID]ExecutorProgram, len(executorPrograms))
	for id, program := range executorPrograms {
		programs[id] = cloneExecutorProgram(program)
	}
	return programs
}

func commandProgram(commands ...[]string) ExecutorProgram {
	return commandProgramIn("", commands...)
}

func commandProgramIn(directory string, commands ...[]string) ExecutorProgram {
	steps := make([]ExecutorStep, len(commands))
	for index, command := range commands {
		steps[index] = ExecutorStep{Directory: directory, Argv: slices.Clone(command)}
	}
	return ExecutorProgram{Strategy: ExecutorStrategyCommands, Steps: steps}
}

func requirePaths(program ExecutorProgram, paths ...string) ExecutorProgram {
	program.RequiredPaths = slices.Clone(paths)
	return program
}

func requireExecutables(program ExecutorProgram, paths ...string) ExecutorProgram {
	program.RequiredExecutables = slices.Clone(paths)
	return program
}

func withGoSeed(program ExecutorProgram) ExecutorProgram {
	program.NeedsGoSeed = true
	return program
}

func withFrontendSeed(program ExecutorProgram) ExecutorProgram {
	program.NeedsFrontendSeed = true
	return program
}

func withFrontendEmbedSeed(program ExecutorProgram) ExecutorProgram {
	program.NeedsFrontendEmbedSeed = true
	return program
}

func backendRaceExecutorProgram() ExecutorProgram {
	packages := RaceSensitivePackagePatterns()
	argv := append([]string{"./scripts/test_with_guard.sh", "--with-race"}, packages...)
	argv = append(argv, "--")
	argv = append(argv, canonicalBackendPackagePatterns()...)
	argv = append(argv, "-count=1", "-timeout=180s")
	return withFrontendEmbedSeed(withGoSeed(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps: []ExecutorStep{
			raceGoExecutorStep(argv),
		},
	}, "scripts/test_with_guard.sh", "scripts/check_nested_go_modules.sh")))
}

func canonicalBackendPackagePatterns() []string {
	return []string{"./cmd/...", "./internal/...", "./pkg/...", "./scripts/..."}
}

func normalGoExecutorStep(argv []string) ExecutorStep {
	return goExecutorStep(argv, []string{
		executorNormalGoFlagsResourceBound,
		executorNormalGoMaxProcsResourceBound,
		executorGoMemoryLimitResourceBound,
	})
}

func raceGoExecutorStep(argv []string) ExecutorStep {
	return goExecutorStep(argv, []string{
		executorRaceGoFlagsResourceBound,
		executorRaceGoMaxProcsResourceBound,
		executorGoMemoryLimitResourceBound,
	})
}

func goExecutorStep(argv []string, environment []string) ExecutorStep {
	return ExecutorStep{
		Argv:        slices.Clone(argv),
		Environment: slices.Clone(environment),
	}
}

// isAllowedExecutorStepEnvironment 将 Go 资源上限限制为完整、不可混配的 profile。
func isAllowedExecutorStepEnvironment(environment []string) bool {
	return slices.Equal(environment, []string{
		executorNormalGoFlagsResourceBound,
		executorNormalGoMaxProcsResourceBound,
		executorGoMemoryLimitResourceBound,
	}) || slices.Equal(environment, []string{
		executorRaceGoFlagsResourceBound,
		executorRaceGoMaxProcsResourceBound,
		executorGoMemoryLimitResourceBound,
	})
}

func cloneExecutorProgram(program ExecutorProgram) ExecutorProgram {
	cloned := program
	cloned.RequiredPaths = slices.Clone(program.RequiredPaths)
	cloned.RequiredExecutables = slices.Clone(program.RequiredExecutables)
	cloned.Steps = make([]ExecutorStep, len(program.Steps))
	for index, step := range program.Steps {
		cloned.Steps[index] = ExecutorStep{
			Directory: step.Directory, Argv: slices.Clone(step.Argv), Environment: slices.Clone(step.Environment),
		}
	}
	return cloned
}
