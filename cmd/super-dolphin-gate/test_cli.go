package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	autoTestRunResultSchemaVersion uint32 = 1
	localLightLockWait                    = 5 * time.Millisecond
	cacheProbeTimeout                     = 2 * time.Minute
)

type autoTestBackend string

const (
	autoTestBackendRemoteCache autoTestBackend = "remote-cache"
	autoTestBackendLocalLight  autoTestBackend = "local-light"
	autoTestBackendRemoteECI   autoTestBackend = "remote-eci"
)

type autoTestRunResult struct {
	SchemaVersion   uint32                    `json:"schema_version"`
	Backend         autoTestBackend           `json:"backend"`
	SourceTreeSHA   string                    `json:"source_tree_sha"`
	Status          gatecontract.ResultStatus `json:"status"`
	Authoritative   bool                      `json:"authoritative"`
	CloudVerified   bool                      `json:"cloud_verified"`
	ReusedWorkloads []gatecontract.GateID     `json:"reused_workloads,omitempty"`
	Executions      []localLightTestExecution `json:"executions,omitempty"`
	StartedAt       time.Time                 `json:"started_at"`
	CompletedAt     time.Time                 `json:"completed_at"`
}

// runTestInvocation 按云端缓存、本机轻量或 ECI 的顺序路由指定测试。
func runTestInvocation(args []string, stdout io.Writer) error {
	options, err := parseAutoTestRunOptions(args)
	if err != nil {
		return err
	}
	config, input, err := prepareAutoTestRun(options)
	if err != nil {
		return err
	}
	probe, err := newAutoTestCacheProbe(config)
	if err != nil {
		return err
	}
	selection, err := probeAutoTestCache(probe, input)
	if err != nil {
		return infrastructureError("probe shared remote test cache: %v", err)
	}
	backend, _, err := selectAutoTestBackend(selection, input)
	if err != nil {
		return infrastructureError("select test execution backend: %v", err)
	}
	if backend == autoTestBackendRemoteCache {
		return emitAutoTestRunResult(stdout, cachedAutoTestResult(input, selection))
	}
	if backend == autoTestBackendRemoteECI {
		return executeAndEmitRemoteTest(options, stdout)
	}
	sourceMatches, err := localSourceMatchesTree(input.RepositoryRoot, input.Tree)
	if err != nil {
		return infrastructureError("verify local light test source: %v", err)
	}
	if !sourceMatches {
		return executeAndEmitRemoteTest(options, stdout)
	}
	return runLockedLocalLightTests(options, input, probe, stdout)
}

// parseAutoTestRunOptions 只接受测试选择器并固定 test 场景。
func parseAutoTestRunOptions(args []string) (remoteRunOptions, error) {
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return remoteRunOptions{}, err
	}
	if options.Scenario != "" || options.Profile != "" || options.Entrypoint != "" ||
		options.LocalRef != "" || options.RemoteRef != "" || options.ObservedRemote != "" ||
		options.UpdateKind != "" {
		return remoteRunOptions{}, protocolError("test command does not accept scenario, profile, entrypoint, or push flags")
	}
	if len(options.Tests) == 0 {
		return remoteRunOptions{}, protocolError("test command requires at least one --test selector")
	}
	options.Scenario = "test"
	return options, nil
}

func prepareAutoTestRun(
	options remoteRunOptions,
) (remoteRunConfig, remoteci.RunInput, error) {
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return remoteRunConfig{}, remoteci.RunInput{},
			protocolError("load remote CI config: %v", err)
	}
	state, err := loadAcceptedRemoteBaseline(options.LedgerPath)
	if err != nil {
		return remoteRunConfig{}, remoteci.RunInput{},
			protocolError("load accepted remote baseline: %v", err)
	}
	if err := validateRunnableRemoteBaseline(config, state); err != nil {
		return remoteRunConfig{}, remoteci.RunInput{},
			protocolError("validate accepted remote baseline: %v", err)
	}
	runnerIdentity, err := resolveRemoteRunnerIdentity(options.RepositoryRoot, state)
	if err != nil {
		return remoteRunConfig{}, remoteci.RunInput{},
			infrastructureError("resolve remote worker execution identity: %v", err)
	}
	input, err := resolveRemoteRunInput(options, config, state, runnerIdentity)
	if err != nil {
		return remoteRunConfig{}, remoteci.RunInput{},
			sourceError("%v", err)
	}
	return config, input, nil
}

