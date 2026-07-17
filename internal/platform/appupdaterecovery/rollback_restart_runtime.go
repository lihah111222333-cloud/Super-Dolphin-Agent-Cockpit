package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

const (
	rollbackRestartCleanupTimeout    = 3 * time.Second
	rollbackRestartTerminateTimeout  = time.Second
	rollbackRestartFirstWaitTimeout  = time.Second
	rollbackRestartEndpointPoll      = 10 * time.Millisecond
	rollbackRestartEndpointNameBytes = 16
)

var errRollbackRestartCleanupTimeout = errors.New("rollback restart cleanup timed out")

type rollbackRestartRuntime struct {
	cleanupLimits    rollbackRestartCleanupLimits
	find             func(context.Context, string, string) (pidregistry.StableProcessIdentity, bool, error)
	start            func(context.Context, string, string, []string) (*exec.Cmd, error)
	waitReady        func(context.Context, pidregistry.StableProcessIdentity) (pidregistry.CooperativeEndpointIdentity, error)
	capture          func(context.Context, int) (pidregistry.StableProcessIdentity, error)
	validate         func(context.Context, pidregistry.StableProcessIdentity, string) (RollbackRestartProcess, error)
	prepare          func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error
	activate         func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error
	release          func(*os.Process) error
	requestTerminate func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error
	terminate        func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error
	kill             func(*os.Process) error
	waitChild        func(context.Context, int) error
	cleanupEndpoint  func(string, pidregistry.CooperativeEndpointIdentity) error
}

type rollbackRestartCleanupLimits struct {
	total, terminate, firstWait time.Duration
}

// RollbackRestartCallbacks 构造按 durable launch token 重发现或启动旧版本的共享回调。
func RollbackRestartCallbacks(transaction Transaction) (RollbackRestartResolver, RollbackRestartLauncher) {
	return rollbackRestartCallbacksWithRuntime(transaction, rollbackRestartRuntime{
		cleanupLimits: rollbackRestartCleanupLimits{
			total: rollbackRestartCleanupTimeout, terminate: rollbackRestartTerminateTimeout,
			firstWait: rollbackRestartFirstWaitTimeout,
		},
		find: pidregistry.FindStableProcessByArgumentContext,
		start: func(ctx context.Context, executable, argument string, env []string) (*exec.Cmd, error) {
			if err := rollbackRestartContextError(ctx); err != nil {
				return nil, err
			}
			cmd := exec.Command(executable, argument)
			cmd.Env = env
			if err := rollbackRestartContextError(ctx); err != nil {
				return nil, err
			}
			return cmd, cmd.Start()
		},
		waitReady: waitRollbackRestartEndpoint,
		capture: func(ctx context.Context, pid int) (pidregistry.StableProcessIdentity, error) {
			if err := rollbackRestartContextError(ctx); err != nil {
				return pidregistry.StableProcessIdentity{}, err
			}
			identity, err := pidregistry.CaptureStableProcessIdentity(pid)
			if err != nil {
				return pidregistry.StableProcessIdentity{}, err
			}
			if err := rollbackRestartContextError(ctx); err != nil {
				return pidregistry.StableProcessIdentity{}, err
			}
			return identity, nil
		},
		validate:         rollbackRestartProcess,
		prepare:          pidregistry.PrepareExactProcessStartup,
		activate:         pidregistry.ActivateExactProcessStartup,
		release:          (*os.Process).Release,
		requestTerminate: pidregistry.RequestExactProcessTerminationInstance,
		terminate:        pidregistry.TerminateExactProcessInstance,
		kill:             (*os.Process).Kill,
		waitChild:        waitRollbackRestartChild,
		cleanupEndpoint:  pidregistry.CleanupCooperativeTerminationEndpointInstance,
	})
}

