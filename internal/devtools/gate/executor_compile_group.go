package gate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

type compiledGroupArtifact struct {
	group                 CompileGroup
	layout                executorLayout
	environment           []string
	goBinary              string
	binaryPath            string
	packageDir            string
	candidateCacheRoot    string
	baselineCacheSeedRoot string
}

// writeExecutorPlanReportWithCompileGroups 在 worker 输出前绑定 manifest 与完整编译观察。
func writeExecutorPlanReportWithCompileGroups(request executorPlanRequest, report PlanExecutionReport, executionErr error, stdout io.Writer) error {
	report.ExecutionOutcome = WorkerExecutionOutcomeForError(executionErr)
	if err := ValidateCompileGroupExecutions(request.compileGroups, report.CompileGroupExecutions); err != nil {
		return errors.Join(executionErr, fmt.Errorf("compile group execution report: %w", err))
	}
	if err := writePlanExecutionReport(stdout, report); err != nil {
		return errors.Join(executionErr, err)
	}
	return executionErr
}

// executeGatePlanWithCompileGroups 执行 manifest 绑定的 shard；每组只编译一次，
// artifact 不进入全局缓存，并在 selector 执行后随 shard workspace 一起清理。
func executeGatePlanWithCompileGroups(ctx context.Context, request executorPlanRequest) (PlanExecutionReport, error) {
	if err := validateExecutorCompileGroups(request.profile, request.gateIDs, request.compileGroups); err != nil {
		return PlanExecutionReport{}, err
	}
	preparedRuntimeSeeds, err := prepareExecutorPlanRuntimeSeeds(request.gateIDs)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	goBuildCacheRoot, goBuildCacheSeedRoot, err := prepareExecutorPlanGoBuildCache(request.gateIDs)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	defer func() {
		if goBuildCacheRoot != "" {
			_ = removeExecutorWorkspacePath(goBuildCacheRoot)
		}
	}()

	artifacts, executions, compileErr := executeCompileGroups(ctx, request, preparedRuntimeSeeds, goBuildCacheRoot, goBuildCacheSeedRoot, time.Now)
	defer cleanupCompiledGroupArtifacts(artifacts)
	executionCtx, cancelExecution := executorWorkloadContext(ctx)
	defer cancelExecution()
	batchedResults, batchedErrors := executeCompileGroupBatches(executionCtx, request.compileGroups, artifacts, time.Now)
	runGate := func(ctx context.Context, laneIndex int, id GateID) (PlanGateExecution, error) {
		return runCompileGroupGate(ctx, laneIndex, id, batchedResults, artifacts, executions, preparedRuntimeSeeds, goBuildCacheRoot, goBuildCacheSeedRoot)
	}
	report, executionErr := executeGatePlanWithRunner(executionCtx, request, runGate, time.Now)
	mergedGates, mergeErr := replaceBatchedGateResults(request.gateIDs, report.Gates, batchedResults)
	if mergeErr == nil {
		report.Gates = mergedGates
	} else {
		executionErr = errors.Join(executionErr, mergeErr)
	}
	for _, group := range request.compileGroups {
		if batchErr := batchedErrors[group.GroupID]; batchErr != nil {
			executionErr = errors.Join(executionErr, batchErr)
		}
	}
	report.CompileGroupExecutions = executions
	if err := ValidateCompileGroupExecutions(request.compileGroups, report.CompileGroupExecutions); err != nil {
		executionErr = errors.Join(executionErr, fmt.Errorf("compile group execution report: %w", err))
	}
	return report, errors.Join(compileErr, executionErr)
}

