package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	guardReadinessTimeout      = 5 * time.Second
	guardReadinessMaxBytes     = 16 << 10
	candidateReadinessTimeout  = 5 * time.Second
	candidateCleanupTimeout    = 5 * time.Second
	candidateStopTimeout       = time.Second
)

// runProbationSupervisor 启动候选与 detached Guard，并阻塞监督 exact ACK。
func (app updaterApp) runProbationSupervisor(ctx context.Context, transaction recovery.Transaction) error {
	if err := app.validateProbationRuntime(); err != nil {
		return err
	}
	store, err := recovery.NewStore(filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName))
	if err != nil {
		return err
	}
	candidate, err := app.startProbationCandidate(ctx, transaction)
	if err != nil {
		return app.handleCandidateStartFailure(ctx, store, transaction, candidate, err)
	}
	process := candidate.Identity()
	ownerID, err := app.newProbationOwnerID()
	if err != nil {
		return app.rollbackStartedCandidate(ctx, store, transaction, candidate, err)
	}
	lease, err := app.acquireProbationLease(store, ctx, transaction.Identity, recovery.ProbationLeaseRequest{
		OwnerID: ownerID, Process: process, TTL: probationLeaseTTL,
	})
	if err != nil {
		return app.rollbackStartedCandidate(ctx, store, transaction, candidate, err)
	}
	if _, statErr := os.Stat(transaction.Paths.RecoveryDir); errors.Is(statErr, os.ErrNotExist) {
		if err := app.startProbationGuard(ctx, transaction, false, nil); err != nil {
			return app.rollbackClaimedCandidate(ctx, store, transaction, lease, candidate, err)
		}
	} else if statErr != nil {
		return app.rollbackClaimedCandidate(ctx, store, transaction, lease, candidate, statErr)
	}
	supervisor, err := app.newProbationSupervisor(recovery.ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:  candidate.ProcessAlive,
		StopCandidate: candidate.Reclaim,
		RestartOldRelease: func(restartCtx context.Context, rolledBack recovery.Transaction) error {
			return app.convergeRollbackRestart(restartCtx, store, rolledBack)
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

// handleCandidateStartFailure 仅在 starter 已确认 child 回收后进入 unclaimed rollback。
func (app updaterApp) handleCandidateStartFailure(
	ctx context.Context,
	store *recovery.Store,
	transaction recovery.Transaction,
	candidate *candidateHandle,
	cause error,
) error {
	if candidate != nil {
		return cause
	}
	return app.rollbackLaunchFailure(ctx, store, transaction, cause)
}

// validateProbationRuntime 拒绝缺失任一 post-start cleanup 入口依赖。
func (app updaterApp) validateProbationRuntime() error {
	if app.startProbationCandidate == nil || app.newProbationOwnerID == nil || app.acquireProbationLease == nil ||
		app.startProbationGuard == nil || app.newProbationSupervisor == nil {
		return errors.New("updater probation runtime is incomplete")
	}
	return nil
}

func newProbationOwnerID() (string, error) {
	id, err := recovery.NewTransactionID()
	if err != nil {
		return "", err
	}
	return "updater-" + string(id), nil
}

// startProbationCandidate 使用 frozen env contract 启动 bundle executable 与唯一 Wait reaper。
func startProbationCandidate(ctx context.Context, transaction recovery.Transaction) (*candidateHandle, error) {
	if ctx == nil {
		return nil, errors.New("probation candidate context is required")
	}
	executable := filepath.Join(transaction.Paths.Target, launcherPath)
	digest, err := recovery.ComputeReleaseDigestContext(ctx, executable)
	if err != nil {
		return nil, fmt.Errorf("digest probation executable: %w", err)
	}
	root := filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName)
	terminationEndpoint, terminationToken, err := newCandidateTerminationContract(transaction.Identity.TransactionID)
	if err != nil {
		return nil, err
	}
	env, err := runtimeenv.AppendRecoveryLaunchEnvironment(sanitizedRestartEnv(os.Environ()), runtimeenv.RecoveryLaunch{
		TransactionRoot: root, TransactionID: string(transaction.Identity.TransactionID),
		ExecutableIdentity: filepath.Clean(executable), ExecutableSHA256: digest, ContractPresent: true,
		TerminationEndpoint: terminationEndpoint, TerminationToken: terminationToken,
	})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start probation candidate: %w", err)
	}
	provisional := recovery.ProcessIdentity{
		PID: cmd.Process.Pid, ExecutableIdentity: filepath.Clean(executable), ExecutableSHA256: digest,
		TerminationEndpoint: terminationEndpoint, TerminationToken: terminationToken,
	}
	handle := newCandidateHandle(cmd, provisional)
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return failCandidateStart(handle, err)
	}
	identity := recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: digest,
		TerminationEndpoint: terminationEndpoint, TerminationToken: terminationToken,
	}
	handle.bindIdentity(identity)
	if stable.ExecutableIdentity != filepath.Clean(executable) {
		return failCandidateStart(handle, pidregistry.ErrStableProcessIdentityMismatch)
	}
	readyCtx, cancel := ctxutil.WithTimeout(ctx, candidateReadinessTimeout)
	defer cancel()
	if err := waitCandidateTerminationReady(readyCtx, identity); err != nil {
		return failCandidateStart(handle, err)
	}
	return handle, nil
}

