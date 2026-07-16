package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	probationLeaseTTL          = 30 * time.Second
	probationObservationPeriod = 2 * time.Second
	probationPollInterval      = 100 * time.Millisecond
)

// runProbationSupervisor 启动候选与 detached Guard，并阻塞监督 exact ACK。
func (app updaterApp) runProbationSupervisor(ctx context.Context, transaction recovery.Transaction) error {
	store, err := recovery.NewStore(filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName))
	if err != nil {
		return err
	}
	candidate, err := startProbationCandidate(transaction)
	if err != nil {
		return app.rollbackLaunchFailure(ctx, store, transaction, err)
	}
	process := candidate.Identity()
	ownerID, err := newProbationOwnerID()
	if err != nil {
		return app.rollbackStartedCandidate(ctx, store, transaction, candidate, err)
	}
	lease, err := store.AcquireProbationLease(ctx, transaction.Identity, recovery.ProbationLeaseRequest{
		OwnerID: ownerID, Process: process, TTL: probationLeaseTTL,
	})
	if err != nil {
		return app.rollbackStartedCandidate(ctx, store, transaction, candidate, err)
	}
	if err := startDetachedGuard(filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName)); err != nil {
		return app.rollbackClaimedCandidate(ctx, store, transaction, lease, candidate, err)
	}
	supervisor, err := recovery.NewProbationSupervisor(recovery.ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:  candidate.ProcessAlive,
		StopCandidate: candidate.Stop,
		RestartOldRelease: func(_ context.Context, rolledBack recovery.Transaction) error {
			return app.restartTargetApp(rolledBack.Paths.Target)
		},
		ObservationPeriod: probationObservationPeriod,
		PollInterval:      probationPollInterval,
		Now:               time.Now,
	})
	if err != nil {
		return app.rollbackClaimedCandidate(ctx, store, transaction, lease, candidate, err)
	}
	return supervisor.Run(ctx)
}

func newProbationOwnerID() (string, error) {
	id, err := recovery.NewTransactionID()
	if err != nil {
		return "", err
	}
	return "updater-" + string(id), nil
}

// startProbationCandidate 使用 frozen env contract 启动 bundle executable 与唯一 Wait reaper。
func startProbationCandidate(transaction recovery.Transaction) (*candidateHandle, error) {
	executable := filepath.Join(transaction.Paths.Target, launcherPath)
	digest, err := recovery.ComputeReleaseDigest(executable)
	if err != nil {
		return nil, fmt.Errorf("digest probation executable: %w", err)
	}
	root := filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName)
	env, err := runtimeenv.AppendRecoveryLaunchEnvironment(sanitizedRestartEnv(os.Environ()), runtimeenv.RecoveryLaunch{
		TransactionRoot: root, TransactionID: string(transaction.Identity.TransactionID),
		ExecutableIdentity: filepath.Clean(executable), ExecutableSHA256: digest, ContractPresent: true,
	})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start probation candidate: %w", err)
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return nil, errors.Join(err, killStartedCandidate(cmd))
	}
	identity := recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: digest,
	}
	handle := newCandidateHandle(cmd, identity)
	if stable.ExecutableIdentity != filepath.Clean(executable) {
		stopCtx, cancel := ctxutil.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(pidregistry.ErrStableProcessIdentityMismatch, handle.Stop(stopCtx, identity))
	}
	return handle, nil
}

type candidateHandle struct {
	identity recovery.ProcessIdentity
	done     chan struct{}

	mu      sync.RWMutex
	waitErr error
}

func newCandidateHandle(cmd *exec.Cmd, identity recovery.ProcessIdentity) *candidateHandle {
	handle := &candidateHandle{identity: identity, done: make(chan struct{})}
	logger := pkglogger.Get()
	safego.Go(context.Background(), logger, "updater.probationCandidate.wait", func(context.Context) {
		waitErr := cmd.Wait()
		handle.mu.Lock()
		handle.waitErr = waitErr
		handle.mu.Unlock()
		close(handle.done)
		logger.Info("probation candidate reaped", "pid", identity.PID, "wait_error", waitErr)
	})
	return handle
}

// Identity 返回 candidate handle 绑定的 exact process identity。
func (handle *candidateHandle) Identity() recovery.ProcessIdentity {
	return handle.identity
}

// Wait 有界等待唯一 reaper 的真实 cmd.Wait 结果。
func (handle *candidateHandle) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("candidate wait context is required")
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-handle.done:
		handle.mu.RLock()
		defer handle.mu.RUnlock()
		return handle.waitErr
	}
}