// rollbackRestartCallbacksWithRuntime 通过可注入进程原语复用生产 launcher 状态机。
func rollbackRestartCallbacksWithRuntime(transaction Transaction, runtime rollbackRestartRuntime) (RollbackRestartResolver, RollbackRestartLauncher) {
	executable := filepath.Join(transaction.Paths.Target, "Contents", "MacOS", "agent-terminal")
	argument := func(token string) string { return "--super-dolphin-rollback-launch-token=" + token }
	resolve := func(ctx context.Context, token string) (RollbackRestartControl, bool, error) {
		return resolveRollbackRestartProcess(ctx, transaction, runtime, argument(token), token, executable)
	}
	launch := func(ctx context.Context, token string) (RollbackRestartControl, error) {
		return launchRollbackRestartProcess(ctx, transaction, runtime, argument(token), token, executable)
	}
	return resolve, launch
}

// resolveRollbackRestartProcess 从 durable token 重发现进程，并用冻结 contract 构造失败清理权。
func resolveRollbackRestartProcess(
	ctx context.Context,
	transaction Transaction,
	runtime rollbackRestartRuntime,
	argument, token, executable string,
) (RollbackRestartControl, bool, error) {
	if err := validateRollbackRestartCleanupLimits(runtime.cleanupLimits); err != nil {
		return RollbackRestartControl{}, false, err
	}
	if err := validateRollbackRestartResolverRuntime(runtime); err != nil {
		return RollbackRestartControl{}, false, err
	}
	if err := verifyRolledBackRelease(ctx, transaction); err != nil {
		return RollbackRestartControl{}, false, err
	}
	stable, found, err := runtime.find(ctx, argument, executable)
	if err != nil || !found {
		return RollbackRestartControl{}, found, err
	}
	process, err := runtime.validate(ctx, stable, executable)
	if err != nil {
		return RollbackRestartControl{}, false, err
	}
	contract, err := rollbackRestartRecoveryLaunch(ctx, transaction, token, executable)
	if err != nil {
		return RollbackRestartControl{}, false, err
	}
	exact := rollbackRestartStableIdentity(stable, contract)
	endpointIdentity, err := runtime.waitReady(ctx, exact)
	if err != nil {
		return RollbackRestartControl{}, false, err
	}
	cleanup := func() error { return cleanupReleasedRollbackProcess(runtime, exact, endpointIdentity) }
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartControl{}, false, errors.Join(err, cleanup())
	}
	return RollbackRestartControl{
		Process: process, Cleanup: cleanup,
		Prepare:  func(prepareCtx context.Context) error { return runtime.prepare(prepareCtx, exact, endpointIdentity) },
		Activate: func(activateCtx context.Context) error { return runtime.activate(activateCtx, exact, endpointIdentity) },
	}, true, nil
}

func launchRollbackRestartProcess(
	ctx context.Context,
	transaction Transaction,
	runtime rollbackRestartRuntime,
	argument, token, executable string,
) (RollbackRestartControl, error) {
	if err := validateRollbackRestartCleanupLimits(runtime.cleanupLimits); err != nil {
		return RollbackRestartControl{}, err
	}
	if err := validateRollbackRestartLauncherRuntime(runtime); err != nil {
		return RollbackRestartControl{}, err
	}
	contract, env, err := prepareRollbackRestartLaunch(ctx, transaction, token, executable)
	if err != nil {
		return RollbackRestartControl{}, err
	}
	cmd, stable, endpointIdentity, err := startRollbackRestartProcess(ctx, runtime, contract, executable, argument, env)
	if err != nil {
		return RollbackRestartControl{}, err
	}
	return prepareRollbackRestartControl(ctx, runtime, contract, executable, cmd, stable, endpointIdentity)
}

func validateRollbackRestartCleanupLimits(limits rollbackRestartCleanupLimits) error {
	if limits.total <= 0 || limits.terminate <= 0 || limits.firstWait <= 0 {
		return errors.New("rollback restart cleanup timeouts must be positive")
	}
	if limits.terminate+limits.firstWait >= limits.total {
		return errors.New("rollback restart cleanup timeout must reserve bounded fallback time")
	}
	return nil
}

