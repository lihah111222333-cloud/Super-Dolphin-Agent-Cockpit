package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

type gatePlanPolicy struct {
	aiMaintenanceFiles              map[string]bool
	codemapExactFiles               map[string]bool
	turnContractInfrastructureFiles map[string]bool
	frontendStaticGuardFiles        map[string]bool
	coreBackendGatePackages         []string
	mcpLSPWorkloadExactFiles        map[string]bool
	mcpLSPWorkloadPrefixes          []string
	archtestNonGoInputFiles         map[string]bool
	archtestNonGoInputPrefixes      []string
}

// newGatePlanPolicy 构造门禁计划使用的路径策略和核心包清单。
func newGatePlanPolicy() gatePlanPolicy {
	return gatePlanPolicy{
		aiMaintenanceFiles: map[string]bool{
			"Makefile":                                   true,
			"scripts/ai_maintenance_gates.sh":            true,
			"scripts/ai_maintenance_gates_guard_test.go": true,
			"scripts/check_mcp_lsp_workload_catalog.sh":  true,
			"scripts/mcp_lsp_workload_catalog.json":      true,
			"scripts/run_mcp_lsp_workload.sh":            true,
			"scripts/configure_hook_node_runtime.sh":     true,
			"scripts/frontend_embed_verify.sh":           true,
			"scripts/refresh_generated_artifacts.sh":     true,
			"scripts/sqlc_verify_worktree.sh":            true,
			"scripts/test_with_guard.ps1":                true,
			"scripts/test_with_guard.sh":                 true,
		},
		codemapExactFiles: map[string]bool{
			".ai-project-map.overrides.json":      true,
			"README.md":                           true,
			"scripts/codemap_index.go":            true,
			"scripts/codemap_policy.txt":          true,
			"scripts/generate_ai_project_map.mjs": true,
		},
		turnContractInfrastructureFiles: map[string]bool{
			"scripts/turncontract/main.go":                                 true,
			"frontend-app/package.json":                                    true,
			"frontend-app/scripts/turn-contract-field-guard.mjs":           true,
			"frontend-app/scripts/turn-contract-field-guard.test.mjs":      true,
			"frontend-app/src/shared/contracts/turnContracts.generated.js": true,
		},
		frontendStaticGuardFiles: map[string]bool{
			"frontend-app/package.json":                                         true,
			"frontend-app/scripts/frontend-state-ownership-registry.json":       true,
			"frontend-app/scripts/frontend-state-ownership-guard.mjs":           true,
			"frontend-app/scripts/frontend-state-ownership-guard.test.mjs":      true,
			"frontend-app/scripts/frontend-dependency-direction-registry.json":  true,
			"frontend-app/scripts/frontend-dependency-direction-guard.mjs":      true,
			"frontend-app/scripts/frontend-dependency-direction-guard.test.mjs": true,
		},
		coreBackendGatePackages: []string{
			"./cmd/mcp-lsp", "./cmd/mcp-orch", "./internal/app", "./internal/module/thread",
			"./internal/platform/config", "./internal/platform/toolbridge", "./internal/provider/contracttest",
			"./internal/provider/unified", "./internal/provider/codexapp", "./internal/provider/claudecli",
			"./internal/provider", "./scripts", "./scripts/ai_maintenance",
		},
		mcpLSPWorkloadExactFiles: map[string]bool{
			"Makefile": true, ".github/workflows/ci.yml": true, ".github/workflows/release.yml": true,
			"scripts/check_mcp_lsp_workload_catalog.sh": true, "scripts/mcp_lsp_workload_catalog.json": true,
			"scripts/run_mcp_lsp_workload.sh": true, "scripts/ai_maintenance/main.go": true,
			"scripts/ai_maintenance/owned_gate_execution.go": true,
			".githooks/README.md":                            true,
		},
		mcpLSPWorkloadPrefixes: []string{"cmd/mcp-lsp/", "scripts/mcp_lsp_workload_"},
		archtestNonGoInputFiles: map[string]bool{
			"cmd/mcp-orch/sqlc.yaml": true, "docs/契约/modularity-convention.md": true,
			"frontend-app/src/App.jsx": true, "frontend-app/src/app/appShellModel.js": true,
			"frontend-app/src/features/slash-commands/adapters/skillInfoFieldRegistry.json": true,
			"go.mod": true, "internal/archtest/freeze_baseline.json": true, "scripts/ci_cross_platform_smoke.ps1": true,
			"scripts/ai_maintenance_gates.sh": true, "scripts/codemap_policy.txt": true,
			"scripts/test_with_guard.ps1": true, "scripts/test_with_guard.sh": true, "sqlc.yaml": true,
		},
		archtestNonGoInputPrefixes: []string{
			".githooks/", "cmd/mcp-orch/sql/queries/", "docs/guards/", "internal/guards/",
			"internal/platform/db/sqlite/migrations/", "internal/platform/shared/builtinprompts/assets/",
			"internal/provider/_template/", "migrations/", "sql/queries/",
		},
	}
}

