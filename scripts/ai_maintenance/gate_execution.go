package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	runners := map[string]gateRunner{
		"ai-maintenance:self-test": {run: func() error {
			if err := runCommand("", "go", "test", "./scripts/ai_maintenance", "-count=1"); err != nil {
				return err
			}
			return runCommand("", "go", "test", "./scripts", "-run", "TestAIMaintenanceGate", "-count=1")
		}},
		"backend:test_with_guard": {run: func() error {
			args := append([]string{"./scripts/test_with_guard.sh"}, plan.AffectedGoPackages...)
			args = append(args, "-count=1")
			return runCommand("", args[0], args[1:]...)
		}},
		"lsp:changed-diagnostics": {run: func() error {
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
		}},
		"backend:test_with_guard_and_race": {run: func() error {
			args, err := backendTestWithGuardAndRaceArgs(plan)
			if err != nil {
				return err
			}
			return runCommand("", args[0], args[1:]...)
		}},
		"backend:nilness": {run: func() error {
			packages := affectedNilnessPackages(plan)
			args := append([]string{"go", "run", "./scripts/nilness_guard.go", "-test=false", "--"}, packages...)
			return runCommand("", args[0], args[1:]...)
		}},
		"capcontract:check": generatedCheck(false, "make", "capcontract-check"),
		"turncontract:verify": {run: func() error {
			if err := runCommand("", "go", "run", "./scripts/turncontract", "--verify"); err != nil {
				return err
			}
			if err := runCommand("", "go", "test", "./internal/dto/turn", "-run", "^TestTurnContractFieldGuard", "-count=1"); err != nil {
				return err
			}
			return runCommand("", "node", "frontend-app/scripts/turn-contract-field-guard.mjs")
		}},
		"frontend:static-guards": {run: func() error {
			return runCommand("frontend-app", "npm", "run", "guard:architecture")
		}},
		"frontend:lint":          {run: func() error { return runCommand("frontend-app", "npm", "run", "lint") }},
		"frontend:changed-tests": {run: func() error { return runFrontendChangedTests(plan) }},
		"frontend:build":         {run: func() error { return runCommand("frontend-app", "npm", "run", "build") }},
		"frontend:embed-verify":  {run: func() error { return runCommand("", "make", "frontend-embed-verify") }},
		"frontend:performance-verify": {run: func() error {
			return runCommand("frontend-app", "npm", "run", "performance:verify")
		}},
		"codemap:check":     generatedCheck(false, "make", "codemap-check"),
		"project-map:check": generatedCheck(true, "make", "project-map-check", "PROJECT_MAP_ARGS="),
		"sqlc:verify":       {run: func() error { return runCommand("", "make", "sqlc-verify-worktree") }},
		"diff:whitespace":   {run: func() error { return runWhitespaceCheck(executionScope) }},
	}
	runners["frontend:typecheck-contracts"] = gateRunner{run: func() error {
		return runCommand("frontend-app", "npm", "run", "typecheck:contracts")
	}}
	return runners
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
			if _, err := os.Stat(filepath.Join("frontend-app", candidate)); err == nil {
				seen[candidate] = true
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
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

// backendTestWithGuardAndRaceArgs 构造一次 guard 后依次运行普通与 race 测试的参数。
func backendTestWithGuardAndRaceArgs(plan gatePlan) ([]string, error) {
	racePackages := affectedRacePackagesForPlan(plan)
	if len(racePackages) == 0 || len(plan.AffectedGoPackages) == 0 {
		return nil, errors.New("combined backend race gate requires normal and race packages")
	}
	args := append([]string{"./scripts/test_with_guard.sh", "--with-race"}, racePackages...)
	args = append(args, "--")
	args = append(args, plan.AffectedGoPackages...)
	return append(args, "-count=1"), nil
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
