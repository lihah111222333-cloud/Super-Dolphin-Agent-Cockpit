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
	executorNormalGoFlagsResourceBound        = "GOFLAGS=-p=4"
	executorNormalGoMaxProcsResourceBound     = "GOMAXPROCS=4"
	executorRaceGoFlagsResourceBound          = "GOFLAGS=-p=4"
	executorRaceGoMaxProcsResourceBound       = "GOMAXPROCS=4"
	executorGoMemoryLimitResourceBound        = "GOMEMLIMIT=6GiB"
	executorNilnessGoFlagsResourceBound       = "GOFLAGS=-p=2"
	executorNilnessGoMaxProcsResourceBound    = "GOMAXPROCS=2"
	executorNilnessGoMemoryLimitResourceBound = "GOMEMLIMIT=3GiB"
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
	{packagePattern: "./internal/archtest/...", pathPrefix: "internal/archtest/"},
	{packagePattern: "./internal/contract/...", pathPrefix: "internal/contract/"},
	{packagePattern: "./internal/devtools/alicloud/...", pathPrefix: "internal/devtools/alicloud/"},
	{packagePattern: "./internal/devtools/coordinatoradmission/...", pathPrefix: "internal/devtools/coordinatoradmission/"},
	{packagePattern: "./internal/devtools/acpnode", pathPrefix: "internal/devtools/acpnode/"},
	{packagePattern: "./internal/devtools/gate/...", pathPrefix: "internal/devtools/gate/"},
	{packagePattern: "./internal/devtools/remoteci/...", pathPrefix: "internal/devtools/remoteci/"},
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
	GateIDFrontendPreflight: withGoSeed(withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "test:hook:preflight"},
		[]string{"npm", "run", "test:hook:dependency-integrity"},
	), "frontend-app/package.json", "frontend-app/package-lock.json"))),
	GateIDFrontendTest: withGoSeed(withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "test:hook"},
	), "frontend-app/package.json", "frontend-app/package-lock.json"))),
	GateIDFrontendE2E: withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "run", "test:e2e:business"},
		[]string{"npm", "run", "test:e2e:desktop-wide"},
	), "frontend-app/package.json", "frontend-app/package-lock.json",
		"frontend-app/tests/e2e/business-flows.spec.js", "frontend-app/playwright.business-flows.config.js",
		"frontend-app/tests/e2e/desktop-wide.spec.js", "frontend-app/playwright.desktop-wide.config.js")),
	GateIDFrontendFullTest: withGoSeed(withFrontendSeed(requirePaths(commandProgramIn("frontend-app",
		[]string{"npm", "test"},
	), "frontend-app/package.json", "frontend-app/package-lock.json"))),
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
	GateIDBackendTestGuardWithRace: backendRaceExecutorProgram(),
	GateIDSQLCVerify: requireExecutables(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategySQLCVerify,
	}, "sqlc.yaml", "cmd/mcp-orch/sqlc.yaml", "scripts/sqlc_postprocess.sh"), ExecutorSQLCBinaryPath, ExecutorBashBinaryPath),
	GateIDCodemapCheck: withGoSeed(requirePaths(commandProgram(
		[]string{"make", "codemap-check"},
	), "Makefile", "scripts/codemap_index.go", "scripts/archtestmap/main.go")),
	GateIDProjectMapCheck: requirePaths(commandProgram(
		[]string{ExecutorSelfCommandName, "project-map", "check", "--tree-from-index"},
	), ".git", "scripts/codemap_policy.txt", "docs/doc/codemap/project-map"),
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

// executorProgramForWorkload 将 canonical gate 或受控测试目标映射为固定 argv。
func executorProgramForWorkload(id GateID) (GateID, ExecutorProgram, error) {
	if program, ok := executorPrograms[id]; ok {
		return id, cloneExecutorProgram(program), nil
	}
	parent, targetKind, target, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil {
		return "", ExecutorProgram{}, err
	}
	if !targeted {
		return "", ExecutorProgram{}, unexpandedWorkloadError(id, parent)
	}
	program, err := executorProgramForTarget(parent, WorkloadTargetKind(targetKind), target)
	return parent, program, err
}

// unexpandedWorkloadError 将 expansion-only gate 与未知 workload 的失败原因分开。
func unexpandedWorkloadError(id, parent GateID) error {
	if isExpansionOnlyGate(parent) {
		return fmt.Errorf("gate %q requires expanded workload targets", parent)
	}
	return fmt.Errorf("unknown workload id %q", id)
}

