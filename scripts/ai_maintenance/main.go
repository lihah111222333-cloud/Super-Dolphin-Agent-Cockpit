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
	"time"

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
	nightlyProtocolFiles            map[string]bool
	archtestNonGoInputFiles         map[string]bool
	archtestNonGoInputPrefixes      []string
}

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
			"scripts/ai_maintenance/owned_gate_execution.go": true, "scripts/ai_maintenance/evidence.go": true,
			".githooks/README.md": true,
		},
		mcpLSPWorkloadPrefixes: []string{"cmd/mcp-lsp/", "scripts/mcp_lsp_workload_"},
		nightlyProtocolFiles: map[string]bool{
			"docs/automation/全仓夜间门禁健康巡检协议.md": true,
			"docs/automation/门禁问题台账接管协议.md":   true,
			"docs/automation/授权问题修复与验证协议.md":  true,
		},
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
	ChangedFiles        []string `json:"changed_files"`
	RequiredGates       []string `json:"required_gates"`
	RequiredEvidence    []string `json:"required_evidence"`
	GeneratedFiles      []string `json:"generated_files"`
	AffectedGoPackages  []string `json:"affected_go_packages,omitempty"`
	DiagnosticFiles     []string `json:"diagnostic_files,omitempty"`
	RequiresEvidenceDoc bool     `json:"requires_evidence_doc"`
}

type evidenceDoc struct {
	Package                      string
	Status                       string
	AgentID                      string
	BaseHead                     string
	OwnedFilesChanged            []string
	UnrelatedDirtyFilesPreserved []string
	LSPEvidence                  map[string]string
	CommandsRun                  []evidenceCommand
	GeneratedFiles               []evidenceGeneratedFile
	Blockers                     []string
}

type evidenceCommand struct {
	Cmd  string
	Exit *int
}