type candidateHandle struct {
	process     *os.Process
	directChild bool
	done        chan struct{}

	terminate       func(context.Context, recovery.ProcessIdentity) error
	captureStable   func(int) (pidregistry.StableProcessIdentity, error)
	cleanupEndpoint func(string) error
	waitCalls       atomic.Int32

	mu       sync.RWMutex
	identity recovery.ProcessIdentity
	waitErr  error
}

func newCandidateHandle(cmd *exec.Cmd, identity recovery.ProcessIdentity) *candidateHandle {
	handle := &candidateHandle{
		identity: identity, process: cmd.Process, directChild: true, done: make(chan struct{}),
		terminate: terminateCandidate, captureStable: pidregistry.CaptureStableProcessIdentity,
		cleanupEndpoint: pidregistry.CleanupCooperativeTerminationEndpoint,
	}
	logger := pkglogger.Get()
	safego.Go(context.Background(), logger, "updater.probationCandidate.wait", func(context.Context) {
		handle.waitCalls.Add(1)
		waitErr := cmd.Wait()
		handle.mu.Lock()
		handle.waitErr = waitErr
		handle.mu.Unlock()
		close(handle.done)
		logger.Info("probation candidate reaped", "pid", cmd.Process.Pid, "wait_error", waitErr)
	})
	return handle
}

func (handle *candidateHandle) bindIdentity(identity recovery.ProcessIdentity) {
	handle.mu.Lock()
	handle.identity = identity
	handle.mu.Unlock()
}

// Identity 返回 candidate handle 绑定的 exact process identity。
func (handle *candidateHandle) Identity() recovery.ProcessIdentity {
	handle.mu.RLock()
	defer handle.mu.RUnlock()
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
	if identity != handle.Identity() {
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
	if identity != handle.Identity() {
		return pidregistry.ErrStableProcessIdentityMismatch
	}
	select {
	case <-handle.done:
		return handle.cleanupEndpoint(identity.TerminationEndpoint)
	default:
	}
	if err := handle.terminate(ctx, identity); err != nil {
		return handle.resolveTerminationError(ctx, err)
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-handle.done:
		return handle.cleanupEndpoint(identity.TerminationEndpoint)
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
			"pid", handle.Identity().PID,
			"termination_error", terminationErr,
		)
		return handle.cleanupEndpoint(handle.Identity().TerminationEndpoint)
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return terminationErr
	}
}

