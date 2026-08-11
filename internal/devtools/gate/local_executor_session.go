package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// LocalExecutorSession prepares one dependency/cache overlay for a local batch.
// Source restoration remains owned by LocalMaterializedTree between workloads.
type LocalExecutorSession struct {
	mu            sync.Mutex
	sourceRoot    string
	receipt       LocalExecutorSessionReceipt
	layout        executorLayout
	cleanup       func() error
	programs      map[GateID]ExecutorProgram
	dependencies  LocalExecutorDependencyInputs
	steps         map[GateID][]resolvedStep
	searchPath    string
	goRoot        string
	sandboxPath   string
	sandboxPolicy string
	cgoEnabled    string
	nowFunc       func() time.Time
	closed        bool
}

// NewLocalExecutorSessionWithReceipt 消费 scheduler PASS lookup 使用的同一预检回执，
// 并要求调用边界显式注入时钟；不接受调用方提供的摘要字符串。
func NewLocalExecutorSessionWithReceipt(sourceRoot string, nowFunc func() time.Time, workloadIDs []GateID, dependencies LocalExecutorDependencyInputs, receipt LocalExecutorSessionReceipt) (*LocalExecutorSession, error) {
	if nowFunc == nil {
		return nil, errors.New("local executor session clock is required")
	}
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return nil, err
	}
	if len(workloadIDs) == 0 {
		return nil, errors.New("local executor session requires workload IDs")
	}
	for _, id := range workloadIDs {
		if _, err := receipt.EnvironmentFor(id); err != nil {
			return nil, fmt.Errorf("local executor session workload %q is absent from receipt: %w", id, err)
		}
	}
	if err := receipt.Reverify(sourceRoot); err != nil {
		return nil, fmt.Errorf("reverify local executor session receipt: %w", err)
	}
	return prepareLocalExecutorSessionWithReceipt(sourceRoot, nowFunc, workloadIDs, dependencies, receipt)
}

// prepareLocalExecutorSessionWithReceipt 组装本批次的共享工作区、依赖、工具链和冻结步骤。
func prepareLocalExecutorSessionWithReceipt(sourceRoot string, nowFunc func() time.Time, workloadIDs []GateID, dependencies LocalExecutorDependencyInputs, receipt LocalExecutorSessionReceipt) (*LocalExecutorSession, error) {
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return nil, err
	}
	programs, err := resolveLocalExecutorPrograms(workloadIDs)
	if err != nil {
		return nil, err
	}
	sandboxPath, err := localNetworkSandboxPath()
	if err != nil {
		return nil, err
	}
	trustedGo, trustedSelf, err := receiptTrustedExecutionBinaries(receipt)
	if err != nil {
		return nil, err
	}
	boundDependencies, snapshotCleanup, err := localExecutorSessionDependencySnapshot(receipt, dependencies)
	if err != nil {
		return nil, err
	}
	layout, layoutCleanup, dependencyCleanup, goRoot, goBin, err := prepareLocalExecutorSessionWorkspace(sourceRoot, programs, boundDependencies, trustedGo)
	if err != nil {
		return nil, errors.Join(err, snapshotCleanup())
	}
	cleanup := func() error { return errors.Join(dependencyCleanup(), layoutCleanup(), snapshotCleanup()) }
	searchPath := localExecutorSearchPath(goBin)
	if verified, ok := receipt.(*localExecutorSessionReceipt); ok && verified.toolPath != "" {
		searchPath = localExecutorSearchPathWithReceiptGo(goBin, verified.toolPath)
	}
	steps, cgoEnabled, profile, err := freezeLocalExecutorSession(sourceRoot, layout, searchPath, goRoot, programs, boundDependencies, trustedGo, trustedSelf)
	if err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return &LocalExecutorSession{
		sourceRoot: sourceRoot, receipt: receipt, layout: layout, cleanup: cleanup, programs: programs, dependencies: boundDependencies,
		steps: steps, searchPath: searchPath, goRoot: goRoot, sandboxPath: sandboxPath, nowFunc: nowFunc,
		sandboxPolicy: profile, cgoEnabled: cgoEnabled,
	}, nil
}

func prepareLocalExecutorSessionWorkspace(sourceRoot string, programs map[GateID]ExecutorProgram, dependencies LocalExecutorDependencyInputs, trustedGo TrustedGoBinary) (executorLayout, func() error, func() error, string, string, error) {
	layout, layoutCleanup, err := newLocalExecutorLayout()
	if err != nil {
		return executorLayout{}, nil, nil, "", "", err
	}
	fail := func(cause error) (executorLayout, func() error, func() error, string, string, error) {
		return executorLayout{}, nil, nil, "", "", errors.Join(cause, layoutCleanup())
	}
	if err := makeLocalExecutorDirectories(layout); err != nil {
		return fail(err)
	}
	dependencyCleanup, err := installLocalExecutorDependencies(sourceRoot, layout, unionLocalExecutorProgram(programs), dependencies)
	if err != nil {
		return fail(err)
	}
	goRoot, goBin, err := localExecutorToolchain(trustedGo)
	if err != nil {
		return executorLayout{}, nil, nil, "", "", errors.Join(err, dependencyCleanup(), layoutCleanup())
	}
	return layout, layoutCleanup, dependencyCleanup, goRoot, goBin, nil
}