func executeCompileGroups(
	ctx context.Context,
	request executorPlanRequest,
	preparedRuntimeSeeds *executorPreparedRuntimeSeeds,
	goBuildCacheRoot string,
	goBuildCacheSeedRoot string,
	now func() time.Time,
) (map[GateID]compiledGroupArtifact, []CompileGroupExecution, error) {
	if err := validateExecutorCompileGroups(request.profile, request.gateIDs, request.compileGroups); err != nil {
		return nil, nil, err
	}
	artifacts := make(map[GateID]compiledGroupArtifact)
	executions := make([]CompileGroupExecution, 0, len(request.compileGroups))
	var allErr error
	for index, group := range request.compileGroups {
		artifact, execution, err := executeCompileGroup(ctx, group, index, preparedRuntimeSeeds, goBuildCacheRoot, goBuildCacheSeedRoot, now)
		executions = append(executions, execution)
		allErr = errors.Join(allErr, err)
		if err != nil {
			continue
		}
		for _, workloadID := range group.WorkloadIDs {
			artifacts[workloadID] = artifact
		}
	}
	return artifacts, executions, allErr
}

// executeCompileGroup 编译一个 group 并返回可供 selector 复用的临时 binary。
func executeCompileGroup(
	ctx context.Context,
	group CompileGroup,
	groupIndex int,
	preparedRuntimeSeeds *executorPreparedRuntimeSeeds,
	goBuildCacheRoot string,
	goBuildCacheSeedRoot string,
	now func() time.Time,
) (compiledGroupArtifact, CompileGroupExecution, error) {
	artifactKey, err := CompileArtifactKey(group)
	if err != nil {
		return compiledGroupArtifact{}, failedCompileGroupExecution(group, artifactKey, err), err
	}
	execution := newCompileGroupExecution(group, artifactKey)
	layout, environment, goBinary, packageDir, log, metricsPath, err := prepareCompileGroupExecution(ctx, group, groupIndex, preparedRuntimeSeeds, goBuildCacheRoot, goBuildCacheSeedRoot, now)
	if err != nil {
		return compiledGroupArtifact{}, finishFailedCompileGroupExecution(execution, time.Time{}, time.Time{}, err), err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = cleanupExecutorWorkspace(layout)
		}
	}()
	binaryPath := compileGroupTestBinaryPath(layout.runRoot)
	argv := compileGroupCommandArgv(group, binaryPath)
	started, completed, compileErr := runCompileGroupCommand(ctx, goBinary, argv, layout.sourceCopy, environment, now, log)
	if !started.IsZero() {
		setCompileGroupExecutionTiming(&execution, started, completed)
		execution.CompileCommandDigest = digestCommandArgv(argv)
	}
	if !started.IsZero() {
		compileErr = recordCompileGroupArtifact(&execution, compileErr, binaryPath, goBuildCacheRoot, metricsPath, goBuildCacheSeedRoot)
	}
	if compileErr != nil {
		if diagnostic := strings.TrimSpace(string(log.Bytes())); diagnostic != "" {
			compileErr = errors.Join(compileErr, errors.New(diagnostic))
		}
		finished := finishFailedCompileGroupExecution(execution, started, completed, compileErr)
		return compiledGroupArtifact{}, finished, compileErr
	}
	execution.Status, execution.ExitCode = ResultStatusPassed, 0
	if err := execution.Validate(); err != nil {
		return compiledGroupArtifact{}, finishFailedCompileGroupExecution(execution, started, completed, err), err
	}
	cleanup = false
	return compiledGroupArtifact{group: group, layout: layout, environment: environment, goBinary: goBinary, binaryPath: binaryPath, packageDir: packageDir, candidateCacheRoot: goBuildCacheRoot, baselineCacheSeedRoot: goBuildCacheSeedRoot}, execution, nil
}

// compileGroupTestBinaryPath 保留 Go 测试进程的 .test 身份，供运行时测试授权严格识别。
func compileGroupTestBinaryPath(runRoot string) string {
	return filepath.Join(runRoot, "test-binary.test")
}

// compileGroupPackageDirectory 校验 go list 解析的包目录位于受信源码快照内。
func compileGroupPackageDirectory(sourceRoot, listedPackageDir string) (string, error) {
	root, err := trustedDirectory(sourceRoot, false, -1)
	if err != nil {
		return "", err
	}
	packageDir, err := trustedDirectory(listedPackageDir, false, -1)
	if err != nil {
		return "", err
	}
	if packageDir != root && !pathContains(root, packageDir) {
		return "", errors.New("compile group package directory escapes source snapshot")
	}
	return packageDir, nil
}