// executorProgramForTarget 按受控 target kind 选择固定执行程序。
func executorProgramForTarget(parent GateID, targetKind WorkloadTargetKind, target string) (ExecutorProgram, error) {
	switch targetKind {
	case workloadTargetGoGuard:
		program, err := goGuardExecutorProgram(parent, target)
		return program, err
	case workloadTargetGoPackage:
		return goPackageExecutorProgram(parent, target), nil
	case workloadTargetGoTest:
		program, err := goTestExecutorProgram(parent, target)
		return program, err
	case workloadTargetGoBenchmark:
		program, err := goBenchmarkExecutorProgram(target)
		return program, err
	case workloadTargetVitest:
		return vitestExecutorProgram(target), nil
	case workloadTargetPlaywright:
		program, err := playwrightE2EExecutorProgram(target)
		return program, err
	default:
		return ExecutorProgram{}, fmt.Errorf("unsupported workload target kind %q", targetKind)
	}
}

// goGuardExecutorProgram 将历史 canonical 或新的原子守卫映射为固定 argv。
func goGuardExecutorProgram(parent GateID, target string) (ExecutorProgram, error) {
	if parent == GateIDAIMaintenanceSelfTest {
		return aiMaintenanceGuardExecutorProgram(target)
	}
	if program, ok := splitSourceGoGuardExecutorProgram(target); ok {
		return program, nil
	}
	var argv []string
	needsFrontendEmbed := false
	switch target {
	case GoGuardTargetCanonical:
		argv = []string{"./scripts/test_with_guard.sh", "--ci-guard"}
		needsFrontendEmbed = true
		if parent == GateIDBackendTestGuardWithRace {
			argv = []string{"./scripts/test_with_guard.sh", "--ci-race-guard"}
		}
	case GoGuardTargetSource:
		argv = []string{"./scripts/test_with_guard.sh", "--ci-guard-source"}
		needsFrontendEmbed = true
	case GoGuardTargetCopylocksProvider:
		argv = []string{"./scripts/test_with_guard.sh", "--ci-copylocks", "provider"}
	case GoGuardTargetCopylocksPlatform:
		argv = []string{"./scripts/test_with_guard.sh", "--ci-copylocks", "platform"}
	case GoGuardTargetCopylocksThread:
		argv = []string{"./scripts/test_with_guard.sh", "--ci-copylocks", "thread"}
	default:
		module, err := ParseNestedGoModuleGuardTarget(target)
		if err != nil {
			return ExecutorProgram{}, fmt.Errorf("unsupported Go guard target %q", target)
		}
		argv = []string{"./scripts/test_with_guard.sh", "--ci-nested-module", module}
	}
	program := goTargetExecutorProgram(argv, false)
	program.NeedsFrontendEmbedSeed = needsFrontendEmbed
	return program, nil
}

// splitSourceGoGuardExecutorProgram 将源码策略和规模检查映射成互不阻塞的原子命令。
func splitSourceGoGuardExecutorProgram(target string) (ExecutorProgram, bool) {
	var argv []string
	var requiredPath string
	switch target {
	case GoGuardTargetSourceRawGoTest:
		argv = []string{"./scripts/forbid_raw_go_test.sh"}
		requiredPath = "scripts/forbid_raw_go_test.sh"
	default:
		return ExecutorProgram{}, false
	}
	program := goTargetExecutorProgram(argv, false)
	program.NeedsFrontendEmbedSeed = false
	program.RequiredPaths = append(program.RequiredPaths, requiredPath)
	return program, true
}

// aiMaintenanceGuardExecutorProgram 将维护工具单测与入口契约测试拆成独立命令。
func aiMaintenanceGuardExecutorProgram(target string) (ExecutorProgram, error) {
	var argv []string
	switch target {
	case GoGuardTargetAIMaintenanceUnit:
		argv = []string{"go", "test", "./scripts/ai_maintenance", "-count=1"}
	case GoGuardTargetAIMaintenanceGate:
		argv = []string{"go", "test", "./scripts", "-run", "^TestAIMaintenanceGate", "-count=1"}
	default:
		return ExecutorProgram{}, fmt.Errorf("unsupported AI maintenance target %q", target)
	}
	return requireExecutables(withGoSeed(requirePaths(commandProgram(argv),
		".github/workflows", "scripts/ai_maintenance/main.go", "scripts/ai_maintenance_gates_guard_test.go",
	)), ExecutorActionlintBinaryPath), nil
}

// goPackageExecutorProgram 为一个 Go 包选择普通或 race 执行语义。
func goPackageExecutorProgram(parent GateID, target string) ExecutorProgram {
	if parent == GateIDBackendNilness {
		return nilnessExecutorProgram(target)
	}
	normal := []string{"./scripts/test_with_guard.sh", "--ci-package", target}
	race := []string{"./scripts/test_with_guard.sh", "--ci-race-package", target}
	return goTargetExecutorProgramForParent(parent, normal, race)
}

// nilnessExecutorProgram 为一个精确 Go 包建立独立 nilness analyzer 调用。
func nilnessExecutorProgram(target string) ExecutorProgram {
	return withGoSeed(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps: []ExecutorStep{
			nilnessGoExecutorStep([]string{"go", "run", "./scripts/nilness_guard.go", "-test=false", "--", target}),
		},
	}, "scripts/nilness_guard.go"))
}