type gatePlan struct {
	ReviewObject       reviewObject `json:"review_object"`
	ChangedFiles       []string     `json:"changed_files"`
	RequiredGates      []string     `json:"required_gates"`
	GeneratedFiles     []string     `json:"generated_files"`
	AffectedGoPackages []string     `json:"affected_go_packages,omitempty"`
	DiagnosticFiles    []string     `json:"diagnostic_files,omitempty"`
	executionRoot      string
}

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ai_maintenance <plan|run> [flags]")
	}
	switch args[0] {
	case "plan":
		return runPlan(args[1:], os.Stdout)
	case "run":
		return runGates(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// runPlan 解析变更路径并输出只读 gate plan JSON。
func runPlan(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	reviewFlags := registerReviewFlags(fs)
	if stdout == nil {
		stdout = os.Stdout
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	plan, err := buildGatePlanFromReviewFlags(reviewFlags)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

// runGates 是统一入口的执行模式：先冻结 exact review object，再只执行命中的命令面。
func runGates(args []string) error {
	return runGatesWithMaterializer(args, materializeReviewObject, gateRunners)
}

// runGatesWithMaterializer 在同一 prepared session 内完成计划和执行，避免重新物化 exact candidate。
func runGatesWithMaterializer(args []string, materializer reviewObjectMaterializer, factory gateRunnerFactory) (resultErr error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	reviewFlags := registerReviewFlags(fs)
	printPlan := fs.Bool("print-plan", false, "print gate plan and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(reviewFlags.changed) > 0 {
		return runPathsOnlyGatePlan(reviewFlags, *printPlan)
	}
	return runExecutableGatePlan(reviewFlags, *printPlan, materializer, factory)
}

// runPathsOnlyGatePlan 保持 paths-only 展示输入不可进入执行器的边界。
func runPathsOnlyGatePlan(reviewFlags *reviewFlags, printPlan bool) error {
	if !printPlan {
		return errors.New("paths-only review object is not executable")
	}
	if *reviewFlags.staged || *reviewFlags.worktree || *reviewFlags.base != "" || *reviewFlags.head != "" {
		return errors.New("--changed-file paths-only mode cannot be combined with executable review object flags")
	}
	plan, err := buildGatePlan([]string(reviewFlags.changed))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

// runExecutableGatePlan 为 executable review object 创建一次 session 并执行或输出计划。
func runExecutableGatePlan(reviewFlags *reviewFlags, printPlan bool, materializer reviewObjectMaterializer, factory gateRunnerFactory) (resultErr error) {
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return fmt.Errorf("resolve review repository root: %w", err)
	}
	object, err := resolveReviewObject(repoRoot, reviewObjectOptions{
		Base: *reviewFlags.base, Head: *reviewFlags.head, Staged: *reviewFlags.staged, Worktree: *reviewFlags.worktree,
	})
	if err != nil {
		return err
	}
	session, err := prepareGatePlanForReviewObject(repoRoot, object, newGatePlanPolicy(), materializer)
	if err != nil {
		return err
	}
	if printPlan {
		defer func() { resultErr = errors.Join(resultErr, session.close()) }()
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(session.plan)
	}
	return executePreparedGatePlanWithRunnerFactory(session, factory)
}

type reviewFlags struct {
	base     *string
	head     *string
	staged   *bool
	worktree *bool
	changed  multiFlag
}

func registerReviewFlags(fs *flag.FlagSet) *reviewFlags {
	flags := &reviewFlags{
		base:     fs.String("base", "", "range base revision used to resolve the merge-base; defaults to HEAD~1"),
		head:     fs.String("head", "", "range candidate revision; defaults to HEAD"),
		staged:   fs.Bool("staged", false, "review the exact staged tree against HEAD"),
		worktree: fs.Bool("worktree", false, "capture staged, unstaged, deleted, and untracked files once"),
	}
	fs.Var(&flags.changed, "changed-file", "paths-only plan input; may be repeated and cannot execute")
	return flags
}

// buildGatePlanFromReviewFlags 将互斥 flag 解析为 paths-only 或 executable exact object 计划。
func buildGatePlanFromReviewFlags(flags *reviewFlags) (gatePlan, error) {
	return buildGatePlanFromReviewFlagsWithMaterializer(flags, materializeReviewObject)
}

// buildGatePlanFromReviewFlagsWithMaterializer 仅为 plan 输出准备并立即收口 session。
func buildGatePlanFromReviewFlagsWithMaterializer(flags *reviewFlags, materializer reviewObjectMaterializer) (plan gatePlan, resultErr error) {
	if len(flags.changed) > 0 {
		if *flags.staged || *flags.worktree || *flags.base != "" || *flags.head != "" {
			return gatePlan{}, errors.New("--changed-file paths-only mode cannot be combined with executable review object flags")
		}
		return buildGatePlan([]string(flags.changed))
	}
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return gatePlan{}, fmt.Errorf("resolve review repository root: %w", err)
	}
	object, err := resolveReviewObject(repoRoot, reviewObjectOptions{
		Base: *flags.base, Head: *flags.head, Staged: *flags.staged, Worktree: *flags.worktree,
	})
	if err != nil {
		return gatePlan{}, err
	}
	session, err := prepareGatePlanForReviewObject(repoRoot, object, newGatePlanPolicy(), materializer)
	if err != nil {
		return gatePlan{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, session.close()) }()
	return session.plan, nil
}

// buildGatePlan 把 paths-only 演示输入映射成必须执行的命令。
// 路由规则保持路径前缀级别，避免把任务语义藏进不可审计的动态推断。
func buildGatePlan(files []string) (gatePlan, error) {
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return gatePlan{}, fmt.Errorf("resolve repository root for capability path rules: %w", err)
	}
	return buildGatePlanForRepo(repoRoot, files, newGatePlanPolicy())
}

// buildGatePlanForReviewObject 从 exact tree pair 派生 changed-files，禁止调用方另行注入路径。
func buildGatePlanForReviewObject(repoRoot string, object reviewObject, policy gatePlanPolicy) (plan gatePlan, resultErr error) {
	session, err := prepareGatePlanForReviewObject(repoRoot, object, policy, materializeReviewObject)
	if err != nil {
		return gatePlan{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, session.close()) }()
	return session.plan, nil
}

// buildGatePlanForRepo 使用 generator AST 派生的 capability roots 构造计划；解析失败时拒绝产出不完整计划。
func buildGatePlanForRepo(repoRoot string, files []string, policy gatePlanPolicy) (gatePlan, error) {
	return buildGatePlanForRepoAndObject(repoRoot, files, pathsOnlyReviewObject(), policy)
}

// buildGatePlanForRepoAndObject 只从指定 candidate root 的规则和源码构造完整门禁计划。
func buildGatePlanForRepoAndObject(repoRoot string, files []string, object reviewObject, policy gatePlanPolicy) (gatePlan, error) {
	capabilityRules, turnContractPaths, err := loadGatePlanRules(repoRoot, policy)
	if err != nil {
		return gatePlan{}, err
	}
	normalized := normalizeFiles(files)
	criticalRouteContext, err := newCriticalTypecheckPlanContext(repoRoot, normalized, loadCriticalTypecheckProductionFiles)
	if err != nil {
		return gatePlan{}, fmt.Errorf("prepare critical typecheck route context: %w", err)
	}
	plan := gatePlan{ReviewObject: object, ChangedFiles: normalized}
	gates := map[string]bool{"diff:whitespace": true}
	generated := map[string]bool{}
	backendChanged, err := applyChangedFileGateRules(
		normalized,
		capabilityRules,
		turnContractPaths,
		gates,
		generated,
		policy,
		criticalRouteContext,
	)
	if err != nil {
		return gatePlan{}, err
	}
	applyBackendGateRules(normalized, gates, backendChanged)
	plan.DiagnosticFiles = changedDiagnosticFiles(normalized)
	if len(plan.DiagnosticFiles) > 0 {
		gates["lsp:changed-diagnostics"] = true
	}
	plan.RequiredGates = orderedGates(gates)
	plan.GeneratedFiles = sortedKeys(generated)
	if backendChanged {
		plan.AffectedGoPackages, err = affectedGoPackages(repoRoot, normalized, policy)
		if err != nil {
			return gatePlan{}, err
		}
	}
	return plan, nil
}

// loadGatePlanRules 读取能力契约和 turn contract 路由所需的权威规则。
func loadGatePlanRules(repoRoot string, policy gatePlanPolicy) (capcontract.PathRules, map[string]bool, error) {
	capabilityRules, err := capcontract.LoadPathRules(repoRoot)
	if err != nil {
		return capcontract.PathRules{}, nil, fmt.Errorf("load capability-contract path rules: %w", err)
	}
	turnContractPaths, err := loadTurnContractPaths(repoRoot, policy)
	if err != nil {
		return capcontract.PathRules{}, nil, err
	}
	return capabilityRules, turnContractPaths, nil
}

// applyChangedFileGateRules 汇总所有变更路径的门禁规则，并报告是否需要后端验证面。
func applyChangedFileGateRules(
	files []string,
	capabilityRules capcontract.PathRules,
	turnContractPaths, gates, generated map[string]bool,
	policy gatePlanPolicy,
	criticalTypecheckContext criticalTypecheckPlanContext,
) (bool, error) {
	backendChanged := false
	for _, file := range files {
		backendFile, err := applyFileGateRules(file, capabilityRules, turnContractPaths, gates, generated, policy, criticalTypecheckContext)
		if err != nil {
			return false, err
		}
		if backendFile {
			backendChanged = true
		}
	}
	return backendChanged, nil
}

// applyBackendGateRules 为后端变更收敛唯一全工作区验证 owner，避免重复排队 archtest。
func applyBackendGateRules(files []string, gates map[string]bool, backendChanged bool) {
	if !backendChanged {
		return
	}
	gates["backend:test_with_guard"] = true
	delete(gates, "ai-maintenance:self-test")
	if requiresBroadBackendRegression(files) {
		// ci-l1 是后端基础设施变更的唯一全工作区 owner；另排 archtest 会重复执行同一守卫。
		delete(gates, "backend:archtest")
	}
}

// applyFileGateRules 汇总单个路径触发的命令 gate，并返回它是否属于 Go/后端验证面。
func applyFileGateRules(file string, capabilityRules capcontract.PathRules, turnContractPaths, gates, generated map[string]bool, policy gatePlanPolicy, criticalTypecheckContext criticalTypecheckPlanContext) (bool, error) {
	backendChanged, err := applySourceGateRules(file, gates, criticalTypecheckContext)
	if err != nil {
		return false, err
	}
	if err := applyCapabilityContractGateRules(file, capabilityRules, gates, generated); err != nil {
		return false, err
	}
	policy.applyOwnedGateRules(file, gates)
	if applyGateInfrastructureRules(file, gates) {
		backendChanged = true
	}
	if goModuleFile(file) {
		backendChanged = true
	}
	if sqlcRelevant(file) {
		gates["sqlc:verify"] = true
	}
	if turnContractRelevant(file, turnContractPaths) {
		gates["turncontract:verify"] = true
	}
	if policy.frontendStaticGuardRelevant(file) {
		gates["frontend:static-guards"] = true
	}
	if policy.codemapRelevant(file) {
		gates["codemap:check"] = true
	}
	if projectMapRelevant(file) {
		gates["project-map:check"] = true
	}
	applyGeneratedFileRoute(file, generated)
	return backendChanged, nil
}

func applyGeneratedFileRoute(file string, generated map[string]bool) {
	if !generatedCodemapFile(file) {
		return
	}
	generated[file] = true
}

// applyCapabilityContractGateRules 统一能力清单生成输入与检查路由，并在路径规则解析失败时阻断。
func applyCapabilityContractGateRules(file string, capabilityRules capcontract.PathRules, gates, generated map[string]bool) error {
	capabilityChanged, err := capabilityRules.Match(file)
	if err != nil {
		return fmt.Errorf("match capability-contract path %q: %w", file, err)
	}
	if capabilityChanged {
		gates["capcontract:check"] = true
	}
	if capabilityContractProducerInput(file) {
		generated[capabilityContractManifest] = true
	}
	return nil
}

func goModuleFile(file string) bool {
	switch file {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	default:
		return false
	}
}

// sqlcRelevant 覆盖两个生成入口、其配置、查询、迁移以及共享 store 消费面。
func sqlcRelevant(file string) bool {
	return file == "sqlc.yaml" ||
		file == "cmd/mcp-orch/sqlc.yaml" ||
		goModuleFile(file) ||
		strings.HasPrefix(file, "sql/") ||
		strings.HasPrefix(file, "cmd/mcp-orch/sql/") ||
		strings.HasPrefix(file, "internal/platform/db/sqlite/migrations/") ||
		strings.HasPrefix(file, "internal/store/")
}

// turnContractRelevant 覆盖 canonical schema、基础设施以及 registry 派生的完整生产消费链。
func turnContractRelevant(file string, turnContractPaths map[string]bool) bool {
	if strings.HasSuffix(file, "_test.go") {
		return false
	}
	return strings.HasPrefix(file, "internal/dto/turn/") || turnContractPaths[file] || turnContractProductionGo(file)
}

// frontendStaticGuardRelevant 覆盖 guard 自身与全部生产前端输入，防止新增 writer 或反向依赖绕过定向门禁。
func (policy gatePlanPolicy) frontendStaticGuardRelevant(file string) bool {
	return frontendStaticGuardInputRelevant(file) || policy.frontendStaticGuardFiles[file]
}

func turnContractProductionGo(file string) bool {
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return false
	}
	for _, prefix := range []string{"cmd/", "internal/", "pkg/", "scripts/"} {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

// aiMaintenanceRelevant 识别会改变本 gate 自身行为的文件，触发自测避免 workflow/script 空绿。
func (policy gatePlanPolicy) aiMaintenanceRelevant(file string) bool {
	return policy.aiMaintenanceFiles[file] ||
		(strings.HasPrefix(file, ".githooks/") && !strings.HasSuffix(file, ".md")) ||
		strings.HasPrefix(file, "scripts/ai_maintenance/")
}

// applyGateInfrastructureRules 将门禁基础设施变更路由到其真实下游验证面。
func applyGateInfrastructureRules(file string, gates map[string]bool) bool {
	switch file {
	case "Makefile":
		gates["sqlc:verify"] = true
		gates["frontend:embed-verify"] = true
		gates["codemap:check"] = true
		gates["project-map:check"] = true
		return true
	case "scripts/test_with_guard.sh", "scripts/test_with_guard.ps1":
		return true
	case "scripts/sqlc_verify_worktree.sh":
		gates["sqlc:verify"] = true
	case "scripts/frontend_embed_verify.sh":
		gates["frontend:embed-verify"] = true
	case "scripts/refresh_generated_artifacts.sh":
		gates["codemap:check"] = true
		gates["project-map:check"] = true
	}
	return false
}

// applySourceGateRules 根据生产源码路径补充必须执行的门禁。
func applySourceGateRules(file string, gates map[string]bool, criticalTypecheckContext criticalTypecheckPlanContext) (bool, error) {
	if strings.HasPrefix(file, "frontend-app/") {
		return applyFrontendSourceGateRules(file, gates, criticalTypecheckContext)
	}
	return applyBackendSourceGateRules(file)
}

// applyFrontendSourceGateRules 为前端路径应用严格类型检查、lint、变更测试和 diagnostics gate。
func applyFrontendSourceGateRules(file string, gates map[string]bool, criticalTypecheckContext criticalTypecheckPlanContext) (bool, error) {
	critical, err := criticalTypecheckContext.relevant(file)
	if err != nil {
		return false, fmt.Errorf("route critical typecheck path %q: %w", file, err)
	}
	if critical {
		gates["frontend:typecheck-contracts"] = true
	}
	if frontendLintRelevant(file) {
		gates["frontend:lint"] = true
	}
	if frontendChangedTestRelevant(file) {
		gates["frontend:changed-tests"] = true
	}
	return false, nil
}

// applyBackendSourceGateRules 标记 Go 生产源码的后端验证面。
func applyBackendSourceGateRules(file string) (bool, error) {
	if strings.HasSuffix(file, ".go") && (strings.HasPrefix(file, "cmd/") || strings.HasPrefix(file, "internal/") || strings.HasPrefix(file, "pkg/")) {
		return true, nil
	}
	if strings.HasPrefix(file, "scripts/") && strings.HasSuffix(file, ".go") {
		return true, nil
	}
	return false, nil
}

func runCommand(dir, name string, args ...string) error {
	fmt.Printf("\n==> %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Env = gateCommandEnvironment(name)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func normalizeFiles(files []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, file := range files {
		// Git -z records path bytes exactly; trimming would silently change a
		// legitimate Unicode/space-bearing filename before gate routing.
		file = filepath.ToSlash(file)
		file = strings.TrimPrefix(file, "./")
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, file)
	}
	sort.Strings(out)
	return out
}

// codemapRelevant 判断变更是否可能影响手写代码地图或其符号索引。
func (policy gatePlanPolicy) codemapRelevant(file string) bool {
	if policy.codemapExactFiles[file] {
		return true
	}
	for _, prefix := range []string{"cmd/", "internal/", "pkg/", "frontend-app/src/", "docs/doc/codemap/", "scripts/archtestmap/"} {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

// projectMapRelevant mirrors the generator's indexed roots so path or size drift is never skipped.
func projectMapRelevant(file string) bool {
	for _, prefix := range []string{
		".githooks/", "cmd/", "config/", "docs/", "frontend-app/", "internal/", "migrations/", "pkg/",
		"scripts/", "sql/", "test/", "tests/", "third_party/",
	} {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	switch file {
	case "AGENTS.md", "CLAUDE.md", "Makefile", "README.de.md", "README.es.md", "README.ja.md",
		"README.ko.md", "README.md", "README.zh-CN.md", "go.mod", "package-lock.json", "package.json",
		"run-new-ui-desktop.ps1", "run-new-ui-desktop.sh", ".ai-project-map.overrides.json":
		return true
	default:
		return false
	}
}

// generatedCodemapFile 判断路径是否属于代码地图生成产物。
func generatedCodemapFile(file string) bool {
	return file == "README.md" ||
		file == "docs/doc/codemap/13-archtest-boundaries.md" ||
		file == "docs/doc/codemap/ai-index.json" ||
		file == "docs/doc/codemap/anchor-identities.json" ||
		file == "docs/doc/codemap/README.md" ||
		file == "docs/doc/codemap/capability-contract/capability_manifest.json" ||
		strings.HasPrefix(file, "docs/doc/codemap/project-map/")
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// orderedGates 按稳定顺序输出已启用门禁，并把未知门禁保留在末尾。
func orderedGates(values map[string]bool) []string {
	order := []string{
		"ai-maintenance:self-test",
		"turncontract:verify",
		"frontend:static-guards",
		"frontend:lint",
		"frontend:typecheck-contracts",
		"frontend:changed-tests",
		"frontend:embed-verify",
		"workflow:actionlint",
		"release:semantic-guards",
		"mcp-lsp:catalog",
		"mcp-lsp:idle-quick",
		"backend:test_with_guard",
		"backend:test-integrity",
		"lsp:changed-diagnostics",
		"backend:archtest",
		"sqlc:verify",
		"codemap:check",
		"project-map:check",
		"capcontract:check",
		"diff:whitespace",
	}
	var out []string
	for _, gate := range order {
		if values[gate] {
			out = append(out, gate)
			delete(values, gate)
		}
	}
	out = append(out, sortedKeys(values)...)
	return out
}

func sameStringSet(a, b []string) bool {
	aa := normalizeFiles(a)
	bb := normalizeFiles(b)
	if len(aa) != len(bb) {
		return false
	}
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func lines(text string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// nulSeparatedPaths 解析 Git 的 -z 路径流；路径可包含换行、空格和非 ASCII 字符。
func nulSeparatedPaths(raw []byte) []string {
	parts := bytes.Split(raw, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		paths = append(paths, string(part))
	}
	return paths
}

type multiFlag []string

// String 实现 flag.Value，便于错误输出展示已收集的 --changed-file。
func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

// Set 实现 flag.Value，用于收集可重复传入的 --changed-file 参数。
func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