// ProcessAlive 优先读取 reaper 结果，否则复核内核 stable identity。
func (handle *candidateHandle) ProcessAlive(identity recovery.ProcessIdentity) (bool, error) {
	if identity != handle.identity {
		return false, pidregistry.ErrStableProcessIdentityMismatch
	}
	select {
	case <-handle.done:
		return false, handle.result()
	default:
		return processAlive(identity)
	}
}

// Stop 只终止绑定的 exact process，并等待唯一 reaper 确认退出。
func (handle *candidateHandle) Stop(ctx context.Context, identity recovery.ProcessIdentity) error {
	if ctx == nil {
		return errors.New("candidate stop context is required")
	}
	if identity != handle.identity {
		return pidregistry.ErrStableProcessIdentityMismatch
	}
	select {
	case <-handle.done:
		return nil
	default:
	}
	if err := terminateCandidate(ctx, identity); err != nil {
		return handle.resolveTerminationError(ctx, err)
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-handle.done:
		return nil
	}
}

// resolveTerminationError 仅在唯一 reaper 紧随终止探针错误确认 exact child 已退出时接受成功。
func (handle *candidateHandle) resolveTerminationError(ctx context.Context, terminationErr error) error {
	timer := time.NewTimer(probationPollInterval)
	defer timer.Stop()
	select {
	case <-handle.done:
		pkglogger.Get().Info(
			"candidate exit confirmed by reaper",
			"pid", handle.identity.PID,
			"termination_error", terminationErr,
		)
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return terminationErr
	}
}

func (handle *candidateHandle) result() error {
	handle.mu.RLock()
	defer handle.mu.RUnlock()
	return handle.waitErr
}

func killStartedCandidate(cmd *exec.Cmd) error {
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill candidate after identity capture failure: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("reap candidate after identity capture failure: %w", err)
		}
	}
	return nil
}

// startDetachedGuard 从 updater 同目录启动独立 Guard 并立即释放进程句柄。
func startDetachedGuard(transactionRoot string) error {
	updaterExecutable, err := os.Executable()
	if err != nil {
		return err
	}
	guardExecutable := filepath.Join(filepath.Dir(updaterExecutable), "super-dolphin-guard")
	if info, err := os.Stat(guardExecutable); err != nil {
		return fmt.Errorf("inspect detached Guard: %w", err)
	} else if info.IsDir() {
		return errors.New("detached Guard path is a directory")
	}
	env, err := runtimeenv.DetachedCommandEnvironment(os.Environ())
	if err != nil {
		return err
	}
	cmd := exec.Command(guardExecutable, transactionRoot)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached Guard: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release detached Guard process: %w", err)
	}
	return nil
}

// processAlive 读取内核 stable identity，PID reuse 或读取失败都作为监督错误返回。
func processAlive(identity recovery.ProcessIdentity) (bool, error) {
	stable, err := pidregistry.CaptureStableProcessIdentity(identity.PID)
	if err != nil {
		return false, err
	}
	if stable.ProcessStartToken != identity.StartToken || stable.ExecutableIdentity != identity.ExecutableIdentity {
		return false, pidregistry.ErrStableProcessIdentityMismatch
	}
	return true, nil
}

func (app updaterApp) rollbackLaunchFailure(ctx context.Context, store *recovery.Store, transaction recovery.Transaction, cause error) error {
	_, rollbackErr := store.RollbackUnclaimedProbation(ctx, transaction.Identity)
	restartErr := app.restartTargetApp(transaction.Paths.Target)
	return errors.Join(cause, rollbackErr, restartErr)
}

func (app updaterApp) rollbackStartedCandidate(ctx context.Context, store *recovery.Store, transaction recovery.Transaction, candidate *candidateHandle, cause error) error {
	if stopErr := candidate.Stop(ctx, candidate.Identity()); stopErr != nil {
		return errors.Join(cause, stopErr)
	}
	return app.rollbackLaunchFailure(ctx, store, transaction, cause)
}

func (app updaterApp) rollbackClaimedCandidate(ctx context.Context, store *recovery.Store, transaction recovery.Transaction, lease recovery.ProbationLease, candidate *candidateHandle, cause error) error {
	if stopErr := candidate.Stop(ctx, candidate.Identity()); stopErr != nil {
		return errors.Join(cause, stopErr)
	}
	_, rollbackErr := store.RollbackClaimed(ctx, transaction.Identity, lease)
	restartErr := app.restartTargetApp(transaction.Paths.Target)
	return errors.Join(cause, rollbackErr, restartErr)
}

func terminateCandidate(ctx context.Context, process recovery.ProcessIdentity) error {
	return pidregistry.TerminateExactProcess(ctx, pidregistry.StableProcessIdentity{
		PID: process.PID, ProcessStartToken: process.StartToken, ExecutableIdentity: process.ExecutableIdentity,
	})
}