// goTestExecutorProgram 解析顶层测试并选择普通或 race 执行语义。
func goTestExecutorProgram(parent GateID, target string) (ExecutorProgram, error) {
	testTarget, err := ParseGoTestTarget(target)
	if err != nil {
		return ExecutorProgram{}, err
	}
	normal := []string{
		"./scripts/test_with_guard.sh",
		"--ci-package-test",
		testTarget.Package,
		testTarget.Name,
	}
	race := []string{
		"./scripts/test_with_guard.sh",
		"--ci-race-package-test",
		testTarget.Package,
		testTarget.Name,
	}
	return goTargetExecutorProgramForParent(parent, normal, race), nil
}

// goBenchmarkExecutorProgram 解析 benchmark 并构造仅远程执行的命令。
func goBenchmarkExecutorProgram(target string) (ExecutorProgram, error) {
	benchmarkTarget, err := ParseGoBenchmarkTarget(target)
	if err != nil {
		return ExecutorProgram{}, err
	}
	argv := []string{
		"./scripts/test_with_guard.sh",
		"--ci-package-benchmark",
		benchmarkTarget.Package,
		benchmarkTarget.Name,
	}
	return goTargetExecutorProgram(argv, false), nil
}

// vitestExecutorProgram 将一个前端测试文件绑定到串行 Vitest 命令。
func vitestExecutorProgram(target string) ExecutorProgram {
	return withGoSeed(withFrontendSeed(requirePaths(commandProgramIn(
		"frontend-app",
		[]string{"npx", "vitest", "run", target, "--no-file-parallelism", "--maxWorkers=1"},
	), "frontend-app/package.json", "frontend-app/package-lock.json", "frontend-app/"+target)))
}

const (
	playwrightBusinessReadSurfacesTarget = "tests/e2e/business-flows.spec.js#business-read-surfaces"
	playwrightBusinessChatBridgeTarget   = "tests/e2e/business-flows.spec.js#business-chat-bridge"
	playwrightDesktopShellTarget         = "tests/e2e/desktop-wide.spec.js#desktop-shell"
	playwrightDesktopBusinessPagesTarget = "tests/e2e/desktop-wide.spec.js#desktop-business-pages"
	playwrightDesktopReadSettingsTarget  = "tests/e2e/desktop-wide.spec.js#desktop-read-settings"
)

// playwrightE2EExecutorProgram 为每个 Playwright describe 建立独立远程 workload。
func playwrightE2EExecutorProgram(spec string) (ExecutorProgram, error) {
	specPath, grep, err := ParsePlaywrightE2ETarget(spec)
	if err != nil {
		return ExecutorProgram{}, err
	}
	var script, config string
	switch specPath {
	case "tests/e2e/business-flows.spec.js":
		script, config = "test:e2e:business", "playwright.business-flows.config.js"
	case "tests/e2e/desktop-wide.spec.js":
		script, config = "test:e2e:desktop-wide", "playwright.desktop-wide.config.js"
	default:
		return ExecutorProgram{}, fmt.Errorf("unsupported Playwright E2E spec %q", specPath)
	}
	return withFrontendSeed(requirePaths(commandProgramIn("frontend-app", []string{"npm", "run", script, "--", "--grep", grep}),
		"frontend-app/package.json", "frontend-app/package-lock.json", "frontend-app/"+specPath, "frontend-app/"+config)), nil
}

// goTargetExecutorProgramForParent 根据父 gate 选择普通或 race argv。
func goTargetExecutorProgramForParent(parent GateID, normal []string, race []string) ExecutorProgram {
	if parent == GateIDBackendTestGuardWithRace {
		return goTargetExecutorProgram(race, true)
	}
	return goTargetExecutorProgram(normal, false)
}

// goTargetExecutorProgram 为 Go 目标添加固定 seed、脚本和资源约束。
func goTargetExecutorProgram(argv []string, race bool) ExecutorProgram {
	step := normalGoExecutorStep(argv)
	if race {
		step = raceGoExecutorStep(argv)
	}
	return withFrontendEmbedSeed(withGoSeed(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps:    []ExecutorStep{step},
	}, "scripts/test_with_guard.sh", "scripts/check_nested_go_modules.sh")))
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
	return []string{"./..."}
}

func normalGoExecutorStep(argv []string) ExecutorStep {
	return goExecutorStep(argv, []string{
		executorNormalGoFlagsResourceBound,
		executorNormalGoMaxProcsResourceBound,
		executorGoMemoryLimitResourceBound,
	})
}

func nilnessGoExecutorStep(argv []string) ExecutorStep {
	return goExecutorStep(argv, []string{
		executorNilnessGoFlagsResourceBound,
		executorNilnessGoMaxProcsResourceBound,
		executorNilnessGoMemoryLimitResourceBound,
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
	}) || slices.Equal(environment, []string{
		executorNilnessGoFlagsResourceBound,
		executorNilnessGoMaxProcsResourceBound,
		executorNilnessGoMemoryLimitResourceBound,
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