// prepareCompileGroupExecution 准备单个 compile group 的隔离 workspace 和预检。
func prepareCompileGroupExecution(ctx context.Context, group CompileGroup, groupIndex int, prepared *executorPreparedRuntimeSeeds, cacheRoot, seedRoot string, now func() time.Time) (executorLayout, []string, string, string, *boundedPlanLog, string, error) {
	_, program, err := executorProgramForWorkload(group.WorkloadIDs[0])
	if err != nil {
		return executorLayout{}, nil, "", "", nil, "", err
	}
	if !program.NeedsGoSeed {
		return executorLayout{}, nil, "", "", nil, "", errors.New("compile group workload does not require Go test execution")
	}
	config, log, metricsPath, err := newCompileGroupConfig(group, groupIndex, prepared, cacheRoot, seedRoot, now)
	if err != nil {
		return executorLayout{}, nil, "", "", nil, "", err
	}
	layout, err := prepareExecutorWorkspace(config)
	if err != nil {
		return executorLayout{}, nil, "", "", nil, "", err
	}
	environment, goBinary, packageDir, err := prepareCompileGroupProgram(ctx, config, layout, program, group.PackageTarget, log)
	if err != nil {
		_ = cleanupExecutorWorkspace(layout)
		return executorLayout{}, nil, "", "", nil, "", err
	}
	return layout, environment, goBinary, packageDir, log, metricsPath, nil
}

func newCompileGroupExecution(group CompileGroup, artifactKey string) CompileGroupExecution {
	return CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: group.GroupID, ArtifactKey: artifactKey, PackageTarget: group.PackageTarget,
		WorkloadIDs: CompileGroupWorkloadIDs(group), Status: ResultStatusFailed,
		ExitCode: 1, ProfileDigest: group.ProfileDigest, ResourceClassID: group.ResourceClassID,
	}
}

func newCompileGroupConfig(group CompileGroup, groupIndex int, prepared *executorPreparedRuntimeSeeds, cacheRoot, seedRoot string, now func() time.Time) (executorConfig, *boundedPlanLog, string, error) {
	cacheProxy, err := executorGoBuildCacheProxyLauncher()
	if err != nil {
		return executorConfig{}, nil, "", err
	}
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "compile-group-"+strings.TrimPrefix(group.GroupID, "sha256:"))
	if err != nil {
		return executorConfig{}, nil, "", err
	}
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	workRoot, err := prepareCompileGroupWorkRoot(ExecutorWorkRoot, groupIndex, group.GroupID)
	if err != nil {
		return executorConfig{}, nil, "", err
	}
	config := executorConfig{
		sourcePath: ExecutorSourcePath, workRoot: workRoot, searchPath: executorSearchPath,
		expectedUID: cicontract.RemoteWorkerUID, requireReadOnlySource: true,
		runtimeSeedRoot: ExecutorRuntimeSeedRoot, runtimeSeedManifest: ExecutorRuntimeSeedManifestPath,
		goRoot: ExecutorGoRoot, preparedRuntimeSeeds: prepared,
		goBuildCacheSeedRoot: seedRoot, goBuildCacheRoot: cacheRoot,
		goBuildCacheProxy: cacheProxy, goBuildCacheMetricsPath: metricsPath,
		frontendEmbedSeedRoot: ExecutorFrontendEmbedSeedRoot, stdout: log, stderr: log,
		nowFunc: now, executionTiming: &executorExecutionTiming{},
	}
	return config, log, metricsPath, nil
}

// prepareCompileGroupWorkRoot 创建编译组专属空目录，随后仍由统一工作区守卫复核真实路径、所有权和空目录约束。
func prepareCompileGroupWorkRoot(baseRoot string, groupIndex int, groupID string) (string, error) {
	workRoot := filepath.Join(baseRoot, "compile-groups", fmt.Sprintf("group-%06d-%s", groupIndex, strings.TrimPrefix(groupID, "sha256:")))
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return "", fmt.Errorf("create compile group work root: %w", err)
	}
	return workRoot, nil
}

