package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

var agentIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var aiMaintenanceFiles = map[string]bool{
	"Makefile":                                   true,
	"scripts/ai_maintenance_gates.sh":            true,
	"scripts/ai_maintenance_gates_guard_test.go": true,
	"scripts/configure_hook_node_runtime.sh":     true,
	"scripts/frontend_embed_verify.sh":           true,
	"scripts/refresh_generated_artifacts.sh":     true,
	"scripts/sqlc_verify_worktree.sh":            true,
	"scripts/test_with_guard.sh":                 true,
}

var turnContractInfrastructureFiles = map[string]bool{
	"scripts/turncontract/main.go":                                 true,
	"frontend-app/package.json":                                    true,
	"frontend-app/scripts/turn-contract-field-guard.mjs":           true,
	"frontend-app/scripts/turn-contract-field-guard.test.mjs":      true,
	"frontend-app/src/shared/contracts/turnContracts.generated.js": true,
}

var frontendStaticGuardFiles = map[string]bool{
	"frontend-app/package.json":                                         true,
	"frontend-app/scripts/frontend-state-ownership-registry.json":       true,
	"frontend-app/scripts/frontend-state-ownership-guard.mjs":           true,
	"frontend-app/scripts/frontend-state-ownership-guard.test.mjs":      true,
	"frontend-app/scripts/frontend-dependency-direction-registry.json":  true,
	"frontend-app/scripts/frontend-dependency-direction-guard.mjs":      true,
	"frontend-app/scripts/frontend-dependency-direction-guard.test.mjs": true,
}

var coreBackendGatePackages = []string{
	"./cmd/mcp-lsp",
	"./cmd/mcp-orch",
	"./internal/app",
	"./internal/module/thread",
	"./internal/platform/config",
	"./internal/platform/toolbridge",
	"./internal/provider/contracttest",
	"./internal/provider/unified",
	"./internal/provider/codexapp",
	"./internal/provider/claudecli",
	"./internal/provider",
	"./scripts",
	"./scripts/ai_maintenance",
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
	fs.Var(&changed, "changed-file", "changed file path; may be repeated")
	fs.Var(&diffRanges, "diff-range", "Git range checked for whitespace errors; may be repeated")
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
	plan, err = filterDeferredE2E(plan, *skipDeferredE2E)
	if err != nil {
		return err
	}
	if *printPlan {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	if err := validateOptionalEvidence(*evidencePath, plan); err != nil {
		return err
	}
	cache, err := optionalGateResultCache(*cacheDir, *cacheMaxAge, *cacheScope)
	if err != nil {
		return err
	}
	executionScope, err := newGateExecutionScope(*diffCached, diffRanges)
	if err != nil {
		return err
	}
	return executeGatePlanWithCache(plan, cache, executionScope)
}

// filterDeferredE2E 在显式要求时从计划中移除延迟 Provider E2E 包。
func filterDeferredE2E(plan gatePlan, skip bool) (gatePlan, error) {
	if !skip {
		return plan, nil
	}
	packages, err := excludeDeferredE2EGoPackages(
		plan.AffectedGoPackages,
		"scripts/ai_maintenance/deferred_e2e_packages.txt",
	)
	if err != nil {
		return gatePlan{}, err
	}
	plan.AffectedGoPackages = packages
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
	return buildGatePlanForRepo(repoRoot, files)
}

// buildGatePlanForRepo 使用 generator AST 派生的 capability roots 构造计划；解析失败时拒绝产出不完整计划。
func buildGatePlanForRepo(repoRoot string, files []string) (gatePlan, error) {
	capabilityRules, err := capcontract.LoadPathRules(repoRoot)
	if err != nil {
		return gatePlan{}, fmt.Errorf("load capability-contract path rules: %w", err)
	}
	turnContractPaths, err := loadTurnContractPaths(repoRoot)
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
		backendFile, err := applyFileGateRules(file, capabilityRules, turnContractPaths, gates, evidence, generated)
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
		plan.AffectedGoPackages = affectedGoPackages(normalized)
	}
	return plan, nil
}

