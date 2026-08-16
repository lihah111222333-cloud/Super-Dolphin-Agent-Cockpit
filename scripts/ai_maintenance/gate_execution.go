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

type gateRunnerFactory func(gatePlan) map[string]gateRunner

type reviewObjectMaterializer func(string, reviewObject) (string, func() error, error)

type preparedReviewExecution struct {
	repoRoot      string
	plan          gatePlan
	object        reviewObject
	changedFiles  []string
	executionRoot string
	cleanup       func() error
	closed        bool
	cleanupErr    error
}

// prepareGatePlanForReviewObject 只物化一次 exact candidate，并把计划、身份和 cleanup 绑定到同一 session。
func prepareGatePlanForReviewObject(repoRoot string, object reviewObject, policy gatePlanPolicy, materializer reviewObjectMaterializer) (*preparedReviewExecution, error) {
	if materializer == nil {
		return nil, errors.New("review object materializer is required")
	}
	files, err := changedFilesFromReviewObject(repoRoot, object)
	if err != nil {
		return nil, err
	}
	executionRoot, cleanup, err := materializer(repoRoot, object)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(executionRoot) == "" {
		if cleanup == nil {
			return nil, errors.New("review object materializer returned empty execution root")
		}
		return nil, errors.Join(errors.New("review object materializer returned empty execution root"), cleanup())
	}
	if cleanup == nil {
		return nil, errors.New("review object materializer returned missing cleanup")
	}
	plan, err := buildGatePlanForRepoAndObject(executionRoot, files, object, policy)
	if err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return &preparedReviewExecution{
		repoRoot: repoRoot, plan: plan, object: object,
		changedFiles: append([]string(nil), files...), executionRoot: executionRoot, cleanup: cleanup,
	}, nil
}

// close 以幂等边界释放 session 的 exact candidate worktree。
func (session *preparedReviewExecution) close() error {
	if session == nil {
		return errors.New("prepared review execution session is required")
	}
	if session.closed {
		return session.cleanupErr
	}
	session.closed = true
	if session.cleanup == nil {
		session.cleanupErr = errors.New("prepared review execution cleanup is required")
		return session.cleanupErr
	}
	session.cleanupErr = session.cleanup()
	return session.cleanupErr
}

// executePreparedGatePlanWithRunnerFactory 复用 prepared session；session 关闭后不可跨 candidate 重放。
func executePreparedGatePlanWithRunnerFactory(session *preparedReviewExecution, factory gateRunnerFactory) (resultErr error) {
	if session == nil {
		return errors.New("prepared review execution session is required")
	}
	if session.closed {
		return errors.New("prepared review execution session is already closed")
	}
	defer func() { resultErr = errors.Join(resultErr, session.close()) }()
	if session.plan.ReviewObject != session.object || !sameStringSet(session.plan.ChangedFiles, session.changedFiles) {
		return errors.New("prepared review execution identity drifted")
	}
	if err := session.plan.ReviewObject.validateExecutable(); err != nil {
		return err
	}
	plan := session.plan
	plan.executionRoot = session.executionRoot
	dependencyCleanup, err := prepareReviewExecutionDependencies(session.repoRoot, session.executionRoot, plan)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, dependencyCleanup()) }()
	return executeMaterializedGatePlan(plan, factory)
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
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return fmt.Errorf("resolve review execution repository: %w", err)
	}
	return executeGatePlanWithRunnerFactory(repoRoot, plan, gateRunners)
}

// executeGatePlanWithRunnerFactory 复核 exact object 后，在一次隔离物化中绑定依赖并执行 runner。
func executeGatePlanWithRunnerFactory(repoRoot string, plan gatePlan, factory gateRunnerFactory) error {
	if err := plan.ReviewObject.validateExecutable(); err != nil {
		return err
	}
	files, err := changedFilesFromReviewObject(repoRoot, plan.ReviewObject)
	if err != nil {
		return err
	}
	if !sameStringSet(files, plan.ChangedFiles) {
		return errors.New("gate plan changed-files drifted from exact review object")
	}
	materializedRoot, cleanup, err := materializeReviewObject(repoRoot, plan.ReviewObject)
	if err != nil {
		return err
	}
	session := &preparedReviewExecution{
		repoRoot: repoRoot, plan: plan, object: plan.ReviewObject,
		changedFiles: append([]string(nil), files...), executionRoot: materializedRoot, cleanup: cleanup,
	}
	return executePreparedGatePlanWithRunnerFactory(session, factory)
}

