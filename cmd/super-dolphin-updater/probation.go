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
	if _, statErr := os.Stat(transaction.Paths.RecoveryDir); errors.Is(statErr, os.ErrNotExist) {
		if err := startDetachedGuard(transaction, false, nil); err != nil {
			return app.rollbackClaimedCandidate(ctx, store, transaction, lease, candidate, err)
		}
	} else if statErr != nil {
		return app.rollbackClaimedCandidate(ctx, store, transaction, lease, candidate, statErr)
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
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return nil, errors.Join(err, killStartedCandidate(cmd))
	}
	identity := recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: digest,
		TerminationEndpoint: terminationEndpoint, TerminationToken: terminationToken,
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

// startDetachedGuard 在有界时间内验证 exact Guard 凭据，成功后才释放进程句柄。
func startDetachedGuard(transaction recovery.Transaction, capsule bool, readyAction func() error) error {
	cmd, executable, digest, err := buildDetachedGuardCommand(transaction, capsule)
	if err != nil {
		return err
	}
	stdout, expected, err := startDetachedGuardProcess(cmd, executable, digest)
	if err != nil {
		return err
	}
	if err := awaitDetachedGuardArmed(stdout, transaction, expected, readyAction); err != nil {
		return errors.Join(err, stopStartedGuard(cmd))
	}
	if err := cmd.Process.Release(); err != nil {
		return errors.Join(fmt.Errorf("release detached Guard process: %w", err), stopStartedGuard(cmd))
	}
	return nil
}

// buildDetachedGuardCommand 绑定 exact helper 路径、SHA、generation 与清洗后的环境。
func buildDetachedGuardCommand(transaction recovery.Transaction, capsule bool) (*exec.Cmd, string, string, error) {
	executable, err := detachedGuardPath(transaction, capsule)
	if err != nil {
		return nil, "", "", err
	}
	executable, err = recovery.CanonicalExistingPath(executable)
	if err != nil {
		return nil, "", "", err
	}
	generation, expectedSHA := guardGenerationIdentity(transaction, capsule)
	digest, err := recovery.ComputeReleaseDigest(executable)
	if err != nil {
		return nil, "", "", fmt.Errorf("digest detached Guard: %w", err)
	}
	if digest != expectedSHA {
		return nil, "", "", errors.New("detached Guard digest does not match exact transaction helper identity")
	}
	env, err := runtimeenv.DetachedCommandEnvironment(os.Environ())
	if err != nil {
		return nil, "", "", err
	}
	transactionRoot := filepath.Join(filepath.Dir(transaction.Paths.Target), updateTransactionDirName)
	cmd := exec.Command(executable, transactionRoot, string(transaction.Identity.TransactionID), generation)
	cmd.Env = env
	return cmd, executable, digest, nil
}

// startDetachedGuardProcess 启动子进程并重新读取其内核 process identity。
func startDetachedGuardProcess(cmd *exec.Cmd, executable, digest string) (io.ReadCloser, recovery.ProcessIdentity, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, recovery.ProcessIdentity{}, fmt.Errorf("open detached Guard readiness pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, recovery.ProcessIdentity{}, fmt.Errorf("start detached Guard: %w", err)
	}
	expected, err := captureStartedGuardIdentity(cmd, executable, digest)
	if err != nil {
		return nil, recovery.ProcessIdentity{}, errors.Join(err, stopStartedGuard(cmd))
	}
	return stdout, expected, nil
}

// awaitDetachedGuardArmed 验证 armed receipt、关闭 parent pipe，再执行 destructive action。
func awaitDetachedGuardArmed(stdout io.ReadCloser, transaction recovery.Transaction, expected recovery.ProcessIdentity, readyAction func() error) error {
	receipt, err := waitGuardReadyReceipt(stdout, guardReadinessTimeout)
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

func captureStartedGuardIdentity(cmd *exec.Cmd, executable, digest string) (recovery.ProcessIdentity, error) {
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return recovery.ProcessIdentity{}, fmt.Errorf("capture detached Guard identity: %w", err)
	}
	stablePath, err := recovery.CanonicalExistingPath(stable.ExecutableIdentity)
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

func stopStartedGuard(cmd *exec.Cmd) error {
	killErr := cmd.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		waitErr = nil
	}
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