func newAutoTestCacheProbe(config remoteRunConfig) (*remoteci.WorkloadCacheProbe, error) {
	store, err := oss.NewCLI(oss.Config{
		Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint,
		Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix,
	})
	if err != nil {
		return nil, infrastructureError("create remote CI OSS client: %v", err)
	}
	probe, err := remoteci.NewWorkloadCacheProbe(
		config.OSS.SourcePrefix+"passed-workloads/v1/",
		store,
	)
	if err != nil {
		return nil, infrastructureError("create shared remote test cache probe: %v", err)
	}
	return probe, nil
}

func probeAutoTestCache(
	probe *remoteci.WorkloadCacheProbe,
	input remoteci.RunInput,
) (remoteci.WorkloadCacheProbeResult, error) {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), cacheProbeTimeout)
	defer cancel()
	return probe.Probe(ctx, input)
}

// selectAutoTestBackend 确保所有请求先过滤缓存，且本机最多执行一个轻量 miss。
func selectAutoTestBackend(
	selection remoteci.WorkloadCacheProbeResult,
	input remoteci.RunInput,
) (autoTestBackend, map[string]remoteci.LocalLightTestDecision, error) {
	if len(selection.CacheMissWorkloads) == 0 {
		return autoTestBackendRemoteCache, nil, nil
	}
	if len(selection.CacheMissWorkloads) != 1 {
		return autoTestBackendRemoteECI, nil, nil
	}
	decisions := make(map[string]remoteci.LocalLightTestDecision, len(selection.CacheMissWorkloads))
	for _, workload := range selection.CacheMissWorkloads {
		decision, err := remoteci.DecideLocalLightTest(workload, input)
		if err != nil {
			return "", nil, err
		}
		decisions[workload.ID] = decision
		if !decision.Eligible {
			return autoTestBackendRemoteECI, decisions, nil
		}
	}
	return autoTestBackendLocalLight, decisions, nil
}

func cachedAutoTestResult(
	input remoteci.RunInput,
	selection remoteci.WorkloadCacheProbeResult,
) autoTestRunResult {
	now := time.Now().UTC()
	return autoTestRunResult{
		SchemaVersion: autoTestRunResultSchemaVersion,
		Backend:       autoTestBackendRemoteCache,
		SourceTreeSHA: input.Tree,
		Status:        gatecontract.ResultStatusPassed,
		CloudVerified: true,
		ReusedWorkloads: append(
			[]gatecontract.GateID(nil),
			selection.ReusedWorkloads...,
		),
		StartedAt: now, CompletedAt: now,
	}
}

// runLockedLocalLightTests 使用跨工作树锁串行一个本机轻量测试。
func runLockedLocalLightTests(
	options remoteRunOptions,
	input remoteci.RunInput,
	probe *remoteci.WorkloadCacheProbe,
	stdout io.Writer,
) (resultErr error) {
	lock, contended, err := acquireLocalLightTestLock(options.LedgerPath)
	if err != nil {
		return err
	}
	if contended {
		return executeAndEmitRemoteTest(options, stdout)
	}
	defer func() {
		resultErr = joinLocalLightLockRelease(resultErr, lock)
	}()
	return runWithAcquiredLocalLightLock(options, input, probe, lock, stdout)
}

