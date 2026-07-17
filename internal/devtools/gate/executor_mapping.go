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
)

// ExecutorStep is one shell-free command in a gate program.
type ExecutorStep struct {
	Directory string
	Argv      []string
}

// ExecutorProgram is the immutable command mapping for one canonical GateID.
type ExecutorProgram struct {
	Strategy            ExecutorStrategy
	Steps               []ExecutorStep
	RequiredPaths       []string
	RequiredExecutables []string
	NeedsGoSeed         bool
	NeedsFrontendSeed   bool
}

var executorPrograms = map[GateID]ExecutorProgram{
	GateIDAIMaintenanceSelfTest: withGoSeed(requirePaths(commandProgram(
		[]string{"go", "test", "./scripts/ai_maintenance", "-count=1"},
		[]string{"go", "test", "./scripts", "-run", "^TestAIMaintenanceGate$", "-count=1"},
	), "scripts/ai_maintenance/main.go", "scripts/ai_maintenance_gates_guard_test.go")),
	GateIDFrontendLint: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "lint"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendTest: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "test"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendBuild: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "build"},
	), "frontend-app/package.json", "frontend-app/package-lock.json")),
	GateIDFrontendEmbedVerify: withFrontendSeed(requirePaths(commandProgram(
		[]string{"make", "frontend-embed-verify"},
	), "Makefile", "scripts/frontend_embed_verify.sh")),
	GateIDBackendTestWithGuard: withGoSeed(requirePaths(commandProgram(
		[]string{"./scripts/test_with_guard.sh", "./cmd/...", "./internal/...", "./pkg/...", "./scripts/...", "-count=1", "-timeout=180s"},
	), "scripts/test_with_guard.sh")),
	GateIDLSPChangedDiagnostics: withFrontendSeed(withGoSeed(ExecutorProgram{
		Strategy: ExecutorStrategyChangedDiagnostics,
		RequiredPaths: []string{
			"scripts/lsp_diagnostics_gate/main.go",
		},
	})),
	GateIDBackendTestGuardWithRace: withGoSeed(requirePaths(commandProgram(
		[]string{"./scripts/test_with_guard.sh", "--with-race", "./cmd/...", "./internal/...", "./pkg/...", "./scripts/...", "--", "./cmd/...", "./internal/...", "./pkg/...", "./scripts/...", "-count=1", "-timeout=180s"},
	), "scripts/test_with_guard.sh")),
	GateIDBackendNilness: withGoSeed(requirePaths(commandProgram(
		[]string{"go", "run", "./scripts/nilness_guard.go", "-test=false", "--", "./..."},
	), "scripts/nilness_guard.go")),
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
	GateIDReleaseLayeredCheck: withGoSeed(requirePaths(commandProgram(
		[]string{"make", "ci-l3-release"},
	), "Makefile", "scripts/test_with_guard.sh")),
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

func cloneExecutorProgram(program ExecutorProgram) ExecutorProgram {
	cloned := program
	cloned.RequiredPaths = slices.Clone(program.RequiredPaths)
	cloned.RequiredExecutables = slices.Clone(program.RequiredExecutables)
	cloned.Steps = make([]ExecutorStep, len(program.Steps))
	for index, step := range program.Steps {
		cloned.Steps[index] = ExecutorStep{Directory: step.Directory, Argv: slices.Clone(step.Argv)}
	}
	return cloned
}