func prepareCompileGroupProgram(ctx context.Context, config executorConfig, layout executorLayout, program ExecutorProgram, packageTarget string, log io.Writer) ([]string, string, string, error) {
	environment, _, _, err := prepareExecutorRun(ctx, config, layout, program)
	if err != nil {
		return nil, "", "", err
	}
	goBinary, err := resolveExecutable("go", config.searchPath)
	if err != nil {
		return nil, "", "", err
	}
	packageDir, err := runCompileGroupPreflight(ctx, goBinary, packageTarget, layout.sourceCopy, environment, log)
	if err != nil {
		return nil, "", "", err
	}
	return environment, goBinary, packageDir, nil
}

// runCompileGroupPreflight 对组执行一次 go list，返回真实包目录并按包范围执行 copylocks。
func runCompileGroupPreflight(ctx context.Context, goBinary, packageTarget, directory string, environment []string, log io.Writer) (string, error) {
	var listed bytes.Buffer
	if err := runCompileGroupToolWithStreams(ctx, goBinary, []string{"go", "list", "-f", "{{.Dir}}", packageTarget}, directory, environment, io.MultiWriter(log, &listed), log); err != nil {
		return "", fmt.Errorf("compile group go list: %w", err)
	}
	listedPackageDir := strings.TrimSpace(listed.String())
	if listedPackageDir == "" {
		return "", errors.New("compile group go list resolved no package directory")
	}
	if strings.Contains(listedPackageDir, "\n") {
		return "", errors.New("compile group go list resolved more than one package directory")
	}
	packageDir, err := compileGroupPackageDirectory(directory, listedPackageDir)
	if err != nil {
		return "", err
	}
	if copylocksPackageTarget(packageTarget) {
		if err := runCompileGroupTool(ctx, goBinary, []string{"go", "vet", "-copylocks", packageTarget}, directory, environment, log); err != nil {
			return "", fmt.Errorf("compile group copylocks guard: %w", err)
		}
	}
	return packageDir, nil
}

func copylocksPackageTarget(packageTarget string) bool {
	for _, prefix := range []string{"./internal/provider", "./internal/platform", "./internal/module/thread"} {
		if packageTarget == prefix || strings.HasPrefix(packageTarget, prefix+"/") {
			return true
		}
	}
	return false
}

func runCompileGroupTool(ctx context.Context, binary string, argv []string, directory string, environment []string, output io.Writer) error {
	return runCompileGroupToolWithStreams(ctx, binary, argv, directory, environment, output, output)
}

// runCompileGroupToolWithStreams 分离 stdout 与 stderr，避免诊断文本污染机器可解析输出。
func runCompileGroupToolWithStreams(ctx context.Context, binary string, argv []string, directory string, environment []string, stdout, stderr io.Writer) error {
	if environment == nil {
		return errors.New("compile group tool environment is required")
	}
	command := exec.CommandContext(ctx, binary, argv[1:]...)
	configureCommandCancellation(command)
	command.Args[0], command.Dir, command.Env = argv[0], directory, environment
	command.Stdout, command.Stderr = synchronizedCommandOutputWriters(stdout, stderr)
	return runConfiguredCommand(command)
}

func compileGroupCommandArgv(group CompileGroup, binaryPath string) []string {
	argv := []string{"go", "test", "-c", "-o", binaryPath}
	if groupUsesRace(group) {
		argv = append(argv, "-race")
	}
	return append(argv, group.PackageTarget)
}

func runCompileGroupCommand(ctx context.Context, goBinary string, argv []string, directory string, environment []string, now func() time.Time, log io.Writer) (time.Time, time.Time, error) {
	if environment == nil {
		return time.Time{}, time.Time{}, errors.New("compile group command environment is required")
	}
	command := exec.CommandContext(ctx, goBinary, argv[1:]...)
	configureCommandCancellation(command)
	command.Args[0] = argv[0]
	command.Dir, command.Env = directory, environment
	command.Stdout, command.Stderr = synchronizedCommandOutputWriters(log, log)
	var started time.Time
	runErr := runConfiguredCommandWithStart(command, func() {
		started = now().UTC()
	})
	if started.IsZero() {
		return time.Time{}, time.Time{}, runErr
	}
	return started, now().UTC(), runErr
}