// validateRollbackRestartResolverRuntime 拒绝缺失认证或 cleanup 原语的 resolver。
func validateRollbackRestartResolverRuntime(runtime rollbackRestartRuntime) error {
	if runtime.find == nil || runtime.waitReady == nil || runtime.validate == nil || runtime.prepare == nil || runtime.activate == nil ||
		runtime.terminate == nil || runtime.cleanupEndpoint == nil {
		return errors.New("rollback restart resolver runtime is incomplete")
	}
	return nil
}

// validateRollbackRestartLauncherRuntime 拒绝缺失启动或失败清理原语的 launcher。
func validateRollbackRestartLauncherRuntime(runtime rollbackRestartRuntime) error {
	if runtime.start == nil || runtime.waitReady == nil || runtime.validate == nil || runtime.prepare == nil || runtime.activate == nil {
		return errors.New("rollback restart launcher runtime is incomplete")
	}
	if err := validateRollbackRestartCleanupRuntime(runtime); err != nil {
		return err
	}
	return nil
}

// validateRollbackRestartCleanupRuntime 确保 post-start 失败可 kill、wait、reap 并清理 endpoint。
func validateRollbackRestartCleanupRuntime(runtime rollbackRestartRuntime) error {
	if runtime.capture == nil || runtime.release == nil || runtime.requestTerminate == nil || runtime.terminate == nil ||
		runtime.kill == nil || runtime.waitChild == nil || runtime.cleanupEndpoint == nil {
		return errors.New("rollback restart launcher runtime is incomplete")
	}
	return nil
}

// prepareRollbackRestartLaunch 冻结完整 recovery contract 并拒绝已存在的终止端点。
func prepareRollbackRestartLaunch(
	ctx context.Context,
	transaction Transaction,
	token, executable string,
) (runtimeenv.RecoveryLaunch, []string, error) {
	if err := verifyRolledBackRelease(ctx, transaction); err != nil {
		return runtimeenv.RecoveryLaunch{}, nil, err
	}
	contract, err := rollbackRestartRecoveryLaunch(ctx, transaction, token, executable)
	if err != nil {
		return runtimeenv.RecoveryLaunch{}, nil, err
	}
	env, err := runtimeenv.DetachedCommandEnvironment(os.Environ())
	if err != nil {
		return runtimeenv.RecoveryLaunch{}, nil, err
	}
	env, err = runtimeenv.AppendRecoveryLaunchEnvironment(env, contract)
	if err != nil {
		return runtimeenv.RecoveryLaunch{}, nil, err
	}
	if err := cleanupStaleRollbackRestartEndpoint(ctx, contract.TerminationEndpoint); err != nil {
		return runtimeenv.RecoveryLaunch{}, nil, err
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return runtimeenv.RecoveryLaunch{}, nil, err
	}
	return contract, env, nil
}

func startRollbackRestartProcess(
	ctx context.Context,
	runtime rollbackRestartRuntime,
	contract runtimeenv.RecoveryLaunch,
	executable, argument string,
	env []string,
) (*exec.Cmd, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity, error) {
	cmd, err := runtime.start(ctx, executable, argument, env)
	if err != nil {
		return nil, pidregistry.StableProcessIdentity{}, pidregistry.CooperativeEndpointIdentity{}, err
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return nil, pidregistry.StableProcessIdentity{}, pidregistry.CooperativeEndpointIdentity{}, cleanupStartedRollbackProcess(runtime, cmd, cmd.Process.Pid, pidregistry.StableProcessIdentity{}, pidregistry.CooperativeEndpointIdentity{}, contract, err)
	}
	stable, err := runtime.capture(ctx, cmd.Process.Pid)
	if err != nil {
		return nil, pidregistry.StableProcessIdentity{}, pidregistry.CooperativeEndpointIdentity{}, cleanupStartedRollbackProcess(runtime, cmd, cmd.Process.Pid, pidregistry.StableProcessIdentity{}, pidregistry.CooperativeEndpointIdentity{}, contract, err)
	}
	exact := rollbackRestartStableIdentity(stable, contract)
	endpointIdentity, err := runtime.waitReady(ctx, exact)
	if err != nil {
		return nil, pidregistry.StableProcessIdentity{}, pidregistry.CooperativeEndpointIdentity{}, cleanupStartedRollbackProcess(runtime, cmd, cmd.Process.Pid, stable, pidregistry.CooperativeEndpointIdentity{}, contract, err)
	}
	return cmd, stable, endpointIdentity, nil
}

