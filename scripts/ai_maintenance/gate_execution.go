package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

type gateRunner struct {
	run func() error
}

// runGateWithTiming 保留每个普通 gate 的可审计耗时输出，不缓存或复用本地结果。
func runGateWithTiming(gate string, run func() error) error {
	started := time.Now()
	err := run()
	fmt.Printf("[ai-maintenance] gate=%s duration=%s\n", gate, time.Since(started).Round(time.Millisecond))
	return err
}

// executeGatePlan 严格按计划执行命令；必需 gate 缺少 runner 时立即失败。
func executeGatePlan(plan gatePlan) error {
	runners := gateRunners(plan)
	for _, gate := range plan.RequiredGates {
		runner, ok := runners[gate]
		if !ok {
			return fmt.Errorf("required gate %q has no runner", gate)
		}
		if err := runGateWithTiming(gate, runner.run); err != nil {
			return err
		}
	}
	return nil
}

// gateRunners 根据 gate 计划构造可执行命令表。
func gateRunners(plan gatePlan) map[string]gateRunner {
	generatedCheck := func(name string, args ...string) gateRunner {
		return gateRunner{run: func() error {
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
			args, err := backendTestWithGuardArgs(plan)
			if err != nil {
				return err
			}
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
		"backend:archtest": {run: func() error {
			return runCommand("", "./scripts/test_with_guard.sh", "--archtest-only")
		}},
		"capcontract:check":   generatedCheck("make", "capcontract-check"),
		"turncontract:verify": {run: runTurnContractVerifyGate},
		"frontend:static-guards": {run: func() error {
			dir, name, args := frontendStaticGuardCommand()
			return runCommand(dir, name, args...)
		}},
		"frontend:lint":                {run: func() error { return runCommand("frontend-app", "npm", "run", "lint") }},
		"frontend:typecheck-contracts": {run: func() error { return runCommand("frontend-app", "npm", "run", "typecheck:contracts") }},
		"frontend:changed-tests":       {run: func() error { return runFrontendChangedTests(plan) }},
		"frontend:embed-verify":        {run: runFrontendEmbedVerify},
		"codemap:check":                generatedCheck("make", "codemap-check"),
		"project-map:check":            generatedCheck("make", "project-map-check", "PROJECT_MAP_ARGS="),
		"sqlc:verify":                  {run: func() error { return runCommand("", "make", "sqlc-verify-worktree") }},
		"diff:whitespace":              {run: runWhitespaceCheck},
	}
	maps.Copy(runners, ownedGateRunners())
	return runners
}

// runFrontendEmbedVerify 通过隔离 runner 执行嵌入验证。
func runFrontendEmbedVerify() error {
	return runCommand("frontend-app", "npm", "run", "verify:embed:isolated")
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

// runFrontendChangedTests 执行与变更前端源码同名或直接变更的 Vitest 文件，避免局部维护退化为整套 npm test。
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
	if len(packages) == 0 {
		if requiresFullArchtest(plan.ChangedFiles) {
			return []string{"./scripts/test_with_guard.sh", "--guard-only"}, nil
		}
		return nil, errors.New("backend gate resolved no Go packages")
	}
	if !requiresBroadBackendRegression(plan.ChangedFiles) {
		packages, err = resolveDirectReverseDependentPackages(packages)
		if err != nil {
			return nil, err
		}
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

// runWhitespaceCheck 检查工作树相对于 HEAD 的空白错误。
func runWhitespaceCheck() error {
	return runCommand("", "git", "diff", "--check")
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
