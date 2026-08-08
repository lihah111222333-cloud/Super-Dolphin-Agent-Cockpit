package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

type gateExecutionScope struct {
	diffCached bool
	diffRanges []string
}

func newGateExecutionScope(diffCached bool, diffRanges []string) (gateExecutionScope, error) {
	if diffCached && len(diffRanges) > 0 {
		return gateExecutionScope{}, errors.New("--diff-cached and --diff-range are mutually exclusive")
	}
	for _, diffRange := range diffRanges {
		if strings.TrimSpace(diffRange) == "" {
			return gateExecutionScope{}, errors.New("--diff-range must not be empty")
		}
	}
	return gateExecutionScope{diffCached: diffCached, diffRanges: append([]string(nil), diffRanges...)}, nil
}

// executeGatePlan 严格按计划执行命令；必需 gate 缺少 runner 时立即失败。
func executeGatePlan(plan gatePlan) error {
	return executeGatePlanWithCache(plan, nil, gateExecutionScope{})
}

// executeGatePlanWithCache 逐项执行计划，并仅对声明为可缓存的绿色 gate 复用严格输入指纹结果。
func executeGatePlanWithCache(plan gatePlan, cache *gateResultCache, executionScope gateExecutionScope) error {
	runners := gateRunners(plan, executionScope)
	for _, gate := range plan.RequiredGates {
		runner, ok := runners[gate]
		if !ok {
			return fmt.Errorf("required gate %q has no runner", gate)
		}
		if cache != nil && runner.cacheable {
			if err := cache.run(gate, plan, runner.run); err != nil {
				return err
			}
			continue
		}
		if err := runGateWithTiming(gate, runner.run); err != nil {
			return err
		}
	}
	return nil
}

// gateRunners 根据 gate 计划和明确 Git 真值范围构造可执行命令表。
func gateRunners(plan gatePlan, executionScope gateExecutionScope) map[string]gateRunner {
	generatedCheck := func(cacheable bool, name string, args ...string) gateRunner {
		return gateRunner{cacheable: cacheable, run: func() error {
			return runCommand("", name, args...)
		}}
	}
	cacheable := func(run func() error) gateRunner {
		return gateRunner{cacheable: true, run: run}
	}
	runners := map[string]gateRunner{
		"ai-maintenance:self-test": cacheable(func() error {
			if err := runCommand("", "go", "test", "./scripts/ai_maintenance", "-count=1"); err != nil {
				return err
			}
			return runCommand("", "go", "test", "./scripts", "-run", "TestAIMaintenanceGate", "-count=1")
		}),
		"backend:test_with_guard": cacheable(func() error {
			args, err := backendTestWithGuardArgs(plan)
			if err != nil {
				return err
			}
			return runCommand("", args[0], args[1:]...)
		}),
		"lsp:changed-diagnostics": cacheable(func() error {
			files, deleted, err := existingDiagnosticFiles(plan.DiagnosticFiles)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				if deleted > 0 && deleted == len(plan.DiagnosticFiles) {
					fmt.Fprintf(os.Stderr, "[ai-maintenance] lsp diagnostics skip: planned=%d existing=0 reason=all-deleted\n", len(plan.DiagnosticFiles))
					return nil
				}
				return errors.New("lsp diagnostics gate has no planned files")
			}
			args := []string{"run", "./scripts/lsp_diagnostics_gate"}
			for _, file := range files {
				args = append(args, "--file", file)
			}
			return runCommand("", "go", args...)
		}),
		"backend:race": cacheable(func() error {
			args, err := backendRaceArgs(plan)
			if err != nil {
				return err
			}
			return runCommand("", args[0], args[1:]...)
		}),
		"backend:archtest": cacheable(func() error {
			return runCommand("", "./scripts/test_with_guard.sh", "--archtest-only")
		}),
		"backend:nilness": cacheable(func() error {
			packages := affectedNilnessPackages(plan)
			args := append([]string{"go", "run", "./scripts/nilness_guard.go", "-test=false", "--"}, packages...)
			return runCommand("", args[0], args[1:]...)
		}),
		"capcontract:check":   generatedCheck(true, "make", "capcontract-check"),
		"turncontract:verify": {run: runTurnContractVerifyGate},
		"frontend:static-guards": {run: func() error {
			dir, name, args := frontendStaticGuardCommand()
			return runCommand(dir, name, args...)
		}},
		"gate-image-closure:check": {run: func() error {
			return runGateImageClosureCheck(".")
		}},
		"frontend:lint":                cacheable(func() error { return runCommand("frontend-app", "npm", "run", "lint") }),
		"frontend:typecheck-contracts": {run: func() error { return runCommand("frontend-app", "npm", "run", "typecheck:contracts") }},
		"frontend:changed-tests":       cacheable(func() error { return runFrontendChangedTests(plan) }),
		"frontend:embed-verify":        cacheable(func() error { return runFrontendEmbedVerify(executionScope) }),
		"frontend:performance-verify": {run: func() error {
			return runCommand("frontend-app", "npm", "run", "performance:verify")
		}},
		"codemap:check":     generatedCheck(false, "make", "codemap-check"),
		"project-map:check": generatedCheck(true, "make", "project-map-check", "PROJECT_MAP_ARGS="),
		"sqlc:verify":       cacheable(func() error { return runCommand("", "make", "sqlc-verify-worktree") }),
		"diff:whitespace":   {run: func() error { return runWhitespaceCheck(executionScope) }},
	}
	maps.Copy(runners, ownedGateRunners(plan, newE2EExecutionPolicy()))
	return runners
}

