package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// runRemote 分派远程 CLI 子命令并保持各入口的独立协议边界。
func runRemote(args []string, input io.Reader, stdout io.Writer, progressWriters ...io.Writer) error {
	if len(args) == 0 {
		return protocolError("remote subcommand is required (run, hook, calibrate, init-ledger)")
	}
	switch args[0] {
	case "calibrate":
		return runRemoteCalibration(args[1:], stdout, progressWriters...)
	case "init-ledger":
		return runRemoteLedgerInit(args[1:])
	case "hook":
		return runRemoteHook(args[1:], input, stdout, progressWriters...)
	case "run":
		return runRemoteInvocation(args[1:], stdout, progressWriters...)
	default:
		return protocolError("remote subcommand must be run, hook, calibrate, or init-ledger")
	}
}

func runRemoteInvocation(args []string, stdout io.Writer, progressWriters ...io.Writer) error {
	if err := requireRemoteCIAgentToken([]string{"remote", "run"}, args, stdout); err != nil {
		return err
	}
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return err
	}
	if options.Scenario == "" {
		return protocolError("remote run requires --scenario")
	}
	if options.WorkloadID != "" || options.CompletionReceiptPath != "" {
		return protocolError("--workload and --completion-receipt are only valid with the test command")
	}
	progress, err := newRemoteProgressObserver(progressWriters...)
	if err != nil {
		return err
	}
	options.ProgressObserver = progress
	result, input, runErr := executeRemoteRun(options)
	runErr = errors.Join(runErr, remoteci.ProgressError(progress))
	return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}

// newRemoteProgressObserver 将 CLI stderr 绑定为唯一的 NDJSON 进度旁路。
func newRemoteProgressObserver(progressWriters ...io.Writer) (*remoteci.JSONProgressObserver, error) {
	if len(progressWriters) > 1 {
		return nil, protocolError("remote CI accepts at most one progress writer")
	}
	if len(progressWriters) == 0 || progressWriters[0] == nil {
		return nil, nil
	}
	return remoteci.NewJSONProgressObserver(progressWriters[0]), nil
}

// executeRemoteRun 构建已接受基线的远程运行，并写入可比较的时长采样。
func executeRemoteRun(options remoteRunOptions) (remoteci.RunResult, remoteci.RunInput, error) {
	var result remoteci.RunResult
	config, state, err := loadRunnableRemoteRunState(options)
	if err != nil {
		return result, remoteci.RunInput{}, err
	}
	runnerIdentity, err := resolveRemoteRunnerIdentity(options.RepositoryRoot, state)
	if err != nil {
		return result, remoteci.RunInput{}, infrastructureError(
			"resolve remote worker execution identity: %v",
			err,
		)
	}
	input, err := resolveRemoteRunInput(options, state, runnerIdentity)
	if err != nil {
		return result, remoteci.RunInput{}, sourceError("%v", err)
	}
	coordinator, containerDeadline, err := newRemoteRunCoordinator(config, input, options.ProgressObserver)
	if err != nil {
		return result, input, err
	}
	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()
	runCtx, cancel := gateprivate.WithTimeout(signalContext, containerDeadline+10*time.Minute)
	defer cancel()
	prepared, err := coordinator.Prepare(runCtx, input)
	if err != nil {
		return result, input, err
	}
	if err := reloadRemotePlanningAfterCalibration(options, state, runnerIdentity, input, prepared); err != nil {
		return result, input, err
	}
	result, runErr := coordinator.RunPrepared(runCtx, prepared)
	if err := finalizeRemoteRunEvidence(input, &result, runErr); err != nil {
		return result, input, err
	}
	return result, input, runErr
}

