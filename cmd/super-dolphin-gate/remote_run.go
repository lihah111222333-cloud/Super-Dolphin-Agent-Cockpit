package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
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
func runRemote(args []string, input io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("remote subcommand is required (run, hook, calibrate, provision-generation-one)")
	}
	switch args[0] {
	case "calibrate":
		return runRemoteCalibration(args[1:], stdout)
	case "hook":
		return runRemoteHook(args[1:], input, stdout)
	case "run":
		return runRemoteInvocation(args[1:], stdout)
	case "provision-generation-one":
		return runRemoteGenerationOneProvision(args[1:], stdout)
	default:
		return protocolError("remote subcommand must be run, hook, calibrate, or provision-generation-one")
	}
}

func runRemoteInvocation(args []string, stdout io.Writer) error {
	if err := requireRemoteCIAgentToken([]string{"remote", "run"}, args, stdout); err != nil {
		return err
	}
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return err
	}
	if options.WorkloadID != "" || options.CompletionReceiptPath != "" {
		return protocolError("--workload and --completion-receipt are only valid with the test command")
	}
	result, input, runErr := executeRemoteRun(options)
	return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
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
	if err := configureRemoteRunCalibration(&input, config); err != nil {
		return result, input, err
	}
	coordinator, containerDeadline, err := newRemoteRunCoordinator(config, input)
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
	if err := refreshRemotePlanningAfterCalibration(options, state, runnerIdentity, input, prepared); err != nil {
		return result, input, err
	}
	result, runErr := coordinator.RunPrepared(runCtx, prepared)
	if err := finalizeRemoteRunEvidence(input, &result, runErr); err != nil {
		return result, input, err
	}
	return result, input, runErr
}

// loadRunnableRemoteRunState 读取并验证本次运行固定使用的 accepted baseline。
func loadRunnableRemoteRunState(options remoteRunOptions) (remoteRunConfig, remoteci.BaselineState, error) {
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("load remote CI config: %v", err)
	}
	state, err := loadAcceptedRemoteBaseline(options.LedgerPath)
	if err != nil {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("load accepted remote baseline: %v", err)
	}
	if err := validateRunnableRemoteBaseline(state); err != nil {
		return remoteRunConfig{}, remoteci.BaselineState{}, protocolError("validate accepted remote baseline: %v", err)
	}
	return config, state, nil
}

// refreshRemotePlanningAfterCalibration 只在普通运行出现 miss 时校准并刷新既有计划快照。
func refreshRemotePlanningAfterCalibration(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
	input remoteci.RunInput,
	prepared *remoteci.PreparedRun,
) error {
	if prepared.AllReused() || input.Calibration || input.SelectedTests {
		return nil
	}
	if err := ensureRemoteDurationCalibration(options, state, runnerIdentity); err != nil {
		return err
	}
	refreshed, _, err := loadRemoteRunLedger(options, state, runnerIdentity)
	if err != nil {
		return infrastructureError("reload remote CI planning snapshot after calibration: %v", err)
	}
	scenario, _, err := resolveRemoteScenario(options)
	if err != nil {
		return protocolError("resolve remote CI scenario after automatic calibration: %v", err)
	}
	if err := validateRemoteDurationCalibration(options, scenario, state, runnerIdentity, refreshed.Ledger); err != nil {
		return protocolError("validate remote CI duration calibration after automatic calibration: %v", err)
	}
	if err := prepared.RefreshPlanningSnapshot(input.LedgerStore); err != nil {
		return infrastructureError("refresh prepared remote CI planning snapshot after calibration: %v", err)
	}
	return nil
}