// runFrontendEmbedVerify 通过隔离 runner 执行嵌入验证。
func runFrontendEmbedVerify(executionScope gateExecutionScope) error {
	return runCommand("frontend-app", "npm", frontendEmbedVerifyArgs(executionScope)...)
}

// frontendEmbedVerifyArgs 将 staged 真值显式传给隔离 runner。
func frontendEmbedVerifyArgs(executionScope gateExecutionScope) []string {
	args := []string{"run", "verify:embed:isolated"}
	if executionScope.diffCached {
		args = append(args, "--", "--cached")
	}
	return args
}

// frontendStaticGuardCommand 返回 hook 与 CI 共用的只读前端静态门禁命令。
func frontendStaticGuardCommand() (string, string, []string) {
	return "frontend-app", "npm", []string{"run", "guard:critical-skip"}
}

func runTurnContractVerifyGate() error {
	if err := runCommand("", "go", "run", "./scripts/turncontract", "--verify"); err != nil {
		return err
	}
	if err := runCommand("", "go", "test", "./internal/dto/turn", "-run", "^TestTurnContractFieldGuard", "-count=1"); err != nil {
		return err
	}
	return runCommand("", "node", "frontend-app/scripts/turn-contract-field-guard.mjs")
}

// runFrontendChangedTests 执行与 staged 前端源码同名或直接变更的 Vitest 文件，避免 pre-commit 退化为整套 npm test。
func runFrontendChangedTests(plan gatePlan) error {
	tests, err := frontendChangedTestFiles(plan.ChangedFiles)
	if err != nil {
		return err
	}
	if len(tests) == 0 {
		return errors.New("frontend changed-tests gate has no matching test files")
	}
	args := append([]string{"vitest", "run"}, tests...)
	args = append(args, "--no-file-parallelism", "--maxWorkers=1")
	return runCommand("frontend-app", "npx", args...)
}

// frontendChangedTestFiles 根据前端 diff 收集直接变更或同名配对的 Vitest 测试文件。
func frontendChangedTestFiles(files []string) ([]string, error) {
	seen := map[string]bool{}
	for _, file := range files {
		if !frontendChangedTestRelevant(file) {
			continue
		}
		rel := strings.TrimPrefix(file, "frontend-app/")
		candidates := []string{}
		if isFrontendTestFile(rel) {
			candidates = append(candidates, rel)
		} else if candidate := pairedFrontendTestFile(rel); candidate != "" {
			candidates = append(candidates, candidate)
		}
		for _, candidate := range candidates {
			if frontendVitestDefaultExcludedTestFile(candidate) {
				continue
			}
			if _, err := os.Stat(filepath.Join("frontend-app", candidate)); err == nil {
				seen[candidate] = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("stat frontend test target %q: %w", candidate, err)
			}
		}
	}
	return sortedKeys(seen), nil
}