type evidenceGeneratedFile struct {
	Path          string
	SourceCommand string
	Precheck      string
}

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ai_maintenance <plan|run|validate-evidence> [flags]")
	}
	switch args[0] {
	case "plan":
		return runPlan(args[1:], os.Stdout)
	case "run":
		return runGates(args[1:])
	case "validate-evidence":
		return runValidateEvidence(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// runPlan 解析变更路径并输出只读 gate plan JSON。
func runPlan(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	base := fs.String("base", "HEAD~1", "git base revision used when --changed-file is omitted")
	pushGates := fs.Bool("push-gates", false, "include push-only risk gates")
	changed := multiFlag{}
	if stdout == nil {
		stdout = os.Stdout
	}
	fs.Var(&changed, "changed-file", "changed file path; may be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := []string(changed)
	if len(files) == 0 {
		var err error
		files, err = changedFilesFromGit(*base)
		if err != nil {
			return err
		}
	}
	plan, err := gatePlanForScope(files, *pushGates)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

// runGates 是统一入口的执行模式：先根据 diff 生成 gate plan，再可选校验证据包，最后只执行命中的命令面。
func runGates(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	base := fs.String("base", "HEAD~1", "git base revision used when --changed-file is omitted")
	evidencePath := fs.String("evidence", "", "optional AI maintenance evidence file to validate")
	printPlan := fs.Bool("print-plan", false, "print gate plan and exit")
	skipDeferredE2E := fs.Bool("skip-deferred-e2e", false, "exclude deferred provider E2E packages from this gate run")
	cacheDir := fs.String("cache-dir", "", "optional directory for staged-input gate result caching")
	cacheMaxAge := fs.Duration("cache-max-age", defaultGateCacheMaxAge, "maximum age for a cached green gate result")
	cacheScope := fs.String("cache-scope", "", "staged Git tree used as the cache truth source")
	diffCached := fs.Bool("diff-cached", false, "run whitespace checks against the staged index")
	pushGates := fs.Bool("push-gates", false, "include push-only risk gates")
	changed := multiFlag{}
	diffRanges := multiFlag{}
	prevalidatedGates := multiFlag{}
	fs.Var(&changed, "changed-file", "changed file path; may be repeated")
	fs.Var(&diffRanges, "diff-range", "Git range checked for whitespace errors; may be repeated")
	fs.Var(&prevalidatedGates, "prevalidated-gate", "staged map gate already completed by pre-commit; may be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := []string(changed)
	if len(files) == 0 {
		var err error
		files, err = changedFilesFromGit(*base)
		if err != nil {
			return err
		}
	}
	cache, err := validatedGateCacheForRun(*cacheDir, *cacheMaxAge, *cacheScope, prevalidatedGates)
	if err != nil {
		return err
	}
	plan, err := buildGateRunPlan(files, *pushGates, *skipDeferredE2E, []string(prevalidatedGates), *diffCached, *cacheScope)
	if err != nil {
		return err
	}
	executionScope, err := newGateExecutionScope(*diffCached, diffRanges)
	if err != nil {
		return err
	}
	plan = gatePlanForExecutionScope(plan, executionScope)
	if *printPlan {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	if err := validateOptionalEvidence(*evidencePath, plan); err != nil {
		return err
	}
	return executeGatePlanWithCache(plan, cache, executionScope)
}

func validatedGateCacheForRun(root string, maxAge time.Duration, scope string, prevalidated []string) (*gateResultCache, error) {
	cache, err := optionalGateResultCache(root, maxAge, scope)
	if err != nil {
		return nil, err
	}
	if len(prevalidated) > 0 && cache == nil {
		return nil, errors.New("prevalidated map gates require a validated gate cache and isolated index")
	}
	return cache, nil
}

// buildGateRunPlan 依次应用推送风险、延后 E2E 和 staged 预验证约束，保持执行入口低复杂度。
func buildGateRunPlan(files []string, pushGates, skipDeferredE2E bool, prevalidatedGates []string, diffCached bool, cacheScope string) (gatePlan, error) {
	plan, err := gatePlanForScope(files, pushGates)
	if err != nil {
		return gatePlan{}, err
	}
	plan, err = filterDeferredE2E(plan, skipDeferredE2E)
	if err != nil {
		return gatePlan{}, err
	}
	return applyPrevalidatedMapGates(plan, prevalidatedGates, diffCached, pushGates, cacheScope)
}

// applyPrevalidatedMapGates 只允许 staged hook 对同一不可变 tree 跳过刚完成的地图检查。
func applyPrevalidatedMapGates(plan gatePlan, gates []string, diffCached, pushGates bool, cacheScope string) (gatePlan, error) {
	if len(gates) == 0 {
		return plan, nil
	}
	if !diffCached || pushGates || !isGitObjectID(cacheScope) {
		return gatePlan{}, errors.New("prevalidated map gates require staged diff, non-push scope, and an immutable cache tree")
	}
	allowed := map[string]bool{"codemap:check": true, "project-map:check": true}
	required := map[string]bool{}
	for _, gate := range plan.RequiredGates {
		required[gate] = true
	}
	seen := map[string]bool{}
	for _, gate := range gates {
		if !allowed[gate] {
			return gatePlan{}, fmt.Errorf("gate %q cannot be prevalidated", gate)
		}
		if seen[gate] {
			return gatePlan{}, fmt.Errorf("prevalidated gate %q was provided more than once", gate)
		}
		if !required[gate] {
			return gatePlan{}, fmt.Errorf("prevalidated gate %q is absent from the generated plan", gate)
		}
		seen[gate] = true
		plan.RequiredGates = removeGate(plan.RequiredGates, gate)
		fmt.Fprintf(os.Stderr, "[ai-maintenance] prevalidated gate=%s scope=%s\n", gate, cacheScope[:12])
	}
	return plan, nil
}

func gatePlanForExecutionScope(plan gatePlan, scope gateExecutionScope) gatePlan {
	if scope.diffCached {
		plan.RequiredGates = appendOrderedGate(plan.RequiredGates, "gate-image-closure:check")
		plan.RequiredGates = orderGateNames(plan.RequiredGates)
	}
	return plan
}

// filterDeferredE2E 在显式要求时从计划中移除延迟 Provider E2E 包。
func filterDeferredE2E(plan gatePlan, skip bool) (gatePlan, error) {
	if !skip {
		return plan, nil
	}
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return gatePlan{}, fmt.Errorf("resolve deferred E2E package owner: %w", err)
	}
	packages, err := excludeDeferredE2EGoPackages(
		plan.AffectedGoPackages,
		filepath.Join(repoRoot, "scripts", "ai_maintenance", "deferred_e2e_packages.txt"),
	)
	if err != nil {
		return gatePlan{}, err
	}
	plan.AffectedGoPackages = packages
	if len(packages) == 0 {
		plan.RequiredGates = removeGate(plan.RequiredGates, "backend:test_with_guard")
	}
	return plan, nil
}

// validateOptionalEvidence 校验显式证据文件；未提供时保留控制器阻断提示但继续运行命令 gate。
func validateOptionalEvidence(path string, plan gatePlan) error {
	if path != "" {
		return validateEvidenceFile(path, plan)
	}
	if plan.RequiresEvidenceDoc {
		fmt.Fprintln(os.Stderr, "ai-maintenance evidence file not supplied; command gates will run, but LSP evidence remains controller-blocking")
	}
	return nil
}

// runValidateEvidence 解析变更范围并校验指定 evidence 文件是否满足计划要求。
func runValidateEvidence(args []string) error {
	fs := flag.NewFlagSet("validate-evidence", flag.ContinueOnError)
	base := fs.String("base", "HEAD~1", "git base revision used when --changed-file is omitted")
	evidencePath := fs.String("evidence", "", "AI maintenance evidence file")
	changed := multiFlag{}
	fs.Var(&changed, "changed-file", "changed file path; may be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *evidencePath == "" {
		return errors.New("--evidence is required")
	}
	files := []string(changed)
	if len(files) == 0 {
		var err error
		files, err = changedFilesFromGit(*base)
		if err != nil {
			return err
		}
	}
	plan, err := buildGatePlan(files)
	if err != nil {
		return err
	}
	return validateEvidenceFile(*evidencePath, plan)
}

// buildGatePlan 把 changed files 映射成必须执行的命令和必须提交的证据项。
// 路由规则保持路径前缀级别，避免把任务语义藏进不可审计的动态推断。
func buildGatePlan(files []string) (gatePlan, error) {
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return gatePlan{}, fmt.Errorf("resolve repository root for capability path rules: %w", err)
	}
	return buildGatePlanForRepo(repoRoot, files, newGatePlanPolicy())
}

// buildGatePlanForRepo 使用 generator AST 派生的 capability roots 构造计划；解析失败时拒绝产出不完整计划。
func buildGatePlanForRepo(repoRoot string, files []string, policy gatePlanPolicy) (gatePlan, error) {
	capabilityRules, err := capcontract.LoadPathRules(repoRoot)
	if err != nil {
		return gatePlan{}, fmt.Errorf("load capability-contract path rules: %w", err)
	}
	turnContractPaths, err := loadTurnContractPaths(repoRoot, policy)
	if err != nil {
		return gatePlan{}, err
	}
	normalized := normalizeFiles(files)
	plan := gatePlan{ChangedFiles: normalized}
	gates := map[string]bool{"diff:whitespace": true}
	evidence := map[string]bool{}
	generated := map[string]bool{}
	backendChanged := false
	for _, file := range normalized {
		backendFile, err := applyFileGateRules(file, capabilityRules, turnContractPaths, gates, evidence, generated, policy)
		if err != nil {
			return gatePlan{}, err
		}
		if backendFile {
			backendChanged = true
		}
	}
	if backendChanged {
		gates["backend:test_with_guard"] = true
		delete(gates, "ai-maintenance:self-test")
	}
	plan.DiagnosticFiles = changedDiagnosticFiles(normalized)
	if len(plan.DiagnosticFiles) > 0 {
		gates["lsp:changed-diagnostics"] = true
	}
	plan.RequiredGates = orderedGates(gates)
	plan.RequiredEvidence = sortedKeys(evidence)
	plan.GeneratedFiles = sortedKeys(generated)
	plan.RequiresEvidenceDoc = len(plan.RequiredEvidence) > 0
	if backendChanged {
		plan.AffectedGoPackages, err = affectedGoPackages(repoRoot, normalized, policy)
		if err != nil {
			return gatePlan{}, err
		}
	}
	return plan, nil
}

// applyFileGateRules 汇总单个路径触发的命令 gate 和证据要求，并返回它是否属于 Go/后端验证面。
func applyFileGateRules(file string, capabilityRules capcontract.PathRules, turnContractPaths, gates, evidence, generated map[string]bool, policy gatePlanPolicy) (bool, error) {
	backendChanged := applySourceGateRules(file, gates, evidence)
	if err := applyCapabilityContractGateRules(file, capabilityRules, gates, evidence, generated); err != nil {
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
	applyGeneratedFileEvidence(file, evidence, generated)
	return backendChanged, nil
}

func applyGeneratedFileEvidence(file string, evidence, generated map[string]bool) {
	if !generatedCodemapFile(file) {
		return
	}
	generated[file] = true
	evidence["generated:source"] = true
}

// applyCapabilityContractGateRules 统一能力清单生成输入与检查路由，并在路径规则解析失败时阻断。
func applyCapabilityContractGateRules(file string, capabilityRules capcontract.PathRules, gates, evidence, generated map[string]bool) error {
	capabilityChanged, err := capabilityRules.Match(file)
	if err != nil {
		return fmt.Errorf("match capability-contract path %q: %w", file, err)
	}
	if capabilityChanged || backendGoFile(file) || file == capabilityContractManifest {
		gates["capcontract:check"] = true
	}
	if capabilityContractProducerInput(file) {
		generated[capabilityContractManifest] = true
		evidence["generated:source"] = true
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
	return frontendProductionScriptRelevant(file) || policy.frontendStaticGuardFiles[file]
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

// applySourceGateRules 根据生产源码路径补充必须执行的门禁和 LSP 证据要求。
func applySourceGateRules(file string, gates, evidence map[string]bool) bool {
	switch {
	case strings.HasPrefix(file, "frontend-app/"):
		if criticalTypecheckRelevant(file) {
			gates["frontend:typecheck-contracts"] = true
		}
		if frontendLintRelevant(file) {
			gates["frontend:lint"] = true
		}
		if frontendChangedTestRelevant(file) {
			gates["frontend:changed-tests"] = true
		}
		if frontendDiagnosticsRelevant(file) {
			requireLSPEvidence(file, evidence)
		}
	case strings.HasPrefix(file, "cmd/"), strings.HasPrefix(file, "internal/"), strings.HasPrefix(file, "pkg/"):
		requireLSPEvidence(file, evidence)
		return true
	case strings.HasPrefix(file, "scripts/") && strings.HasSuffix(file, ".go"):
		requireLSPEvidence(file, evidence)
		return true
	}
	return false
}

// criticalTypecheckRelevant 判断变更是否会影响关键前端严格类型检查闭包。
func criticalTypecheckRelevant(file string) bool {
	return file == "frontend-app/tsconfig.contracts.json" ||
		file == "frontend-app/scripts/critical-typecheck-files.json" ||
		file == "frontend-app/scripts/critical-typecheck-guard.mjs" ||
		file == "frontend-app/scripts/contracts-typecheck-guard.test.mjs" ||
		(strings.HasPrefix(file, "frontend-app/src/") &&
			(strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".jsx")) &&
			!isFrontendTestFile(strings.TrimPrefix(file, "frontend-app/")))
}

func requireLSPEvidence(file string, evidence map[string]bool) {
	evidence["lsp:diagnostics"] = true
	if sourceLike(file) {
		evidence["lsp:locate"] = true
		evidence["lsp:inspect"] = true
		evidence["lsp:xref"] = true
		evidence["lsp:read_file"] = true
	}
}

func changedFilesFromGit(base string) ([]string, error) {
	out, err := exec.Command("git", "diff", "--name-only", "-z", base+"...HEAD").CombinedOutput()
	if err != nil {
		out, err = exec.Command("git", "diff", "--name-only", "-z", base).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git diff changed files: %w\n%s", err, out)
		}
	}
	files := nulSeparatedPaths(out)
	untracked, err := exec.Command("git", "ls-files", "--others", "--exclude-standard", "-z").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git untracked changed files: %w\n%s", err, untracked)
	}
	files = append(files, nulSeparatedPaths(untracked)...)
	return normalizeFiles(files), nil
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
		"cmd/", "docs/", "frontend-app/", "internal/", "migrations/", "pkg/",
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
		"frontend:e2e",
		"frontend:embed-verify",
		"frontend:performance-verify",
		"workflow:actionlint",
		"release:semantic-guards",
		"nightly-protocol:check",
		"mcp-lsp:catalog",
		"mcp-lsp:idle-quick",
		"backend:test_with_guard",
		"backend:test-integrity",
		"lsp:changed-diagnostics",
		"backend:archtest",
		"backend:nilness",
		"backend:race",
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