// newRemoteRunCoordinator 以 worker 时限和额外初始化租约构造单次远程协调器。
func newRemoteRunCoordinator(
	config remoteRunConfig,
	input remoteci.RunInput,
) (*remoteci.Coordinator, time.Duration, error) {
	store, err := oss.NewCLI(oss.Config{
		Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint,
		Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix,
	})
	if err != nil {
		return nil, 0, infrastructureError("create remote CI OSS client: %v", err)
	}
	workerTimeout, err := remoteProfileDeadline(input.Profile)
	if err != nil {
		return nil, 0, protocolError("resolve remote CI deadline: %v", err)
	}
	containerDeadline, err := remoteContainerDeadline(workerTimeout)
	if err != nil {
		return nil, 0, protocolError("resolve remote CI container deadline: %v", err)
	}
	runtime, err := eci.New(eci.Config{
		Binary:   config.AliyunCLI,
		RegionID: config.RegionID, VSwitchID: config.VSwitchID, SecurityGroupID: config.SecurityGroupID,
		WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile,
		Deadline:     containerDeadline,
		SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1,
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
		ResourcePolicy: config.Capacity.ResourcePolicy,
	}, store, runtime)
	if err != nil {
		return nil, 0, infrastructureError("create remote CI coordinator: %v", err)
	}
	return coordinator, containerDeadline, nil
}

// configureRemoteRunCalibration 为校准运行解析并绑定固定资源规格。
func configureRemoteRunCalibration(input *remoteci.RunInput, config remoteRunConfig) error {
	if !input.Calibration {
		return nil
	}
	resource, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass()
	if err != nil {
		return protocolError("resolve fixed remote calibration resources: %v", err)
	}
	input.CalibrationResource = resource
	return nil
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
	if err := finalizeRemoteRunReceiptAuthority(input, *result, receipts, result.DurationSamples, runErr); err != nil {
		return err
	}
	result.Authoritative = true
	return nil
}

// validateRemoteRunEvidence 要求完整回执、必需检查和非终止时长告警都满足远程 CI 契约。
func validateRemoteRunEvidence(
	input remoteci.RunInput,
	workerResult remoteci.RunResult,
	runErr error,
) ([]gatecontract.CheckReceiptRecord, error) {
	observations, receipts, contractErr := validateRemoteRunContract(input, input.AcceptedGeneration, workerResult)
	if contractErr != nil {
		return nil, remoteRunEvidenceError(runErr, "validate remote CI executed check receipts", contractErr)
	}
	if err := cicontract.ValidateRequiredChecksObservedPass(observations); err != nil {
		return nil, remoteRunEvidenceError(runErr, "validate complete structured remote CI check observations", err)
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
	identity := gatecontract.RemoteCIRunAuthorityIdentity{
		JobID: result.JobID, AgentTokenDigest: result.AgentTokenDigest, Entrypoint: result.Entrypoint,
		Profile: result.Profile, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		AcceptedGeneration: result.AcceptedGeneration, SourceTreeSHA: result.SourceTreeSHA,
		ImageCacheSnapshotID:         result.ImageCacheSnapshotID,
		CandidateGateSourceSHA256:    result.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256,
		RunnerImage:                  result.RunnerImage, StartedAt: result.StartedAt,
	}
	if err := input.LedgerStore.FinalizeRemoteCIRunAuthorityWithSamples(identity, receipts, samples, len(result.FreshWorkloadExecutions) != 0); err != nil {
		return remoteRunReceiptAuthorityError(runErr, "finalize remote CI receipt, authority, and fresh workload evidence", err)
	}
	return nil
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
	if result.SchemaVersion != 0 {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return infrastructureError("encode remote CI result: %v", err)
		}
		if ledgerStore != nil {
			if err := remoteci.RenderHumanTimingLedgerFromAuthority(os.Stderr, ledgerStore, result.JobID); err != nil {
				return infrastructureError("render remote CI timing ledger: %v", err)
			}
		}
	}
	if runErr == nil {
		return nil
	}
	if errors.Is(runErr, remoteci.ErrGateFailed) {
		return gatecontract.WithExitCode(gatecontract.ExitGateViolation, runErr)
	}
	return infrastructureError("execute remote CI: %v", runErr)
}