// synchronizedCommandOutputWriters 为 stdout/stderr 的并发复制共享同一串行写入边界，保护共用目标。
// os/exec 会并发排空两条管道；调用方可以把同一个诊断 writer 同时传给两路。
func synchronizedCommandOutputWriters(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	mu := &sync.Mutex{}
	return newSynchronizedCommandOutputWriter(mu, stdout), newSynchronizedCommandOutputWriter(mu, stderr)
}

type synchronizedCommandOutputWriter struct {
	mu          *sync.Mutex
	destination io.Writer
}

func newSynchronizedCommandOutputWriter(mu *sync.Mutex, destination io.Writer) io.Writer {
	if destination == nil {
		return nil
	}
	return synchronizedCommandOutputWriter{mu: mu, destination: destination}
}

// Write 在单次 os/exec 输出回调内持锁写入底层目标，避免 stdout/stderr 竞争。
func (writer synchronizedCommandOutputWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.destination.Write(value)
}

// recordCompileGroupArtifact 校验编译文件并读取本次私有缓存计数。
func recordCompileGroupArtifact(execution *CompileGroupExecution, compileErr error, binaryPath, cacheRoot, metricsPath, seedRoot string) error {
	if compileErr == nil {
		info, statErr := os.Stat(binaryPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			compileErr = statErr
			if compileErr == nil {
				compileErr = errors.New("compiled test binary is empty or not regular")
			}
		} else {
			execution.ArtifactSize = info.Size()
			execution.ArtifactSHA256, compileErr = fileSHA256(binaryPath)
		}
	}
	metrics, metricsErr := LoadGoBuildCacheProxyMetricsAt(cacheRoot, metricsPath, seedRoot)
	if metricsErr != nil {
		return errors.Join(compileErr, metricsErr)
	}
	execution.CacheHits = metrics.PrivateHitCount + metrics.BaselineHitCount
	execution.CacheMisses, execution.CachePuts = metrics.MissCount, metrics.PutCount
	return compileErr
}

func executeCompiledSelector(ctx context.Context, laneIndex int, id GateID, artifact compiledGroupArtifact, now func() time.Time) (PlanGateExecution, error) {
	if now == nil {
		return PlanGateExecution{}, errors.New("compiled selector clock is required")
	}
	selector, err := selectorSpecForWorkload(id, artifact.group.PackageTarget)
	if err != nil {
		return PlanGateExecution{}, err
	}
	argv := []string{"go", "tool", "test2json", "-t", "-p", selector.packageTarget, artifact.binaryPath}
	argv = append(argv, selector.testArgs...)
	observation := runCompiledSelectorProcess(ctx, artifact, argv, now)
	result, profileErr := compiledSelectorResult(id, argv, observation)
	if profileErr != nil {
		return PlanGateExecution{}, profileErr
	}
	if result.Status == ResultStatusPassed {
		if profileErr := validateCompiledSelectorTiming(id, result.TestTimings); profileErr != nil {
			result.Status, result.ExitCode = ResultStatusFailed, ExecutorExitCode(profileErr)
			return result, profileErr
		}
	}
	_ = laneIndex // lane identity 仍由现有 workspace scheduler 维护。
	return result, observation.err()
}

type compiledSelectorObservation struct {
	started, bodyStarted, completed time.Time
	log                             *boundedPlanLog
	timings                         []GoTestTiming
	argv                            []string
	runErr, closeErr                error
	contextErr                      error
}

func (observation compiledSelectorObservation) err() error {
	return errors.Join(observation.runErr, observation.closeErr)
}

func selectorSpecForWorkload(id GateID, packageTarget string) (compileGroupSelectorSpec, error) {
	_, _, target, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil || !targeted {
		return compileGroupSelectorSpec{}, fmt.Errorf("compiled selector %q is invalid", id)
	}
	return compileGroupSelector(target, packageTarget, id)
}