// cleanupStartFailure 复用 rollback 的强制回收路径处理启动或 READY 失败。
func (handle *candidateHandle) cleanupStartFailure(primary error) error {
	_, err := failCandidateStart(handle, primary)
	return err
}

// failCandidateStart 只在共享回收确认完成后隐藏 handle，阻止未回收 child 进入 rollback。
func failCandidateStart(handle *candidateHandle, primary error) (*candidateHandle, error) {
	if handle == nil {
		return nil, primary
	}
	if reclaimErr := handle.Reclaim(context.Background(), handle.Identity()); reclaimErr != nil {
		return handle, errors.Join(primary, reclaimErr)
	}
	return nil, primary
}

// Reclaim 在独立 deadline 内完成 cooperative stop、exact recapture、direct kill、唯一 reap 与 endpoint cleanup。
func (handle *candidateHandle) Reclaim(caller context.Context, identity recovery.ProcessIdentity) error {
	if caller == nil {
		return errors.New("candidate reclaim caller context is required")
	}
	if handle == nil || handle.process == nil || handle.done == nil || handle.terminate == nil ||
		handle.captureStable == nil || handle.cleanupEndpoint == nil {
		return errors.New("candidate reclaim owner is incomplete")
	}
	if identity != handle.Identity() {
		return pidregistry.ErrStableProcessIdentityMismatch
	}
	cleanupCtx, cancel := ctxutil.WithTimeout(context.Background(), candidateCleanupTimeout)
	defer cancel()
	return handle.reclaimWithin(cleanupCtx, identity)
}

func (handle *candidateHandle) reclaimWithin(ctx context.Context, identity recovery.ProcessIdentity) error {
	stopCtx, cancelStop := ctxutil.WithTimeout(ctx, candidateStopTimeout)
	stopErr := handle.Stop(stopCtx, identity)
	cancelStop()
	if stopErr == nil {
		return nil
	}
	recaptureErr := handle.recaptureExact(identity)
	killErr := handle.killDirectChild()
	waitErr := normalizeCandidateWait(handle.Wait(ctx))
	endpointErr := handle.cleanupEndpoint(identity.TerminationEndpoint)
	fatalErr := errors.Join(killErr, waitErr, endpointErr)
	if fatalErr != nil {
		return errors.Join(stopErr, recaptureErr, fatalErr)
	}
	pkglogger.Get().Warn("candidate force reclaim recovered cleanup failure",
		"pid", identity.PID, "cleanup_error", errors.Join(stopErr, recaptureErr))
	return nil
}

// recaptureExact 读取当前 PID identity，并与 candidate generation 做完整比对。
func (handle *candidateHandle) recaptureExact(identity recovery.ProcessIdentity) error {
	current, err := handle.captureStable(identity.PID)
	if err != nil {
		return fmt.Errorf("recapture probation candidate identity: %w", err)
	}
	if identity.StartToken == "" || identity.ExecutableIdentity == "" {
		return errors.New("probation candidate exact identity was not captured")
	}
	if current.PID != identity.PID || current.ProcessStartToken != identity.StartToken ||
		current.ExecutableIdentity != identity.ExecutableIdentity {
		return pidregistry.ErrStableProcessIdentityMismatch
	}
	return nil
}