// applyFileGateRules 汇总单个路径触发的命令 gate 和证据要求，并返回它是否属于 Go/后端验证面。
func applyFileGateRules(file string, capabilityRules capcontract.PathRules, turnContractPaths, gates, evidence, generated map[string]bool) (bool, error) {
	backendChanged := applySourceGateRules(file, gates, evidence)
	if err := applyCapabilityContractGateRules(file, capabilityRules, gates, evidence, generated); err != nil {
		return false, err
	}
	if aiMaintenanceRelevant(file) {
		gates["ai-maintenance:self-test"] = true
	}
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
	if frontendStaticGuardRelevant(file) {
		gates["frontend:static-guards"] = true
	}
	if codemapRelevant(file) {
		gates["codemap:check"] = true
		gates["project-map:check"] = true
	}
	if generatedCodemapFile(file) {
		generated[file] = true
		evidence["generated:source"] = true
	}
	return backendChanged, nil
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
func frontendStaticGuardRelevant(file string) bool {
	return strings.HasPrefix(file, "frontend-app/src/") || frontendStaticGuardFiles[file]
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
func aiMaintenanceRelevant(file string) bool {
	return aiMaintenanceFiles[file] ||
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
	case "scripts/test_with_guard.sh":
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
		gates["frontend:lint"] = true
		gates["frontend:test"] = true
		gates["frontend:build"] = true
		gates["frontend:embed-verify"] = true
		requireLSPEvidence(file, evidence)
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
			(strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".jsx")))
}

// affectedGoPackages 合并稳定后端回归包与 diff 命中的 Go 包，并避免 archtest 被守卫包装器重复执行。
func affectedGoPackages(files []string) []string {
	packages := map[string]bool{}
	for _, pkg := range coreBackendGatePackages {
		packages[pkg] = true
	}
	for _, file := range files {
		if pkg, ok := changedGoPackage(file); ok {
			if pkg == "./internal/archtest" {
				continue
			}
			packages[pkg] = true
		}
	}
	return sortedKeys(packages)
}