func runCompiledSelectorProcess(ctx context.Context, artifact compiledGroupArtifact, argv []string, now func() time.Time) compiledSelectorObservation {
	started := now().UTC()
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	packageDir, packageErr := trustedCompileGroupPackageDirectory(artifact.layout.sourceCopy, artifact.packageDir)
	if packageErr != nil {
		return compiledSelectorObservation{started: started, completed: now().UTC(), log: log, argv: argv, runErr: packageErr, contextErr: ctx.Err()}
	}
	timingWriter := testtiming.NewEventWriter(log)
	command := exec.CommandContext(ctx, artifact.goBinary, argv[1:]...)
	configureCommandCancellation(command)
	command.Args[0], command.Dir, command.Env = argv[0], packageDir, artifact.environment
	command.Stdout, command.Stderr = timingWriter, log
	var bodyStarted time.Time
	runErr := runConfiguredCommandWithStart(command, func() {
		bodyStarted = now().UTC()
	})
	closeErr := timingWriter.Close()
	return compiledSelectorObservation{started: started, bodyStarted: bodyStarted, completed: now().UTC(), log: log, timings: timingWriter.Timings(), argv: argv, runErr: runErr, closeErr: closeErr, contextErr: ctx.Err()}
}

// trustedCompileGroupPackageDirectory 拒绝空目录或不可信目录，防止 selector 隐式继承进程 cwd。
func trustedCompileGroupPackageDirectory(sourceRoot, packageDir string) (string, error) {
	if strings.TrimSpace(packageDir) == "" {
		return "", errors.New("compiled selector package directory is required")
	}
	resolved, err := compileGroupPackageDirectory(sourceRoot, packageDir)
	if err != nil {
		return "", fmt.Errorf("compiled selector package directory: %w", err)
	}
	return resolved, nil
}

func compiledSelectorResult(id GateID, argv []string, observation compiledSelectorObservation) (PlanGateExecution, error) {
	result := PlanGateExecution{GateID: id, StartedAt: observation.started, CompletedAt: observation.completed, ExitCode: -1, TestTimings: observation.timings, ArgvDigest: digestCommandArgv(argv)}
	result.Log = observation.log.Bytes()
	result.LogDigest = digestPlanLog(result.Log)
	profile, err := measuredExecutionProfileForWorkload(id)
	if err != nil {
		return PlanGateExecution{}, err
	}
	profile.StartupMS = max(measuredExecutorPhaseMilliseconds(observation.started, observation.bodyStarted), cicontract.TimingResolution.Milliseconds())
	if !observation.bodyStarted.IsZero() {
		profile.TestBodyMS = max(measuredExecutorPhaseMilliseconds(observation.bodyStarted, observation.completed), cicontract.TimingResolution.Milliseconds())
	}
	profile.TotalMS = profile.StartupMS + profile.TestBodyMS
	result.ExecutionProfile = profile
	result.CompletedAt = normalizedExecutionCompletedAt(result.StartedAt, result.CompletedAt, profile)
	status, exitCode := classifyPlanGateOutcome(observation.err(), observation.contextErr)
	result.Status, result.ExitCode = status, exitCode
	if status != ResultStatusPassed {
		summary := planGateFailureSummary(observation.err(), observation.contextErr, status, exitCode)
		if len(summary) != 0 {
			_, _ = observation.log.Write(summary)
			result.Log = observation.log.Bytes()
			result.LogDigest = digestPlanLog(result.Log)
		}
	}
	canonical, err := CanonicalizePlanGateExecutionTiming(result)
	if err != nil {
		return result, err
	}
	return canonical, nil
}

type compileGroupSelectorSpec struct {
	kind          string
	packageTarget string
	testName      string
	testArgs      []string
}

// compileGroupSelector 将 manifest selector 解析成独立 test2json 参数。
func compileGroupSelector(target, packageTarget string, id GateID) (compileGroupSelectorSpec, error) {
	_, kind, raw, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil || !targeted || raw != target {
		return compileGroupSelectorSpec{}, errors.New("compile group selector target is invalid")
	}
	switch kind {
	case workloadTargetGoTest:
		return compileGroupGoTestSelector(target, packageTarget, id)
	case workloadTargetGoBenchmark:
		return compileGroupBenchmarkSelector(target, packageTarget)
	default:
		return compileGroupSelectorSpec{}, fmt.Errorf("workload %q is not a supported compile-group selector", id)
	}
}