// freezeLocalExecutorSession 解析每项 canonical argv，并校验本机 CGO 与 sandbox 策略。
func freezeLocalExecutorSession(sourceRoot string, layout executorLayout, searchPath, goRoot string, programs map[GateID]ExecutorProgram, dependencies LocalExecutorDependencyInputs, trustedGo TrustedGoBinary, trustedSelf TrustedSelfBinary) (map[GateID][]resolvedStep, string, string, error) {
	steps := make(map[GateID][]resolvedStep, len(programs))
	for id, program := range programs {
		resolved, err := prepareLocalExecutorStepsWithSelf(searchPath, sourceRoot, program, trustedSelf)
		if err != nil {
			return nil, "", "", err
		}
		steps[id] = resolved
	}
	cgoEnabled, err := localExecutorCGOEnabled(trustedGo)
	if err != nil {
		return nil, "", "", err
	}
	if strings.TrimSpace(dependencies.CGOEnabled) != "" && dependencies.CGOEnabled != cgoEnabled {
		return nil, "", "", errors.New("local executor dependency CGO_ENABLED drifted from go env")
	}
	profile, err := localSandboxProfile(sourceRoot, layout, dependencies, goRoot, sessionSandboxToolPaths(steps, programs))
	if err != nil {
		return nil, "", "", err
	}
	return steps, cgoEnabled, profile, nil
}

func sessionSandboxToolPaths(steps map[GateID][]resolvedStep, programs map[GateID]ExecutorProgram) []string {
	paths := make([]string, 0)
	for id, workloadSteps := range steps {
		paths = append(paths, sandboxToolPaths(workloadSteps, programs[id])...)
	}
	return paths
}

// Execute 在共享 session 中执行一个已冻结 workload，执行前只重新挂载轻量覆盖层。
func (session *LocalExecutorSession) Execute(ctx context.Context, id GateID) (PlanGateExecution, error) {
	if session == nil {
		return PlanGateExecution{}, errors.New("local executor session is nil")
	}
	if ctx == nil {
		return PlanGateExecution{}, errors.New("local executor session context is required")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return PlanGateExecution{}, errors.New("local executor session is closed")
	}
	if session.receipt == nil {
		return PlanGateExecution{}, errors.New("local executor session verified receipt is missing")
	}
	if err := session.receipt.Reverify(session.sourceRoot); err != nil {
		return PlanGateExecution{}, fmt.Errorf("local executor session receipt drifted: %w", err)
	}
	program, ok := session.programs[id]
	if !ok {
		return PlanGateExecution{}, fmt.Errorf("local executor session workload %q was not frozen", id)
	}
	if err := reattachLocalExecutorSessionDependencies(session.sourceRoot, session.layout, program, session.dependencies); err != nil {
		return PlanGateExecution{}, err
	}
	if err := reverifyLocalExecutorSessionPrivateOverlay(session.layout, program); err != nil {
		return PlanGateExecution{}, err
	}
	environment, err := localExecutorEnvironment(session.layout, session.searchPath, session.goRoot, session.cgoEnabled)
	if err != nil {
		return PlanGateExecution{}, err
	}
	observation := runLocalExecutorSteps(ctx, session.nowFunc, id, session.steps[id], environment, session.sandboxPath, session.sandboxPolicy)
	return finishLocalGateExecution(id, program, observation, ctx.Err())
}

// reverifyLocalExecutorSessionPrivateOverlay checks only the batch-private
// mountpoints after Restore. It intentionally does not walk or hash shared
// dependency trees: those expensive proofs belong to receipt construction.
func reverifyLocalExecutorSessionPrivateOverlay(layout executorLayout, program ExecutorProgram) error {
	roots := []string{layout.runRoot, layout.home, layout.tmp, layout.goCache, layout.xdgCache}
	if program.NeedsGoSeed {
		roots = append(roots, layout.goModCache)
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("local executor private overlay %q is unavailable: %w", root, err)
		}
	}
	return nil
}

// Close 严格清理本批次依赖覆盖层和临时工作区，重复关闭直接失败。
func (session *LocalExecutorSession) Close() error {
	if session == nil {
		return errors.New("local executor session is nil")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return errors.New("local executor session already closed")
	}
	session.closed = true
	return session.cleanup()
}