// prepareRollbackRestartControl 复验 READY 与 tuple，并将 direct-child ownership 保留到 ACK 后。
func prepareRollbackRestartControl(
	ctx context.Context,
	runtime rollbackRestartRuntime,
	contract runtimeenv.RecoveryLaunch,
	executable string,
	cmd *exec.Cmd,
	stable pidregistry.StableProcessIdentity,
	endpointIdentity pidregistry.CooperativeEndpointIdentity,
) (RollbackRestartControl, error) {
	childPID := cmd.Process.Pid
	process, err := runtime.validate(ctx, stable, executable)
	if err != nil {
		return RollbackRestartControl{}, cleanupStartedRollbackProcess(runtime, cmd, childPID, stable, endpointIdentity, contract, err)
	}
	exact := rollbackRestartStableIdentity(stable, contract)
	confirmedEndpoint, err := runtime.waitReady(ctx, exact)
	if err != nil {
		return RollbackRestartControl{}, cleanupStartedRollbackProcess(runtime, cmd, childPID, stable, endpointIdentity, contract, err)
	}
	if confirmedEndpoint != endpointIdentity {
		return RollbackRestartControl{}, cleanupStartedRollbackProcess(
			runtime, cmd, childPID, stable, endpointIdentity, contract, pidregistry.ErrCooperativeEndpointIdentityMismatch,
		)
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartControl{}, cleanupStartedRollbackProcess(runtime, cmd, childPID, stable, endpointIdentity, contract, err)
	}
	cleanup := func() error {
		return cleanupStartedRollbackProcess(runtime, cmd, childPID, stable, endpointIdentity, contract, nil)
	}
	return RollbackRestartControl{
		Process: process, Cleanup: cleanup,
		Prepare:  func(prepareCtx context.Context) error { return runtime.prepare(prepareCtx, exact, endpointIdentity) },
		Activate: func(activateCtx context.Context) error { return runtime.activate(activateCtx, exact, endpointIdentity) },
		release:  func() error { return runtime.release(cmd.Process) },
	}, nil
}

func rollbackRestartRecoveryLaunch(ctx context.Context, transaction Transaction, token, executable string) (runtimeenv.RecoveryLaunch, error) {
	if !validLowerHex(token, rollbackLaunchTokenBytes*2) {
		return runtimeenv.RecoveryLaunch{}, errors.New("rollback restart launch token must be 64 lowercase hex characters")
	}
	transactionID := string(transaction.Identity.TransactionID)
	if !validLowerHex(transactionID, transactionIDBytes*2) {
		return runtimeenv.RecoveryLaunch{}, errors.New("rollback restart transaction ID is invalid")
	}
	digest, err := ComputeReleaseDigestContext(ctx, executable)
	if err != nil {
		return runtimeenv.RecoveryLaunch{}, fmt.Errorf("digest rollback restart executable: %w", err)
	}
	endpoint := filepath.Join(os.TempDir(), fmt.Sprintf(
		"sd-rr-%s-%s.sock", transactionID[:8], token[:rollbackRestartEndpointNameBytes],
	))
	return runtimeenv.RecoveryLaunch{
		TransactionRoot: TransactionRootForTarget(transaction.Paths.Target), TransactionID: transactionID,
		ExecutableIdentity: filepath.Clean(executable), ExecutableSHA256: digest,
		TerminationEndpoint: filepath.Clean(endpoint), TerminationToken: token, ContractPresent: true,
	}, nil
}