func compileGroupGoTestSelector(target, packageTarget string, id GateID) (compileGroupSelectorSpec, error) {
	parsed, err := ParseGoTestTarget(target)
	if err != nil || parsed.Package != packageTarget {
		return compileGroupSelectorSpec{}, errors.New("compile group Go test package target drifted")
	}
	args := []string{"-test.v", "-test.run=^" + parsed.Name + "$", "-test.count=1"}
	parent, _, _, _, _ := parseTargetWorkloadID(string(id))
	if parent == GateIDBackendTestGuardWithRace {
		args = append([]string{"-test.short"}, args...)
	}
	return compileGroupSelectorSpec{kind: workloadTargetGoTest, packageTarget: packageTarget, testName: parsed.Name, testArgs: args}, nil
}

func compileGroupBenchmarkSelector(target, packageTarget string) (compileGroupSelectorSpec, error) {
	parsed, err := ParseGoBenchmarkTarget(target)
	if err != nil || parsed.Package != packageTarget {
		return compileGroupSelectorSpec{}, errors.New("compile group benchmark package target drifted")
	}
	return compileGroupSelectorSpec{kind: workloadTargetGoBenchmark, packageTarget: packageTarget, testName: parsed.Name, testArgs: []string{"-test.v", "-test.run=^$", "-test.bench=^" + parsed.Name + "$", "-test.count=1"}}, nil
}

// validateCompileGroupSelector 校验单个 selector 与组包和语义键一致。
func validateCompileGroupSelector(id GateID, packageTarget string, semanticKey string) error {
	if !isCompileGroupSelector(id) {
		return fmt.Errorf("compile group workload %q is not a supported Go selector", id)
	}
	expectedSemantic, err := CompileGroupSemanticKeyForWorkloadID(id)
	if err != nil {
		return fmt.Errorf("compile group workload %q semantic: %w", id, err)
	}
	if semanticKey != expectedSemantic {
		return fmt.Errorf("compile group workload %q semantic does not match group", id)
	}
	_, _, target, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil || !targeted {
		return fmt.Errorf("compile group workload %q is malformed", id)
	}
	_, err = compileGroupSelector(target, packageTarget, id)
	return err
}

func isCompileGroupSelector(id GateID) bool {
	_, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && (kind == workloadTargetGoTest || kind == workloadTargetGoBenchmark)
}

// CompileGroupWorkloadSupported 返回唯一允许共享 Go test binary 的 selector owner。
func CompileGroupWorkloadSupported(id GateID) bool { return isCompileGroupSelector(id) }

func cleanupCompiledGroupArtifacts(artifacts map[GateID]compiledGroupArtifact) {
	seen := make(map[string]struct{})
	for _, artifact := range artifacts {
		if _, ok := seen[artifact.group.GroupID]; ok {
			continue
		}
		seen[artifact.group.GroupID] = struct{}{}
		_ = cleanupExecutorWorkspace(artifact.layout)
	}
}

func groupUsesRace(group CompileGroup) bool {
	for _, id := range group.WorkloadIDs {
		parent, _, _, _, err := parseTargetWorkloadID(string(id))
		if err == nil && parent == GateIDBackendTestGuardWithRace {
			return true
		}
	}
	return false
}

// validateExecutorCompileGroups 校验 worker 请求的 compile group 覆盖闭包。
func validateExecutorCompileGroups(profile Profile, gateIDs []GateID, groups []CompileGroup) error {
	if len(groups) == 0 {
		for _, id := range gateIDs {
			if isCompileGroupSelector(id) {
				return fmt.Errorf("compile-group selector %q has no compile group", id)
			}
		}
		return nil
	}
	manifest := ShardExecutionManifest{SchemaVersion: ShardExecutionManifestSchemaVersion, Profile: profile, PlanDigest: "sha256:" + strings.Repeat("0", 64), ShardIdentity: digestPlanLog([]byte("worker")), SourceTreeSHA: strings.Repeat("0", 40), GateIDs: gateIDs, CompileGroups: groups}
	// 已准入请求复用 manifest validator 做精确覆盖校验，不依赖伪造的 plan/shard 值。
	return manifest.Validate()
}