// loadRunnableRemoteRunState 首代空库时原子导入已由 ECI 实测的严格回执，后续只读取 accepted baseline。
func loadRunnableRemoteRunState(options remoteRunOptions) (remoteRunConfig, remoteci.BaselineState, error) {
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("load remote CI config: %v", err)
	}
	state, err := loadAcceptedRemoteBaseline(options.LedgerPath)
	if errors.Is(err, gatecontract.ErrRemoteBaselineStateNotFound) {
		bootstrapErr := initializeConfiguredRemoteGenerationOne(config, options.LedgerPath)
		if bootstrapErr != nil && !configuredRemoteGenerationOneAlreadyAccepted(config, options.LedgerPath) {
			return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("initialize remote baseline generation one: %v", bootstrapErr)
		}
		state, err = loadAcceptedRemoteBaseline(options.LedgerPath)
	}
	if err != nil {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("load accepted remote baseline: %v", err)
	}
	if err := validateRunnableRemoteBaseline(state); err != nil {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("validate accepted remote baseline: %v", err)
	}
	if state.ExecutionProvider != cicontract.ExecutionProviderID || state.RegionID != config.RegionID {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("accepted remote baseline is not bound to the configured Alibaba Cloud ECI region")
	}
	return config, state, nil
}

// reloadRemotePlanningAfterCalibration 只在普通运行出现 miss 时校准并重载既有计划快照。
func reloadRemotePlanningAfterCalibration(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
	input remoteci.RunInput,
	prepared *remoteci.PreparedRun,
) error {
	if prepared.AllReused() || input.Calibration {
		return nil
	}
	if err := ensureRemoteDurationCalibration(options, state, runnerIdentity); err != nil {
		return err
	}
	reloaded, _, err := loadRemoteRunLedger(options, state, runnerIdentity)
	if err != nil {
		return infrastructureError("reload remote CI planning snapshot after calibration: %v", err)
	}
	if err := validateRemoteDurationCalibration(options, state, runnerIdentity, reloaded.Ledger); err != nil {
		return protocolError("validate remote CI duration calibration after automatic calibration: %v", err)
	}
	if err := prepared.ReloadPlanningSnapshot(input.LedgerStore); err != nil {
		return infrastructureError("reload prepared remote CI planning snapshot after calibration: %v", err)
	}
	return nil
}

// newRemoteRunCoordinator 创建 OSS/ECI 客户端与协调器，并绑定本次运行的截止时间、进度观察器和 accepted snapshot。
func newRemoteRunCoordinator(
	config remoteRunConfig,
	input remoteci.RunInput,
	progressObservers ...remoteci.ProgressObserver,
) (*remoteci.Coordinator, time.Duration, error) {
	store, err := oss.NewCLI(oss.Config{
		Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint,
		Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix,
	})
	if err != nil {
		return nil, 0, infrastructureError("create remote CI OSS client: %v", err)
	}
	workerTimeout, err := remoteWorkerTimeout(input.Profile, input.Calibration)
	if err != nil {
		return nil, 0, protocolError("resolve remote CI deadline: %v", err)
	}
	containerDeadline, err := remoteContainerDeadline(workerTimeout)
	if err != nil {
		return nil, 0, protocolError("resolve remote CI container deadline: %v", err)
	}
	if len(progressObservers) > 1 {
		return nil, 0, protocolError("remote CI accepts at most one progress observer")
	}
	var progressObserver remoteci.ProgressObserver
	if len(progressObservers) == 1 {
		progressObserver = progressObservers[0]
	}
	runtime, err := eci.New(eci.Config{
		Binary:   config.AliyunCLI,
		RegionID: config.RegionID, VSwitches: slices.Clone(config.VSwitches), SecurityGroupID: config.SecurityGroupID,
		WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile,
		Deadline:     containerDeadline,
		SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1,
		RegistryCredentialLoader: loadRemoteRegistryCredential,
	})
	if err != nil {
		return nil, 0, infrastructureError("create remote CI ECI client: %v", err)
	}
	coordinator, err := remoteci.NewCoordinator(remoteci.CoordinatorConfig{
		Bucket: config.OSS.Bucket, SourcePrefix: config.OSS.SourcePrefix,
		InternalOSSEndpoint: config.OSS.InternalEndpoint,
		WorkerRoleName:      config.WorkerRoleName, WorkerTimeout: workerTimeout,
		ImageCacheSnapshotID: input.ImageCacheSnapshotID,
		PollInterval:         2 * time.Second, CleanupTimeout: 2 * time.Minute,
		ResourcePolicy:   config.Capacity.ResourcePolicy,
		ProgressObserver: progressObserver,
	}, store, runtime)
	if err != nil {
		return nil, 0, infrastructureError("create remote CI coordinator: %v", err)
	}
	return coordinator, containerDeadline, nil
}