func isFrontendTestFile(file string) bool {
	base := filepath.Base(file)
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func frontendVitestDefaultExcludedTestFile(file string) bool {
	base := filepath.Base(file)
	return strings.Contains(base, "benchmark.test.") ||
		(strings.HasPrefix(file, "scripts/performance-") && isFrontendTestFile(file))
}

func pairedFrontendTestFile(file string) string {
	ext := filepath.Ext(file)
	if ext == "" {
		return ""
	}
	return strings.TrimSuffix(file, ext) + ".test" + ext
}

// existingDiagnosticFiles keeps deleted paths out of the live diagnostics request while
// preserving every currently existing file selected from the Git truth source.
func existingDiagnosticFiles(files []string) (existing []string, deleted int, err error) {
	existing = make([]string, 0, len(files))
	for _, file := range files {
		info, err := os.Stat(file)
		if errors.Is(err, os.ErrNotExist) {
			deleted++
			continue
		}
		if err != nil {
			return nil, deleted, fmt.Errorf("stat diagnostics target %q: %w", file, err)
		}
		if !info.Mode().IsRegular() {
			return nil, deleted, fmt.Errorf("diagnostics target %q is not a regular file", file)
		}
		existing = append(existing, file)
	}
	return existing, deleted, nil
}

func runGateImageClosureCheck(directory string) error {
	tree, err := resolveGateImageClosureTree(directory)
	if err != nil {
		return err
	}
	return runCommand("", "go", gateImageClosureCheckArgs(tree)...)
}

// resolveGateImageClosureTree 从指定仓库的 Git index 生成并复核 canonical closure tree object。
func resolveGateImageClosureTree(directory string) (string, error) {
	output, err := exec.Command("git", "-C", directory, "write-tree").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve gate image closure staged tree: %w\n%s", err, output)
	}
	tree := strings.TrimSpace(string(output))
	invalidHex := strings.IndexFunc(tree, func(value rune) bool {
		return (value < '0' || value > '9') && (value < 'a' || value > 'f')
	}) >= 0
	if (len(tree) != 40 && len(tree) != 64) || invalidHex {
		return "", fmt.Errorf("resolve gate image closure staged tree: invalid object ID %q", tree)
	}
	verified, err := exec.Command("git", "-C", directory, "rev-parse", "--verify", tree+"^{tree}").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("verify gate image closure staged tree: %w\n%s", err, verified)
	}
	if strings.TrimSpace(string(verified)) != tree {
		return "", errors.New("verify gate image closure staged tree: object ID drifted")
	}
	return tree, nil
}

func gateImageClosureCheckArgs(tree string) []string {
	return []string{"run", "./cmd/super-dolphin-gate", "closure", "check", "--tree", tree}
}

func backendTestWithGuardArgs(plan gatePlan) ([]string, error) {
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return nil, fmt.Errorf("resolve backend regression owner: %w", err)
	}
	return backendTestWithGuardArgsForRepo(plan, repoRoot)
}

// backendTestWithGuardArgsForRepo 对删除或全局输入变化使用 ci-l1 的完整、分序工作区回归。
func backendTestWithGuardArgsForRepo(plan gatePlan, repoRoot string) ([]string, error) {
	fullRegression, err := requiresFullWorkspaceRegression(repoRoot, plan.ChangedFiles)
	if err != nil {
		return nil, err
	}
	if fullRegression {
		return []string{"make", "ci-l1"}, nil
	}
	packages := plan.AffectedGoPackages
	if !requiresBroadBackendRegression(plan.ChangedFiles) {
		packages, err = resolveDirectReverseDependentPackages(packages)
		if err != nil {
			return nil, err
		}
	}
	if len(packages) == 0 {
		return nil, errors.New("backend gate resolved no Go packages")
	}
	args := []string{"./scripts/test_with_guard.sh"}
	if !requiresFullArchtest(plan.ChangedFiles) {
		args = append(args, "--quick-guard")
	}
	args = append(args, packages...)
	return append(args, "-count=1"), nil
}

func requiresFullArchtest(files []string) bool {
	if requiresBroadBackendRegression(files) {
		return true
	}
	for _, file := range files {
		if strings.HasPrefix(file, "internal/archtest/") || file == "scripts/code_size_guard.go" {
			return true
		}
	}
	return false
}

// backendRaceArgs isolates the push-only race lane so a matching normal gate can be reused.
func backendRaceArgs(plan gatePlan) ([]string, error) {
	racePackages := affectedRacePackagesForPlan(plan)
	if len(racePackages) == 0 {
		return nil, errors.New("backend race gate requires affected race packages")
	}
	return append([]string{"./scripts/test_with_guard.sh", "--race-only"}, racePackages...), nil
}

// runWhitespaceCheck 根据 hook 显式传入的 staged 或 push-range 真值源检查空白错误。
func runWhitespaceCheck(scope gateExecutionScope) error {
	if scope.diffCached {
		return runCommand("", "git", "diff", "--cached", "--check")
	}
	if len(scope.diffRanges) == 0 {
		return runCommand("", "git", "diff", "--check")
	}
	for _, diffRange := range scope.diffRanges {
		args := []string{"diff", "--check", diffRange}
		if !strings.Contains(diffRange, "..") {
			emptyTree, err := gitEmptyTree()
			if err != nil {
				return err
			}
			args = []string{"diff", "--check", emptyTree, diffRange}
		}
		if err := runCommand("", "git", args...); err != nil {
			return err
		}
	}
	return nil
}

func gitEmptyTree() (string, error) {
	out, err := exec.Command("git", "hash-object", "-t", "tree", "/dev/null").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve empty Git tree: %w\n%s", err, out)
	}
	emptyTree := strings.TrimSpace(string(out))
	if !isGitObjectID(emptyTree) {
		return "", fmt.Errorf("resolve empty Git tree returned invalid object ID %q", emptyTree)
	}
	return emptyTree, nil
}

func gateCommandEnvironment(name string) []string {
	env := os.Environ()
	if name == "git" {
		return env
	}

	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_INDEX_FILE=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