func changedGoPackage(file string) (string, bool) {
	if !strings.HasSuffix(file, ".go") {
		return "", false
	}
	switch {
	case strings.HasPrefix(file, "cmd/"),
		strings.HasPrefix(file, "internal/"),
		strings.HasPrefix(file, "pkg/"),
		strings.HasPrefix(file, "scripts/"):
		dir := filepath.ToSlash(filepath.Dir(file))
		if dir == "." || dir == "" {
			return "", false
		}
		return "./" + dir, true
	default:
		return "", false
	}
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

// statusEvidenceProblems 校验 evidence 状态字段之间的一致性，先于命令和 LSP 证据校验执行。
func statusEvidenceProblems(doc evidenceDoc) []string {
	problems := agentIDEvidenceProblems(doc)
	problems = append(problems, statusValueProblems(doc)...)
	return append(problems, blockerStatusProblems(doc)...)
}

// agentIDEvidenceProblems 要求 agentid 是平台 UUID，避免 worker-1 这类不可追溯别名混入证据链。
func agentIDEvidenceProblems(doc evidenceDoc) []string {
	if strings.TrimSpace(doc.AgentID) == "" {
		return []string{"missing AGENTID"}
	}
	if !agentIDPattern.MatchString(strings.TrimSpace(doc.AgentID)) {
		return []string{"AGENTID must be exact platform UUID"}
	}
	return nil
}

// statusValueProblems 限定 evidence 状态枚举，防止自由文本状态被当成可验收结果。
func statusValueProblems(doc evidenceDoc) []string {
	if strings.TrimSpace(doc.Status) == "" {
		return []string{"missing STATUS"}
	}
	if doc.Status != "" && doc.Status != "DONE_WITH_EVIDENCE" && doc.Status != "BLOCKED" && doc.Status != "PLAN_BLOCKER" {
		return []string{"unsupported STATUS " + doc.Status}
	}
	return nil
}

// blockerStatusProblems 区分完成报告和阻塞报告：完成不能带 blocker，阻塞必须说明 blocker。
func blockerStatusProblems(doc evidenceDoc) []string {
	var problems []string
	if doc.Status == "DONE_WITH_EVIDENCE" && len(doc.CommandsRun) == 0 {
		problems = append(problems, "DONE_WITH_EVIDENCE requires COMMANDS_RUN")
	}
	if doc.Status == "DONE_WITH_EVIDENCE" && len(doc.Blockers) > 0 {
		problems = append(problems, "DONE_WITH_EVIDENCE must not include BLOCKERS")
	}
	if doc.Status == "BLOCKED" || doc.Status == "PLAN_BLOCKER" {
		if len(doc.Blockers) == 0 {
			problems = append(problems, doc.Status+" requires BLOCKERS")
		}
	}
	return problems
}

// lspEvidenceProblems 确认计划要求的每一类 LSP 证据都明确 PASS。
func lspEvidenceProblems(doc evidenceDoc, plan gatePlan) []string {
	var problems []string
	for _, want := range plan.RequiredEvidence {
		if key, ok := strings.CutPrefix(want, "lsp:"); ok {
			if !evidencePassed(doc.LSPEvidence[key]) {
				problems = append(problems, "missing or non-pass LSP evidence "+key)
			}
		}
	}
	return problems
}

func diffScopeProblems(doc evidenceDoc, plan gatePlan) []string {
	if len(plan.ChangedFiles) > 0 && !sameStringSet(plan.ChangedFiles, doc.OwnedFilesChanged) {
		return []string{"OWNED_FILES_CHANGED does not match changed files"}
	}
	return nil
}

func generatedEvidenceProblems(doc evidenceDoc, plan gatePlan) []string {
	var problems []string
	for _, generatedFile := range plan.GeneratedFiles {
		if !generatedEvidencePresent(doc.GeneratedFiles, generatedFile) {
			problems = append(problems, "generated file lacks check-failed plus refresh evidence: "+generatedFile)
		}
	}
	return problems
}

func gateEvidenceCommandFragments() map[string][]string {
	return map[string][]string{
		"ai-maintenance:self-test":         {"go test ./scripts/ai_maintenance", "go test ./scripts -run TestAIMaintenanceGate"},
		"backend:test_with_guard":          {"./scripts/test_with_guard.sh"},
		"backend:test_with_guard_and_race": {"./scripts/test_with_guard.sh", "--with-race", "-race"},
		"backend:nilness":                  {"go run ./scripts/nilness_guard.go"},
		"lsp:changed-diagnostics":          {"go run ./scripts/lsp_diagnostics_gate"},
		"capcontract:check":                {"make capcontract-check"},
		"turncontract:verify":              {"go run ./scripts/turncontract --verify", "go test ./internal/dto/turn -run ^TestTurnContractFieldGuard", "node frontend-app/scripts/turn-contract-field-guard.mjs"},
		"frontend:static-guards":           {"npm run guard:architecture"},
		"frontend:lint":                    {"npm run lint"},
		"frontend:typecheck-contracts":     {"npm run typecheck:contracts"},
		"frontend:test":                    {"npm test"},
		"frontend:build":                   {"npm run build"},
		"frontend:embed-verify":            {"make frontend-embed-verify"},
		"codemap:check":                    {"make codemap-check"},
		"project-map:check":                {"make project-map-check"},
		"sqlc:verify":                      {"make sqlc-verify-worktree", "make sqlc-verify"},
	}
}

func changedFilesFromGit(base string) ([]string, error) {
	out, err := exec.Command("git", "diff", "--name-only", base+"...HEAD").CombinedOutput()
	if err != nil {
		out, err = exec.Command("git", "diff", "--name-only", base).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git diff changed files: %w\n%s", err, out)
		}
	}
	files := lines(string(out))
	untracked, err := exec.Command("git", "ls-files", "--others", "--exclude-standard").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git untracked changed files: %w\n%s", err, untracked)
	}
	files = append(files, lines(string(untracked))...)
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
		file = filepath.ToSlash(strings.TrimSpace(file))
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

// codemapRelevant 判断变更是否可能影响代码地图或 AI 项目地图。
func codemapRelevant(file string) bool {
	return strings.HasPrefix(file, "cmd/") ||
		strings.HasPrefix(file, "internal/") ||
		strings.HasPrefix(file, "pkg/") ||
		strings.HasPrefix(file, "frontend-app/src/") ||
		strings.HasPrefix(file, "docs/doc/codemap/") ||
		file == ".ai-project-map.overrides.json" ||
		file == "scripts/generate_ai_project_map.mjs" ||
		file == "scripts/codemap_index.go"
}

func generatedCodemapFile(file string) bool {
	return file == "docs/doc/codemap/ai-index.json" ||
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
		"frontend:test",
		"frontend:build",
		"frontend:embed-verify",
		"backend:test_with_guard",
		"lsp:changed-diagnostics",
		"backend:test_with_guard_and_race",
		"backend:nilness",
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