// finalizeRemoteRunEvidence 校验本次证据、持久化权威回执和时长，并在全部成功后公布权威结果。
func finalizeRemoteRunEvidence(input remoteci.RunInput, result *remoteci.RunResult, runErr error) error {
	if runErr != nil {
		return remoteRunEvidenceError(runErr, "execute remote CI before authority finalization", runErr)
	}
	receipts, err := validateRemoteRunEvidence(input, *result, runErr)
	if err != nil {
		return err
	}
	var overhead *gatecontract.ShardOrchestrationOverheadEvidence
	if input.Calibration && len(result.FreshWorkloadExecutions) != 0 {
		evidence, err := prepareRemoteRunShardOverhead(input, *result)
		if err != nil {
			return remoteRunReceiptAuthorityError(runErr, "derive remote CI shard orchestration overhead", err)
		}
		overhead = &evidence
	}
	if overhead == nil {
		if err := finalizeRemoteRunReceiptAuthority(input, *result, receipts, result.DurationSamples, runErr); err != nil {
			return err
		}
	} else if err := finalizeRemoteRunReceiptAuthorityWithShardOverhead(input, *result, receipts, result.DurationSamples, runErr, *overhead); err != nil {
		return err
	}
	result.Authoritative = true
	return nil
}

// prepareRemoteRunShardOverhead 从 provisional SQLite run 读取完整 timing，
// 在最终化前派生并校验证据；真正写入与 authority CAS 在同一事务内完成。
func prepareRemoteRunShardOverhead(input remoteci.RunInput, result remoteci.RunResult) (gatecontract.ShardOrchestrationOverheadEvidence, error) {
	if input.LedgerStore == nil {
		return gatecontract.ShardOrchestrationOverheadEvidence{}, errors.New("remote CI duration ledger store is nil")
	}
	record, err := input.LedgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		return gatecontract.ShardOrchestrationOverheadEvidence{}, err
	}
	planning := gatecontract.PlanningContext{
		Platform:           input.Platform,
		Runner:             input.RunnerIdentityDigest,
		Toolchain:          input.ToolchainDigest,
		TargetDurationMS:   gatecontract.FullCITargetDurationMS,
		AcceptedSnapshotID: input.ImageCacheSnapshotID,
	}
	resource := input.CalibrationResource
	return gatecontract.DeriveShardOrchestrationOverheadEvidence(
		record.JobID, record.AcceptedGeneration, planning,
		resource.ID, float64(resource.VCPU), float64(resource.MemoryGiB),
		record.ImageCacheSnapshotID, record.TimingObservations, record.Shards,
	)
}

// validateRemoteRunEvidence 要求完整回执、必需检查和非终止时长告警都满足远程 CI 契约。
func validateRemoteRunEvidence(
	input remoteci.RunInput,
	workerResult remoteci.RunResult,
	runErr error,
) ([]gatecontract.CheckReceiptRecord, error) {
	_, receipts, contractErr := validateRemoteRunContract(input, input.AcceptedGeneration, workerResult)
	if contractErr != nil {
		return nil, remoteRunEvidenceError(runErr, "validate remote CI executed check receipts", contractErr)
	}
	if err := cicontract.ValidateTimingWarningAction(cicontract.TimingWarningWarnAndContinue); err != nil {
		return nil, remoteRunEvidenceError(runErr, "validate non-terminating remote CI timing warning action", err)
	}
	return receipts, nil
}

// remoteRunEvidenceError 将证据验证失败固定为不可通过的远程 CI 门禁结果。
func remoteRunEvidenceError(runErr error, operation string, err error) error {
	return errors.Join(
		runErr,
		remoteci.ErrGateFailed,
		protocolError("%s: %v", operation, err),
	)
}