// executeMaterializedGatePlan 仅在 exact candidate root 已绑定后构造并顺序执行 runner。
func executeMaterializedGatePlan(plan gatePlan, factory gateRunnerFactory) error {
	if strings.TrimSpace(plan.executionRoot) == "" {
		return errors.New("exact candidate execution root is required before runner construction")
	}
	if factory == nil {
		return errors.New("gate runner factory is required")
	}
	runners := factory(plan)
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
			return runCommand(gateExecutionDir(plan, ""), name, args...)
		}}
	}
	runners := map[string]gateRunner{
		"ai-maintenance:self-test": {run: func() error {
			if err := runCommand(gateExecutionDir(plan, ""), "go", "test", "./scripts/ai_maintenance", "-count=1"); err != nil {
				return err
			}
			return runCommand(gateExecutionDir(plan, ""), "go", "test", "./scripts", "-run", "TestAIMaintenanceGate", "-count=1")
		}},
		"backend:test_with_guard": {run: func() error {
			args, err := backendTestWithGuardArgs(plan)
			if err != nil {
				return err
			}
			return runCommand(gateExecutionDir(plan, ""), args[0], args[1:]...)
		}},
		"lsp:changed-diagnostics": {run: func() error {
			files, deleted, err := existingDiagnosticFilesInRoot(gateExecutionDir(plan, ""), plan.DiagnosticFiles)
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
			return runCommand(gateExecutionDir(plan, ""), "go", args...)
		}},
		"backend:archtest": {run: func() error {
			return runCommand(gateExecutionDir(plan, ""), "./scripts/test_with_guard.sh", "--archtest-only")
		}},
		"capcontract:check":   generatedCheck("make", "capcontract-check"),
		"turncontract:verify": {run: func() error { return runTurnContractVerifyGate(plan) }},
		"frontend:static-guards": {run: func() error {
			dir, name, args := frontendStaticGuardCommand()
			return runCommand(gateExecutionDir(plan, dir), name, args...)
		}},
		"frontend:lint": {run: func() error { return runCommand(gateExecutionDir(plan, "frontend-app"), "npm", "run", "lint") }},
		"frontend:typecheck-contracts": {run: func() error {
			return runCommand(gateExecutionDir(plan, "frontend-app"), "npm", "run", "typecheck:contracts")
		}},
		"frontend:changed-tests": {run: func() error { return runFrontendChangedTests(plan) }},
		"frontend:embed-verify":  {run: func() error { return runFrontendEmbedVerify(plan) }},
		"codemap:check":          generatedCheck("make", "codemap-check"),
		"project-map:check":      generatedCheck("make", "project-map-check", "PROJECT_MAP_ARGS="),
		"sqlc:verify":            {run: func() error { return runCommand(gateExecutionDir(plan, ""), "make", "sqlc-verify-worktree") }},
		"diff:whitespace":        {run: func() error { return runWhitespaceCheck(plan) }},
	}
	maps.Copy(runners, ownedGateRunners(plan))
	return runners
}

// runFrontendEmbedVerify 通过隔离 runner 执行嵌入验证。
func runFrontendEmbedVerify(plan gatePlan) error {
	return runCommand(gateExecutionDir(plan, "frontend-app"), "npm", "run", "verify:embed:isolated")
}

// frontendStaticGuardCommand 返回 hook 与 CI 共用的只读前端静态门禁命令。
func frontendStaticGuardCommand() (string, string, []string) {
	return "frontend-app", "npm", []string{"run", "guard:critical-skip"}
}

func runTurnContractVerifyGate(plan gatePlan) error {
	if err := runCommand(gateExecutionDir(plan, ""), "go", "run", "./scripts/turncontract", "--verify"); err != nil {
		return err
	}
	if err := runCommand(gateExecutionDir(plan, ""), "go", "test", "./internal/dto/turn", "-run", "^TestTurnContractFieldGuard", "-count=1"); err != nil {
		return err
	}
	return runCommand(gateExecutionDir(plan, ""), "node", "frontend-app/scripts/turn-contract-field-guard.mjs")
}

// runFrontendChangedTests 执行与变更前端源码同名或直接变更的 Vitest 文件，避免局部维护退化为整套 npm test。
func runFrontendChangedTests(plan gatePlan) error {
	tests, err := frontendChangedTestFilesInRoot(gateExecutionDir(plan, ""), plan.ChangedFiles)
	if err != nil {
		return err
	}
	if len(tests) == 0 {
		return errors.New("frontend changed-tests gate has no matching test files")
	}
	args := append([]string{"vitest", "run"}, tests...)
	args = append(args, "--no-file-parallelism", "--maxWorkers=1")
	return runCommand(gateExecutionDir(plan, "frontend-app"), "npx", args...)
}

// frontendChangedTestFilesInRoot 仅从指定 candidate root 解析直接或同名配对测试。
func frontendChangedTestFilesInRoot(repoRoot string, files []string) ([]string, error) {
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
			if _, err := os.Stat(filepath.Join(repoRoot, "frontend-app", candidate)); err == nil {
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
	return strings.Contains(base, "benchmark.test.")
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
	return existingDiagnosticFilesInRoot(".", files)
}

// existingDiagnosticFilesInRoot 仅返回 candidate root 中仍存在的诊断目标并记录删除数。
func existingDiagnosticFilesInRoot(repoRoot string, files []string) (existing []string, deleted int, err error) {
	existing = make([]string, 0, len(files))
	for _, file := range files {
		targetPath := file
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(repoRoot, filepath.FromSlash(file))
		}
		info, err := os.Stat(targetPath)
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
	repoRoot := plan.executionRoot
	if repoRoot == "" {
		return nil, errors.New("exact candidate execution root is required for backend regression")
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
		packages, err = resolveDirectReverseDependentPackages(repoRoot, packages)
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

// runWhitespaceCheck 检查与 changed-files 同源的 exact tree pair。
func runWhitespaceCheck(plan gatePlan) error {
	if strings.TrimSpace(plan.executionRoot) == "" {
		return errors.New("exact candidate execution root is required for whitespace check")
	}
	if err := plan.ReviewObject.validateExecutable(); err != nil {
		return err
	}
	return runCommand(gateExecutionDir(plan, ""), "git", "diff", "--no-ext-diff", "--no-renames", "--check",
		plan.ReviewObject.BaseTree, plan.ReviewObject.CandidateTree, "--")
}

func gateExecutionDir(plan gatePlan, relative string) string {
	if plan.executionRoot == "" {
		return relative
	}
	if relative == "" {
		return plan.executionRoot
	}
	return filepath.Join(plan.executionRoot, filepath.FromSlash(relative))
}

func gateCommandEnvironment(_ string) []string {
	return reviewGitEnvironment()
}