func cleanupStaleRollbackRestartEndpoint(ctx context.Context, endpoint string) error {
	_, err := os.Lstat(endpoint)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect rollback restart termination endpoint: %w", err)
	}
	if err := pidregistry.CleanupStaleCooperativeTerminationEndpoint(ctx, endpoint); err != nil {
		return fmt.Errorf("cleanup stale rollback restart termination endpoint: %w", err)
	}
	return nil
}

// waitRollbackRestartEndpoint 等待 exact 协作终止端点就绪并响应 callback 取消。
func waitRollbackRestartEndpoint(
	ctx context.Context,
	exact pidregistry.StableProcessIdentity,
) (pidregistry.CooperativeEndpointIdentity, error) {
	ticker := time.NewTicker(rollbackRestartEndpointPoll)
	defer ticker.Stop()
	for {
		if err := rollbackRestartContextError(ctx); err != nil {
			return pidregistry.CooperativeEndpointIdentity{}, err
		}
		endpointIdentity, err := pidregistry.CaptureCooperativeEndpointIdentity(exact.TerminationEndpoint)
		if err == nil {
			err = pidregistry.ProbeExactProcessEndpointInstance(ctx, exact, endpointIdentity)
		}
		if err == nil {
			return endpointIdentity, nil
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, pidregistry.ErrCooperativeEndpointNotReady) {
			return pidregistry.CooperativeEndpointIdentity{}, fmt.Errorf("authenticate rollback restart termination endpoint: %w", err)
		}
		select {
		case <-ctx.Done():
			return pidregistry.CooperativeEndpointIdentity{}, context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

// cleanupStartedRollbackProcess 在独立有界上下文内终止并回收尚未 Release 的直接子进程。
func cleanupStartedRollbackProcess(
	runtime rollbackRestartRuntime,
	cmd *exec.Cmd,
	childPID int,
	stable pidregistry.StableProcessIdentity,
	endpointIdentity pidregistry.CooperativeEndpointIdentity,
	contract runtimeenv.RecoveryLaunch,
	primary error,
) error {
	cleanupCtx, cancel := context.WithTimeoutCause(context.Background(), runtime.cleanupLimits.total, errRollbackRestartCleanupTimeout)
	defer cancel()

	if stable.PID == 0 {
		captured, err := runtime.capture(cleanupCtx, cmd.Process.Pid)
		if err != nil {
			return errors.Join(primary, fmt.Errorf("capture started rollback process for cleanup: %w", err),
				fallbackRollbackChildCleanup(cleanupCtx, runtime, cmd.Process, childPID, contract.TerminationEndpoint, endpointIdentity))
		}
		stable = captured
	}
	exact := rollbackRestartStableIdentity(stable, contract)
	if endpointIdentity == (pidregistry.CooperativeEndpointIdentity{}) {
		return errors.Join(primary, fallbackRollbackChildCleanup(
			cleanupCtx, runtime, cmd.Process, childPID, contract.TerminationEndpoint, endpointIdentity,
		))
	}
	terminateCtx, terminateCancel := context.WithTimeoutCause(
		cleanupCtx, runtime.cleanupLimits.terminate, errRollbackRestartCleanupTimeout,
	)
	terminationErr := runtime.requestTerminate(terminateCtx, exact, endpointIdentity)
	terminateCancel()
	if terminationErr != nil {
		terminationErr = fmt.Errorf("request exact rollback process termination: %w", terminationErr)
		return errors.Join(primary, terminationErr,
			fallbackRollbackChildCleanup(cleanupCtx, runtime, cmd.Process, childPID, contract.TerminationEndpoint, endpointIdentity))
	}

	waitCtx, waitCancel := context.WithTimeoutCause(
		cleanupCtx, runtime.cleanupLimits.firstWait, errRollbackRestartCleanupTimeout,
	)
	waitErr := runtime.waitChild(waitCtx, childPID)
	waitCancel()
	if waitErr == nil {
		return errors.Join(primary, runtime.release(cmd.Process), runtime.cleanupEndpoint(contract.TerminationEndpoint, endpointIdentity))
	}
	waitErr = fmt.Errorf("wait exact rollback child after cooperative termination: %w", waitErr)
	return errors.Join(primary, waitErr,
		fallbackRollbackChildCleanup(cleanupCtx, runtime, cmd.Process, childPID, contract.TerminationEndpoint, endpointIdentity))
}

func fallbackRollbackChildCleanup(
	ctx context.Context,
	runtime rollbackRestartRuntime,
	process *os.Process,
	childPID int,
	endpoint string,
	endpointIdentity pidregistry.CooperativeEndpointIdentity,
) error {
	killErr := runtime.kill(process)
	if killErr != nil {
		killErr = fmt.Errorf("kill exact rollback child handle: %w", killErr)
	}
	waitErr := runtime.waitChild(ctx, childPID)
	if waitErr != nil {
		waitErr = fmt.Errorf("wait exact rollback child after kill: %w", waitErr)
		if releaseErr := runtime.release(process); releaseErr != nil {
			return errors.Join(killErr, waitErr, fmt.Errorf("release unreaped rollback child: %w", releaseErr))
		}
		return errors.Join(killErr, waitErr)
	}
	var cleanupErr error
	if endpointIdentity != (pidregistry.CooperativeEndpointIdentity{}) {
		cleanupErr = runtime.cleanupEndpoint(endpoint, endpointIdentity)
	}
	return errors.Join(killErr, runtime.release(process), cleanupErr)
}

func cleanupReleasedRollbackProcess(
	runtime rollbackRestartRuntime,
	exact pidregistry.StableProcessIdentity,
	endpointIdentity pidregistry.CooperativeEndpointIdentity,
) error {
	cleanupCtx, cancel := context.WithTimeoutCause(context.Background(), runtime.cleanupLimits.total, errRollbackRestartCleanupTimeout)
	defer cancel()
	if err := runtime.terminate(cleanupCtx, exact, endpointIdentity); err != nil {
		return fmt.Errorf("terminate released rollback process: %w", err)
	}
	return runtime.cleanupEndpoint(exact.TerminationEndpoint, endpointIdentity)
}

func rollbackRestartStableIdentity(stable pidregistry.StableProcessIdentity, contract runtimeenv.RecoveryLaunch) pidregistry.StableProcessIdentity {
	stable.TerminationEndpoint = contract.TerminationEndpoint
	stable.TerminationToken = contract.TerminationToken
	return stable
}

// verifyRolledBackRelease 校验回滚目标仍是 transaction 绑定的旧版本。
func verifyRolledBackRelease(ctx context.Context, transaction Transaction) error {
	if err := rollbackRestartContextError(ctx); err != nil {
		return err
	}
	canonical, err := CanonicalExistingPath(transaction.Paths.Target)
	if err != nil {
		return err
	}
	if canonical != transaction.Paths.Target {
		return errors.New("rolled back target is not canonical")
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return err
	}
	digest, err := ComputeReleaseDigestContext(ctx, transaction.Paths.Target)
	if err != nil {
		return err
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return err
	}
	if digest != transaction.Identity.OldRelease.SHA256 {
		return errors.New("rolled back target digest does not match old release")
	}
	return nil
}

// rollbackRestartProcess 将内核稳定身份绑定到 canonical executable 与实时摘要。
func rollbackRestartProcess(ctx context.Context, stable pidregistry.StableProcessIdentity, expectedExecutable string) (RollbackRestartProcess, error) {
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartProcess{}, err
	}
	canonical, err := CanonicalExistingPath(stable.ExecutableIdentity)
	if err != nil {
		return RollbackRestartProcess{}, err
	}
	if canonical != expectedExecutable {
		return RollbackRestartProcess{}, pidregistry.ErrStableProcessIdentityMismatch
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartProcess{}, err
	}
	digest, err := ComputeReleaseDigestContext(ctx, canonical)
	if err != nil {
		return RollbackRestartProcess{}, err
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartProcess{}, err
	}
	return RollbackRestartProcess{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: canonical, ExecutableSHA256: digest,
	}, nil
}