// finalizeRemoteRunReceiptAuthority 把当前回执重载、run 升权和 fresh PASS
// evidence 提升收敛在唯一 SQLite authority 的同一事务内。
func finalizeRemoteRunReceiptAuthority(
	input remoteci.RunInput,
	result remoteci.RunResult,
	receipts []gatecontract.CheckReceiptRecord,
	samples []gatecontract.DurationSample,
	runErr error,
) error {
	if err := validateRemoteRunStoredAggregateExecutionReadback(input, result); err != nil {
		return remoteRunReceiptAuthorityError(runErr, "validate remote CI aggregate execution readback before finalization", err)
	}
	identity := remoteRunAuthorityIdentity(result)
	if err := input.LedgerStore.FinalizeRemoteCIRunAuthorityWithSamples(identity, receipts, samples, len(result.FreshWorkloadExecutions) != 0); err != nil {
		return remoteRunReceiptAuthorityError(runErr, "finalize remote CI receipt, authority, and fresh workload evidence", err)
	}
	return nil
}

// finalizeRemoteRunReceiptAuthorityWithShardOverhead 将 calibration overhead 与
// receipt/authority/fresh evidence 绑定到同一个 SQLite 最终化事务。
func finalizeRemoteRunReceiptAuthorityWithShardOverhead(
	input remoteci.RunInput,
	result remoteci.RunResult,
	receipts []gatecontract.CheckReceiptRecord,
	samples []gatecontract.DurationSample,
	runErr error,
	evidence gatecontract.ShardOrchestrationOverheadEvidence,
) error {
	if err := validateRemoteRunStoredAggregateExecutionReadback(input, result); err != nil {
		return remoteRunReceiptAuthorityError(runErr, "validate remote CI aggregate execution readback before finalization", err)
	}
	identity := remoteRunAuthorityIdentity(result)
	if err := input.LedgerStore.FinalizeRemoteCIRunAuthorityWithShardOverhead(identity, receipts, samples, len(result.FreshWorkloadExecutions) != 0, evidence); err != nil {
		return remoteRunReceiptAuthorityError(runErr, "finalize remote CI receipt, shard overhead, authority, and fresh workload evidence", err)
	}
	return nil
}

// remoteRunAuthorityIdentity 提取 immutable remote run 字段作为 SQLite authority 身份。
func remoteRunAuthorityIdentity(result remoteci.RunResult) gatecontract.RemoteCIRunAuthorityIdentity {
	return gatecontract.RemoteCIRunAuthorityIdentity{
		JobID: result.JobID, AgentTokenDigest: result.AgentTokenDigest, Force: result.Force, Entrypoint: result.Entrypoint,
		Profile: result.Profile, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		AcceptedGeneration: result.AcceptedGeneration, SourceTreeSHA: result.SourceTreeSHA,
		ImageCacheSnapshotID:         result.ImageCacheSnapshotID,
		CandidateGateSourceSHA256:    result.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256,
		RunnerImage:                  result.RunnerImage, StartedAt: result.StartedAt,
		WorkloadPassIdentities: slices.Clone(result.WorkloadPassIdentities),
	}
}

// remoteRunReceiptAuthorityError 将回执权威链的任一失败固定为不可通过的门禁结果。
func remoteRunReceiptAuthorityError(runErr error, operation string, err error) error {
	return errors.Join(
		runErr,
		remoteci.ErrGateFailed,
		infrastructureError("%s: %v", operation, err),
	)
}

// emitRemoteRunResult 编码远程运行结果、回放权威时长账本，并将失败映射为稳定退出错误。
func emitRemoteRunResult(stdout io.Writer, ledgerStore *gatecontract.DurationLedgerStore, result remoteci.RunResult, runErr error) error {
	var outcomeErr error
	if runErr != nil {
		if errors.Is(runErr, remoteci.ErrGateFailed) {
			outcomeErr = gatecontract.WithExitCode(gatecontract.ExitGateViolation, runErr)
		} else {
			outcomeErr = infrastructureError("execute remote CI: %v", runErr)
		}
	}
	if result.SchemaVersion != 0 {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return errors.Join(outcomeErr, infrastructureError("encode remote CI result: %v", err))
		}
		if ledgerStore != nil {
			if err := remoteci.RenderHumanTimingLedgerFromAuthority(os.Stderr, ledgerStore, result.JobID); err != nil {
				return errors.Join(outcomeErr, infrastructureError("render remote CI timing ledger: %v", err))
			}
		}
	}
	return outcomeErr
}