// killDirectChild 只使用 Start 返回的 direct-child handle；同一 handle 的 Wait 会在 reap 前禁止后续 Signal。
func (handle *candidateHandle) killDirectChild() error {
	if !handle.directChild {
		return errors.New("candidate direct-child ownership is required for forced kill")
	}
	err := handle.process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func normalizeCandidateWait(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

// waitCandidateTerminationReady 在发布 handle 前完成 token-authenticated READY。
func waitCandidateTerminationReady(ctx context.Context, identity recovery.ProcessIdentity) error {
	exact := pidregistry.StableProcessIdentity{
		PID: identity.PID, ProcessStartToken: identity.StartToken, ExecutableIdentity: identity.ExecutableIdentity,
		TerminationEndpoint: identity.TerminationEndpoint, TerminationToken: identity.TerminationToken,
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := pidregistry.ProbeExactProcessEndpoint(ctx, exact); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("authenticate candidate termination endpoint: %w", err)
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

func (handle *candidateHandle) result() error {
	handle.mu.RLock()
	defer handle.mu.RUnlock()
	return handle.waitErr
}

// startDetachedGuard 在 caller context 内验证 exact Guard 凭据，成功后才释放独立进程句柄。
func startDetachedGuard(ctx context.Context, transaction recovery.Transaction, capsule bool, readyAction func() error) error {
	cmd, executable, digest, lease, err := buildDetachedGuardCommand(ctx, transaction, capsule)
	if err != nil {
		return err
	}
	stdout, expected, lease, err := startDetachedGuardProcess(ctx, cmd, executable, digest, lease)
	if err != nil {
		return err
	}
	if err := awaitDetachedGuardArmed(ctx, stdout, transaction, expected, readyAction); err != nil {
		return errors.Join(err, stopStartedGuard(cmd, lease))
	}
	if err := handoffGuardProcessTree(cmd, lease); err != nil {
		return errors.Join(fmt.Errorf("handoff detached Guard process tree: %w", err), stopStartedGuard(cmd, lease))
	}
	return nil
}

// buildDetachedGuardCommand 在 caller context 内绑定 exact helper 路径、SHA、generation 与清洗后的环境。
func buildDetachedGuardCommand(ctx context.Context, transaction recovery.Transaction, capsule bool) (*exec.Cmd, string, string, *guardProcessTreeLease, error) {
	executable, err := detachedGuardPath(transaction, capsule)
	if err != nil {
		return nil, "", "", nil, err
	}
	executable, err = recovery.CanonicalExistingPathContext(ctx, executable)
	if err != nil {
		return nil, "", "", nil, err
	}
	generation, expectedSHA := guardGenerationIdentity(transaction, capsule)
	digest, err := recovery.ComputeReleaseDigestContext(ctx, executable)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("digest detached Guard: %w", err)
	}
	if digest != expectedSHA {
		return nil, "", "", nil, errors.New("detached Guard digest does not match exact transaction helper identity")
	}
	env, err := runtimeenv.DetachedCommandEnvironment(os.Environ())
	if err != nil {
		return nil, "", "", nil, err
	}
	transactionRoot := filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName)
	// Guard 在 armed receipt 后独立存活，因此不能让 exec.Cmd 继承 caller context 的自动 kill。
	cmd := exec.Command(executable, transactionRoot, string(transaction.Identity.TransactionID), generation)
	cmd.Env = env
	lease, err := configureGuardProcessTree(cmd)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("configure detached Guard process tree: %w", err)
	}
	return cmd, executable, digest, lease, nil
}

// startDetachedGuardProcess 启动子进程并在 caller context 内重新读取其内核 process identity。
func startDetachedGuardProcess(
	ctx context.Context,
	cmd *exec.Cmd,
	executable string,
	digest string,
	lease *guardProcessTreeLease,
) (io.ReadCloser, recovery.ProcessIdentity, *guardProcessTreeLease, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, recovery.ProcessIdentity{}, nil, fmt.Errorf("open detached Guard readiness pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, recovery.ProcessIdentity{}, nil, errors.Join(fmt.Errorf("start detached Guard: %w", err), stopUnattachedGuardProcessTree(lease))
	}
	lease, err = attachGuardProcessTree(cmd, lease)
	if err != nil {
		var cleanupErr error
		if lease != nil {
			cleanupErr = stopStartedGuard(cmd, lease)
		} else {
			cleanupErr = stopUnleasedStartedGuard(cmd)
		}
		return nil, recovery.ProcessIdentity{}, nil, errors.Join(
			fmt.Errorf("attach detached Guard process tree: %w", err),
			cleanupErr,
		)
	}
	expected, err := captureStartedGuardIdentity(ctx, cmd, executable, digest)
	if err != nil {
		return nil, recovery.ProcessIdentity{}, nil, errors.Join(err, stopStartedGuard(cmd, lease))
	}
	return stdout, expected, lease, nil
}

// awaitDetachedGuardArmed 在 caller context 内验证 armed receipt、关闭 parent pipe，再执行 destructive action。
func awaitDetachedGuardArmed(ctx context.Context, stdout io.ReadCloser, transaction recovery.Transaction, expected recovery.ProcessIdentity, readyAction func() error) error {
	receipt, err := waitGuardReadyReceiptContext(ctx, stdout, guardReadinessTimeout)
	if err != nil {
		return err
	}
	if err := recovery.ValidateGuardReadyReceipt(receipt, transaction, expected); err != nil {
		return err
	}
	if err := stdout.Close(); err != nil {
		return fmt.Errorf("close detached Guard readiness pipe: %w", err)
	}
	if readyAction != nil {
		return readyAction()
	}
	return nil
}

func guardGenerationIdentity(transaction recovery.Transaction, capsule bool) (string, string) {
	if capsule {
		return "old", transaction.Identity.OldHelpers.GuardSHA256
	}
	return "candidate", transaction.Identity.CandidateHelpers.GuardSHA256
}

func captureStartedGuardIdentity(ctx context.Context, cmd *exec.Cmd, executable, digest string) (recovery.ProcessIdentity, error) {
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return recovery.ProcessIdentity{}, fmt.Errorf("capture detached Guard identity: %w", err)
	}
	stablePath, err := recovery.CanonicalExistingPathContext(ctx, stable.ExecutableIdentity)
	if err != nil {
		return recovery.ProcessIdentity{}, err
	}
	if stablePath != executable {
		return recovery.ProcessIdentity{}, errors.New("detached Guard kernel executable identity mismatch")
	}
	return recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: executable, ExecutableSHA256: digest,
	}, nil
}