// acquireLocalLightTestLock 尝试取得账本旁的共享锁，竞争时要求调用方转远程。
func acquireLocalLightTestLock(ledgerPath string) (*gateprivate.ExclusiveFileLock, bool, error) {
	absolutePath, err := filepath.Abs(ledgerPath)
	if err != nil {
		return nil, false, infrastructureError("resolve local light test lock path: %v", err)
	}
	lockCtx, cancel := gateprivate.WithTimeout(context.Background(), localLightLockWait)
	defer cancel()
	lock, err := gateprivate.AcquireExclusiveFileLock(lockCtx, absolutePath+".local-light.lock")
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, infrastructureError("acquire local light test lock: %v", err)
	}
	return lock, false, nil
}

// joinLocalLightLockRelease 将锁释放失败并入原始执行错误。
func joinLocalLightLockRelease(resultErr error, lock *gateprivate.ExclusiveFileLock) error {
	if err := lock.Release(); err != nil {
		return errors.Join(resultErr, infrastructureError("release local light test lock: %v", err))
	}
	return resultErr
}

// runWithAcquiredLocalLightLock 在锁内重新探测缓存并按最新结果执行。
func runWithAcquiredLocalLightLock(
	options remoteRunOptions,
	input remoteci.RunInput,
	probe *remoteci.WorkloadCacheProbe,
	lock *gateprivate.ExclusiveFileLock,
	stdout io.Writer,
) error {
	refreshed, err := probeAutoTestCache(probe, input)
	if err != nil {
		return infrastructureError("recheck shared remote test cache: %v", err)
	}
	backend, refreshedDecisions, err := selectAutoTestBackend(refreshed, input)
	if err != nil {
		return infrastructureError("reselect test execution backend: %v", err)
	}
	switch backend {
	case autoTestBackendRemoteCache:
		return emitAutoTestRunResult(stdout, cachedAutoTestResult(input, refreshed))
	case autoTestBackendRemoteECI:
		if err := lock.Release(); err != nil {
			return infrastructureError("release local light test lock before remote execution: %v", err)
		}
		return executeAndEmitRemoteTest(options, stdout)
	case autoTestBackendLocalLight:
		return executeAndEmitLocalLightTest(
			options,
			input,
			refreshed,
			refreshedDecisions,
			lock,
			stdout,
		)
	default:
		return infrastructureError("unknown test execution backend %q", backend)
	}
}

// executeAndEmitLocalLightTest 执行单个已过滤目标，超时时释放本机锁并转入 ECI。
func executeAndEmitLocalLightTest(
	options remoteRunOptions,
	input remoteci.RunInput,
	selection remoteci.WorkloadCacheProbeResult,
	decisions map[string]remoteci.LocalLightTestDecision,
	lock *gateprivate.ExclusiveFileLock,
	stdout io.Writer,
) error {
	result, runErr := executeLocalLightTests(input, selection, decisions)
	if errors.Is(runErr, errLocalLightTestTimeout) || errors.Is(runErr, errLocalLightTestNeedsRemote) {
		if err := lock.Release(); err != nil {
			return infrastructureError("release local light test lock after timeout: %v", err)
		}
		return executeAndEmitRemoteTest(options, stdout)
	}
	if err := emitAutoTestRunResult(stdout, result); err != nil {
		return err
	}
	if errors.Is(runErr, errLocalLightTestFailed) {
		return gatecontract.WithExitCode(gatecontract.ExitGateViolation, runErr)
	}
	if runErr != nil {
		return infrastructureError("execute local light test: %v", runErr)
	}
	return nil
}

func executeAndEmitRemoteTest(options remoteRunOptions, stdout io.Writer) error {
	result, _, runErr := executeRemoteRun(options)
	return emitRemoteRunResult(stdout, result, runErr)
}

func emitAutoTestRunResult(stdout io.Writer, result autoTestRunResult) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return infrastructureError("encode automatic test result: %v", err)
	}
	return nil
}