func failedCompileGroupExecution(group CompileGroup, artifactKey string, err error) CompileGroupExecution {
	execution := newCompileGroupExecution(group, artifactKey)
	return finishFailedCompileGroupExecution(execution, time.Time{}, time.Time{}, err)
}

func finishFailedCompileGroupExecution(execution CompileGroupExecution, started, completed time.Time, err error) CompileGroupExecution {
	execution.Status, execution.ExitCode = ResultStatusFailed, max(1, ExecutorExitCode(err))
	execution.ErrorText = compactCompileGroupError(err)
	if started.IsZero() {
		// Preparation or Cmd.Start failed before the compile process existed. Keep only
		// the bounded failure result; no test_binary_compile observation is fabricated.
		execution.StartedAtUnixMS, execution.CompletedAtUnixMS, execution.DurationMS = 0, 0, 0
		execution.CompileCommandDigest = ""
		return execution
	}
	if !completed.After(started) {
		completed = started.Add(cicontract.TimingResolution)
	}
	setCompileGroupExecutionTiming(&execution, started, completed)
	return execution
}

// setCompileGroupExecutionTiming 统一按毫秒分辨率写入真实编译区间，避免同毫秒完成导致时序不一致。
func setCompileGroupExecutionTiming(execution *CompileGroupExecution, started, completed time.Time) {
	startedMS := started.UTC().UnixMilli()
	completedMS := completed.UTC().UnixMilli()
	if completedMS <= startedMS {
		completedMS = startedMS + cicontract.TimingResolution.Milliseconds()
	}
	execution.StartedAtUnixMS = startedMS
	execution.CompletedAtUnixMS = completedMS
	execution.DurationMS = completedMS - startedMS
}

// failedCompileGroupSelector 为失败组中的每个 selector 生成独立失败结果。
func failedCompileGroupSelector(id GateID, executions []CompileGroupExecution, now func() time.Time) (PlanGateExecution, error) {
	profile, err := measuredExecutionProfileForWorkload(id)
	if err != nil {
		return PlanGateExecution{}, err
	}
	started := now().UTC()
	log := []byte("compile group failed before selector execution\n")
	for _, execution := range executions {
		for _, workloadID := range execution.WorkloadIDs {
			if workloadID == id && execution.ErrorText != "" {
				log = []byte("compile group failed: " + execution.ErrorText + "\n")
			}
		}
	}
	completed := started.Add(time.Millisecond)
	profile.StartupMS = max(measuredExecutorPhaseMilliseconds(started, completed), 1)
	profile.TotalMS = profile.StartupMS
	return PlanGateExecution{GateID: id, Status: ResultStatusFailed, ExitCode: 1, StartedAt: started, CompletedAt: completed, Log: log, LogDigest: digestPlanLog(log), ExecutionProfile: profile}, nil
}

func compactCompileGroupError(err error) string {
	if err == nil {
		return "compile group failed"
	}
	text := strings.TrimSpace(err.Error())
	text = strings.NewReplacer("\r", " ", "\n", " ", "\x00", " ").Replace(text)
	if len(text) > compileGroupErrorTextBytes {
		text = text[len(text)-compileGroupErrorTextBytes:]
	}
	return text
}

func digestCommandArgv(argv []string) string {
	encoded, err := json.Marshal(argv)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest)
}

// validateCompiledSelectorTiming 校验 exact selector 只有一个终端 timing。
func validateCompiledSelectorTiming(id GateID, timings []GoTestTiming) error {
	_, _, target, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil || !targeted {
		return err
	}
	_, kind, _, _, _ := parseTargetWorkloadID(string(id))
	if kind != workloadTargetGoTest {
		return nil
	}
	testTarget, err := ParseGoTestTarget(target)
	if err != nil {
		return err
	}
	matched := exactGoTestTimings(timings, testTarget.Name)
	if len(matched) != 1 {
		return errors.New("compiled selector has no unique terminal test timing")
	}
	return nil
}