// waitGuardReadyReceiptContext 用已 join 的关闭监视器让 caller 取消中断 pipe read。
func waitGuardReadyReceiptContext(ctx context.Context, reader io.ReadCloser, timeout time.Duration) (recovery.GuardReadyReceipt, error) {
	if ctx == nil {
		return recovery.GuardReadyReceipt{}, errors.New("Guard readiness context is required")
	}
	stopWatching := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Go(func() {
		select {
		case <-ctx.Done():
			_ = reader.Close()
		case <-stopWatching:
		}
	})
	receipt, readErr := waitGuardReadyReceipt(reader, timeout)
	close(stopWatching)
	watcher.Wait()
	if err := context.Cause(ctx); err != nil {
		return recovery.GuardReadyReceipt{}, err
	}
	return receipt, readErr
}

// waitGuardReadyReceipt 通过 OS pipe deadline 有界读取单个 exact receipt，不创建游离 goroutine。
func waitGuardReadyReceipt(reader io.Reader, timeout time.Duration) (recovery.GuardReadyReceipt, error) {
	deadlineReader, ok := reader.(interface{ SetReadDeadline(time.Time) error })
	if !ok {
		return recovery.GuardReadyReceipt{}, errors.New("Guard readiness pipe does not support bounded reads")
	}
	if err := deadlineReader.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return recovery.GuardReadyReceipt{}, fmt.Errorf("set Guard readiness deadline: %w", err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, guardReadinessMaxBytes+1)).ReadBytes('\n')
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return recovery.GuardReadyReceipt{}, errors.New("timed out waiting for exact Guard readiness")
	}
	if err != nil {
		return recovery.GuardReadyReceipt{}, fmt.Errorf("read Guard readiness receipt: %w", err)
	}
	if len(line) > guardReadinessMaxBytes {
		return recovery.GuardReadyReceipt{}, errors.New("Guard readiness receipt exceeds size limit")
	}
	return recovery.DecodeGuardReadyReceipt(line)
}

