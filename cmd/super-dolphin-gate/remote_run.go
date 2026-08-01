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
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// runRemote 分派远程 CLI 子命令并保持各入口的独立协议边界。
func runRemote(args []string, input io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("remote subcommand is required (run, hook, calibrate, baseline-refresh)")
	}
	switch args[0] {
	case "baseline-refresh":
		return runRemoteBaselineRefresh(args[1:], stdout)
	case "calibrate":
		return runRemoteCalibration(args[1:], stdout)
	case "hook":
		return runRemoteHook(args[1:], input, stdout)
	case "run":
		return runRemoteInvocation(args[1:], stdout)
	default:
		return protocolError("remote subcommand must be run, hook, calibrate, or baseline-refresh")
	}
}

func runRemoteInvocation(args []string, stdout io.Writer) error {
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return err
	}
	result, _, runErr := executeRemoteRun(options)
	return emitRemoteRunResult(stdout, result, runErr)
}

// executeRemoteRun 构建已接受基线的远程运行，并写入可比较的时长采样。
func executeRemoteRun(options remoteRunOptions) (remoteci.RunResult, remoteci.RunInput, error) {
	var result remoteci.RunResult
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return result, remoteci.RunInput{}, protocolError("load remote CI config: %v", err)
	}
	state, err := loadAcceptedRemoteBaseline(options.ConfigPath, options.StatePath, options.LedgerPath)
	if err != nil {
		return result, remoteci.RunInput{}, protocolError("load accepted remote baseline: %v", err)
	}
	if err := validateRunnableRemoteBaseline(config, state); err != nil {
		return result, remoteci.RunInput{}, protocolError("validate accepted remote baseline: %v", err)
	}
	runnerIdentity, err := resolveRemoteRunnerIdentity(options.RepositoryRoot, state)
	if err != nil {
		return result, remoteci.RunInput{}, infrastructureError(
			"resolve remote worker execution identity: %v",
			err,
		)
	}
	if err := ensureRemoteDurationCalibration(options, state, runnerIdentity); err != nil {
		return result, remoteci.RunInput{}, err
	}
	input, err := resolveRemoteRunInput(options, config, state, runnerIdentity)
	if err != nil {
		return result, remoteci.RunInput{}, sourceError("%v", err)
	}
	coordinator, containerDeadline, err := newRemoteRunCoordinator(config, state, input)
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
	result, runErr := coordinator.Run(runCtx, input)
	if err := appendRemoteDurationSamples(options.LedgerPath, result.DurationSamples); err != nil {
		return result, input, infrastructureError("persist remote CI duration samples: %v", err)
	}
	return result, input, runErr
}

// newRemoteRunCoordinator 以 worker 时限和额外初始化租约构造单次远程协调器。
func newRemoteRunCoordinator(
	config remoteRunConfig,
	state remoteci.BaselineState,
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
		Image: state.RuntimeImage, Deadline: containerDeadline,
		SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1, FallbackToPayAsYouGo: true,
	})
	if err != nil {
		return nil, 0, infrastructureError("create remote CI ECI client: %v", err)
	}
	phaseObserver, err := remoteci.NewTextPhaseObserver(os.Stderr)
	if err != nil {
		return nil, 0, infrastructureError("create remote CI phase observer: %v", err)
	}
	coordinator, err := remoteci.NewCoordinator(remoteci.CoordinatorConfig{
		Bucket: config.OSS.Bucket, SourcePrefix: config.OSS.SourcePrefix,
		WorkloadCachePrefix: config.OSS.SourcePrefix + "passed-workloads/v1/",
		InternalOSSEndpoint: config.OSS.InternalEndpoint,
		WorkerRoleName:      config.WorkerRoleName, WorkerTimeout: workerTimeout,
		PollInterval: 2 * time.Second, CleanupTimeout: 2 * time.Minute,
		ResourcePolicy:      config.Capacity.ResourcePolicy,
		CandidateCLIBuilder: buildRemoteCandidateCLI,
	}, store, runtime, phaseObserver)
	if err != nil {
		return nil, 0, infrastructureError("create remote CI coordinator: %v", err)
	}
	return coordinator, containerDeadline, nil
}

// appendRemoteDurationSamples 仅在本次运行产生采样时追加到同一 CAS 账本。
func appendRemoteDurationSamples(path string, samples []gatecontract.DurationSample) error {
	if len(samples) == 0 {
		return nil
	}
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		return err
	}
	_, err = store.AppendSamplesFast(samples)
	return err
}

func emitRemoteRunResult(stdout io.Writer, result remoteci.RunResult, runErr error) error {
	if result.SchemaVersion != 0 {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return infrastructureError("encode remote CI result: %v", err)
		}
		if err := remoteci.RenderHumanTimingLedger(os.Stderr, result); err != nil {
			return infrastructureError("render remote CI timing ledger: %v", err)
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