// reattachLocalExecutorSessionDependencies 在 exact-tree restore 后只恢复前端轻量覆盖层。
func reattachLocalExecutorSessionDependencies(sourceRoot string, layout executorLayout, program ExecutorProgram, dependencies LocalExecutorDependencyInputs) error {
	cleanupTargets := make([]string, 0, 2)
	if program.NeedsFrontendSeed {
		target := filepath.Join(sourceRoot, "frontend-app", "node_modules")
		if err := localExecutorOverlayTargetAvailable(target); err != nil {
			return err
		}
		if err := resetLocalExecutorFrontendViteCache(layout); err != nil {
			return err
		}
		cleanupTargets = append(cleanupTargets, target)
		if err := installLocalFrontendDependencies(sourceRoot, layout, dependencies); err != nil {
			return errors.Join(fmt.Errorf("reattach local frontend dependencies: %w", err), cleanupLocalExecutorOverlayTargets(cleanupTargets))
		}
	}
	if program.NeedsFrontendEmbedSeed {
		target := filepath.Join(sourceRoot, "cmd", "agent-terminal", "web-dist")
		if err := localExecutorOverlayTargetAvailable(target); err != nil {
			return errors.Join(cleanupLocalExecutorOverlayTargets(cleanupTargets), err)
		}
		cleanupTargets = append(cleanupTargets, target)
		if err := installLocalFrontendEmbedDependency(sourceRoot, dependencies.FrontendEmbedRoot); err != nil {
			return errors.Join(fmt.Errorf("reattach local frontend embed seed: %w", err), cleanupLocalExecutorOverlayTargets(cleanupTargets))
		}
	}
	return nil
}

func resetLocalExecutorFrontendViteCache(layout executorLayout) error {
	return removeExecutorWorkspacePath(filepath.Join(layout.tmp, ".vite-temp"))
}

func cleanupLocalExecutorOverlayTargets(targets []string) error {
	var cleanupErr error
	for _, target := range slices.Backward(targets) {
		if err := os.RemoveAll(target); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleanup local executor overlay %q: %w", target, err))
		}
	}
	return cleanupErr
}

// resolveLocalExecutorReceiptPrograms validates canonical program semantics for
// receipt identity only. Local execution eligibility stays owned by the strict
// session/executor boundary so mapped remote-only workloads can still reuse a
// proven local PASS identity without becoming locally executable.
func resolveLocalExecutorReceiptPrograms(workloadIDs []GateID) (map[GateID]ExecutorProgram, error) {
	programs := make(map[GateID]ExecutorProgram, len(workloadIDs))
	for _, id := range workloadIDs {
		if _, duplicate := programs[id]; duplicate {
			return nil, fmt.Errorf("local executor receipt workload %q is duplicated", id)
		}
		_, program, err := executorProgramForWorkload(id)
		if err != nil {
			return nil, err
		}
		if err := validateExecutorProgram(program); err != nil {
			return nil, fmt.Errorf("local executor receipt workload %q program is invalid: %w", id, err)
		}
		programs[id] = program
	}
	return programs, nil
}

func resolveLocalExecutorPrograms(workloadIDs []GateID) (map[GateID]ExecutorProgram, error) {
	programs, err := resolveLocalExecutorReceiptPrograms(workloadIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range workloadIDs {
		if err := validateLocalExecutorProgramSupport(programs[id]); err != nil {
			return nil, fmt.Errorf("local executor workload %q: %w", id, err)
		}
	}
	return programs, nil
}

func unionLocalExecutorProgram(programs map[GateID]ExecutorProgram) ExecutorProgram {
	var union ExecutorProgram
	for _, program := range programs {
		union.NeedsGoSeed = union.NeedsGoSeed || program.NeedsGoSeed
		union.NeedsFrontendSeed = union.NeedsFrontendSeed || program.NeedsFrontendSeed
		union.NeedsFrontendEmbedSeed = union.NeedsFrontendEmbedSeed || program.NeedsFrontendEmbedSeed
	}
	return union
}

func localExecutorCGOEnabled(trustedGo TrustedGoBinary) (string, error) {
	goBinary, err := localExecutorGoBinary(trustedGo)
	if err != nil {
		return "", err
	}
	return localGoEnvValue(goBinary, "CGO_ENABLED")
}

func localExecutorGoBinary(trustedGo TrustedGoBinary) (string, error) {
	path, err := trustedGo.VerifiedPath()
	if err != nil {
		return "", fmt.Errorf("reverify receipt-bound local Go binary: %w", err)
	}
	return path, nil
}

func localGoEnvValue(goBinary, key string) (string, error) {
	command := exec.Command(goBinary, "env", key)
	outputBytes, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("read local go env %s: %w", key, err)
	}
	value := strings.TrimSpace(string(outputBytes))
	if value == "" {
		return "", fmt.Errorf("local go env %s is empty", key)
	}
	return value, nil
}