func stopStartedGuard(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	return stopGuardProcessTree(cmd, lease)
}

// stopUnleasedStartedGuard 仅处理边界尚未建立且不能派生子进程的启动失败。
func stopUnleasedStartedGuard(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("started Guard direct-child process is required")
	}
	killErr := cmd.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	waitErr := normalizeGuardProcessWait(cmd.Wait())
	return errors.Join(killErr, waitErr)
}

func detachedGuardPath(transaction recovery.Transaction, capsule bool) (string, error) {
	if capsule {
		return filepath.Join(transaction.Paths.RecoveryDir, "super-dolphin-guard"), nil
	}
	updaterExecutable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(updaterExecutable), "super-dolphin-guard"), nil
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
	rolledBack, rollbackErr := store.RollbackUnclaimedProbation(ctx, transaction.Identity)
	if rollbackErr != nil {
		return errors.Join(cause, rollbackErr)
	}
	return errors.Join(cause, app.convergeRollbackRestart(ctx, store, rolledBack))
}

func (app updaterApp) rollbackStartedCandidate(ctx context.Context, store *recovery.Store, transaction recovery.Transaction, candidate *candidateHandle, cause error) error {
	if stopErr := candidate.Reclaim(ctx, candidate.Identity()); stopErr != nil {
		return errors.Join(cause, stopErr)
	}
	return app.rollbackLaunchFailure(ctx, store, transaction, cause)
}

func (app updaterApp) rollbackClaimedCandidate(ctx context.Context, store *recovery.Store, transaction recovery.Transaction, lease recovery.ProbationLease, candidate *candidateHandle, cause error) error {
	if stopErr := candidate.Reclaim(ctx, candidate.Identity()); stopErr != nil {
		return errors.Join(cause, stopErr)
	}
	rolledBack, rollbackErr := store.RollbackClaimed(ctx, transaction.Identity, lease)
	if rollbackErr != nil {
		return errors.Join(cause, rollbackErr)
	}
	return errors.Join(cause, app.convergeRollbackRestart(ctx, store, rolledBack))
}

func (app updaterApp) convergeRollbackRestart(ctx context.Context, store *recovery.Store, transaction recovery.Transaction) error {
	if app.rollbackRestartCallbackFactory == nil {
		return errors.New("updater rollback restart callback factory is required")
	}
	if app.rollbackRestartDeadline <= 0 {
		return errors.New("updater rollback restart deadline must be positive")
	}
	resolve, launch := app.rollbackRestartCallbackFactory(transaction)
	deadlineCtx, cancel := ctxutil.WithTimeout(ctx, app.rollbackRestartDeadline)
	defer cancel()
	_, err := store.ConvergeRollbackRestart(deadlineCtx, transaction.Identity, resolve, launch)
	return err
}

func terminateCandidate(ctx context.Context, process recovery.ProcessIdentity) error {
	return pidregistry.TerminateExactProcess(ctx, pidregistry.StableProcessIdentity{
		PID: process.PID, ProcessStartToken: process.StartToken, ExecutableIdentity: process.ExecutableIdentity,
		TerminationEndpoint: process.TerminationEndpoint, TerminationToken: process.TerminationToken,
	})
}

func newCandidateTerminationContract(transactionID recovery.TransactionID) (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate candidate termination token: %w", err)
	}
	token := hex.EncodeToString(raw)
	endpoint := filepath.Join(os.TempDir(), "sd-term-"+string(transactionID)[:16]+"-"+token[:8]+".sock")
	if len(endpoint) >= 100 {
		endpoint = filepath.Join("/tmp", "sd-term-"+token[:24]+".sock")
	}
	return endpoint, token, nil
}
