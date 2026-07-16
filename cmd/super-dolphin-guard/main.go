package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

const (
	guardLeaseTTL     = 30 * time.Second
	guardPollInterval = 100 * time.Millisecond
)

var ErrCandidateProcessIdentityMismatch = pidregistry.ErrStableProcessIdentityMismatch

type guardConfig struct {
	Store             *recovery.Store
	Identity          recovery.Identity
	OwnerID           string
	Now               func() time.Time
	UpdaterAlive      func(recovery.ProcessIdentity) (bool, error)
	StopCandidate     func(context.Context, recovery.ProcessIdentity) error
	RestartOldRelease func(context.Context, string) error
}

type probationGuard struct {
	config guardConfig
}

// newGuard 建立只负责单个 exact transaction 的 detached Guard。
func newGuard(config guardConfig) *probationGuard {
	return &probationGuard{config: config}
}

// Run 从 prepared 开始监督 exact updater，随后接管 stale probation。
func (guard *probationGuard) Run(ctx context.Context) error {
	if err := guard.validate(); err != nil {
		return err
	}
	for {
		transaction, err := guard.config.Store.Load(ctx, guard.config.Identity)
		if errors.Is(err, recovery.ErrTransactionBusy) {
			if err := guard.waitForNextPoll(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		finished, err := guard.superviseState(ctx, transaction)
		if errors.Is(err, recovery.ErrTransactionBusy) {
			if err := guard.waitForNextPoll(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil || finished {
			return err
		}
	}
}

func (guard *probationGuard) superviseState(ctx context.Context, transaction recovery.Transaction) (bool, error) {
	switch transaction.State {
	case recovery.StateCommitted, recovery.StateRolledBack:
		return true, nil
	case recovery.StatePrepared, recovery.StateBackupPending, recovery.StateBackupRetained, recovery.StateInstallPending:
		return guard.monitorUpdater(ctx, transaction)
	case recovery.StateProbation:
		if !transaction.Probation.LeasePresent {
			return guard.monitorUpdater(ctx, transaction)
		}
		return true, guard.takeOverProbation(ctx, transaction)
	default:
		return false, fmt.Errorf("guard cannot supervise transaction state %q", transaction.State)
	}
}

func (guard *probationGuard) takeOverProbation(ctx context.Context, transaction recovery.Transaction) error {
	if err := guard.waitUntilLeaseExpires(ctx, transaction.Probation.Lease); err != nil {
		return err
	}
	lease, active, err := guard.takeOverActive(ctx, transaction)
	if err != nil || !active {
		return err
	}
	if err := guard.stopTakenOverCandidate(ctx, transaction, lease); err != nil {
		return err
	}
	return guard.rollbackAndRestart(ctx, transaction, lease)
}

// monitorUpdater 在 probation 前只依据 exact updater process 决定等待或回滚。
func (guard *probationGuard) monitorUpdater(ctx context.Context, transaction recovery.Transaction) (bool, error) {
	if transaction.State == recovery.StateBackupPending || transaction.State == recovery.StateInstallPending {
		replayed, err := guard.config.Store.Replay(ctx, transaction.Identity)
		if errors.Is(err, recovery.ErrTransactionBusy) {
			return false, guard.waitForNextPoll(ctx)
		}
		if err != nil {
			return false, fmt.Errorf("replay pending updater intent: %w", err)
		}
		transaction = replayed
	}
	alive, err := guard.config.UpdaterAlive(transaction.Identity.UpdaterProcess)
	if err != nil {
		return false, fmt.Errorf("inspect exact updater process: %w", err)
	}
	if !alive {
		return guard.recoverAfterUpdaterDeath(ctx, transaction)
	}
	return false, guard.waitForNextPoll(ctx)
}

func (guard *probationGuard) waitForNextPoll(ctx context.Context) error {
	timer := time.NewTimer(guardPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// recoverAfterUpdaterDeath 先补完崩溃前已持久化的文件意图，再选择 exact rollback 路径。
func (guard *probationGuard) recoverAfterUpdaterDeath(ctx context.Context, transaction recovery.Transaction) (bool, error) {
	replayed, err := guard.config.Store.Replay(ctx, transaction.Identity)
	if err != nil {
		return false, fmt.Errorf("replay exact updater transaction: %w", err)
	}
	switch replayed.State {
	case recovery.StateCommitted:
		return true, nil
	case recovery.StateRolledBack:
		return true, guard.restartRolledBackRelease(ctx, replayed)
	case recovery.StateProbation:
		replayed, err = guard.config.Store.RollbackUnclaimedProbation(ctx, replayed.Identity)
	default:
		replayed, err = guard.config.Store.Rollback(ctx, replayed.Identity)
	}
	if err != nil {
		return false, err
	}
	return true, guard.restartRolledBackRelease(ctx, replayed)
}

// restartRolledBackRelease 只在 exact rollback 已完成后重启旧 target。
func (guard *probationGuard) restartRolledBackRelease(ctx context.Context, transaction recovery.Transaction) error {
	if err := guard.config.RestartOldRelease(ctx, transaction.Paths.Target); err != nil {
		return fmt.Errorf("restart rolled back release: %w", err)
	}
	return nil
}

func (guard *probationGuard) rollbackAndRestart(
	ctx context.Context,
	transaction recovery.Transaction,
	lease recovery.ProbationLease,
) error {
	rolledBack, err := guard.config.Store.RollbackClaimed(ctx, transaction.Identity, lease)
	if err != nil {
		return err
	}
	if err := guard.config.RestartOldRelease(ctx, rolledBack.Paths.Target); err != nil {
		return fmt.Errorf("restart rolled back release: %w", err)
	}
	return nil
}

func (guard *probationGuard) stopTakenOverCandidate(
	ctx context.Context,
	transaction recovery.Transaction,
	lease recovery.ProbationLease,
) error {
	if lease.Process != transaction.Probation.Lease.Process {
		return ErrCandidateProcessIdentityMismatch
	}
	if err := guard.config.StopCandidate(ctx, lease.Process); err != nil {
		return fmt.Errorf("stop exact probation candidate: %w", err)
	}
	return nil
}

// waitUntilLeaseExpires 阻塞 detached Guard，避免抢占仍健康的 updater supervisor。
func (guard *probationGuard) waitUntilLeaseExpires(ctx context.Context, lease recovery.ProbationLease) error {
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		return fmt.Errorf("parse probation lease expiry: %w", err)
	}
	delay := expiresAt.Sub(guard.config.Now())
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// validate 校验 Guard 运行所需的全部显式依赖。
func (guard *probationGuard) validate() error {
	if guard == nil || guard.config.Store == nil || guard.config.Now == nil ||
		guard.config.UpdaterAlive == nil || guard.config.StopCandidate == nil || guard.config.RestartOldRelease == nil {
		return errors.New("guard requires store, clock, exact candidate stopper, and restart callback")
	}
	if guard.config.OwnerID == "" {
		return errors.New("guard owner id is required")
	}
	return nil
}

func (guard *probationGuard) takeOverActive(
	ctx context.Context,
	transaction recovery.Transaction,
) (recovery.ProbationLease, bool, error) {
	lease, err := guard.config.Store.TakeOverProbationLease(
		ctx,
		transaction.Identity,
		transaction.Probation.Lease,
		guard.config.OwnerID,
		guard.config.Now(),
		guardLeaseTTL,
	)
	if errors.Is(err, recovery.ErrNoActiveProbation) {
		return recovery.ProbationLease{}, false, nil
	}
	if err != nil {
		return recovery.ProbationLease{}, false, err
	}
	return lease, true, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runGuardCommand(ctx, os.Args[1:]); err != nil {
		slog.Error("super-dolphin-guard failed", "error", err)
		os.Exit(1)
	}
}

// runGuardCommand 加载调用方指定的 exact transaction，发布就绪凭据后运行 Guard。
func runGuardCommand(ctx context.Context, args []string) error {
	return runGuardCommandWithWriter(ctx, args, os.Stdout)
}

// runGuardCommandWithWriter 在所有初始化成功后发布 armed receipt，并立即进入监督。
func runGuardCommandWithWriter(ctx context.Context, args []string, readyWriter io.Writer) error {
	if len(args) != 3 {
		return errors.New("usage: super-dolphin-guard <absolute-transaction-root> <transaction-id> <old|candidate>")
	}
	if readyWriter == nil {
		return errors.New("Guard readiness writer is required")
	}
	store, err := recovery.NewStore(args[0])
	if err != nil {
		return err
	}
	transaction, err := store.LoadByID(ctx, recovery.TransactionID(args[1]))
	if err != nil {
		return err
	}
	process, err := captureGuardProcess(transaction, args[2])
	if err != nil {
		return err
	}
	owner, err := recovery.NewTransactionID()
	if err != nil {
		return err
	}
	guard := newGuard(guardConfig{
		Store: store, Identity: transaction.Identity, OwnerID: string(owner), Now: time.Now,
		UpdaterAlive:      updaterAliveForTransaction(transaction),
		StopCandidate:     stopCandidateExact,
		RestartOldRelease: restartApplication,
	})
	if err := guard.validate(); err != nil {
		return err
	}
	receipt, err := recovery.EncodeGuardReadyReceipt(recovery.BuildGuardReadyReceipt(transaction, process, time.Now()))
	if err != nil {
		return err
	}
	if _, err := readyWriter.Write(receipt); err != nil {
		return fmt.Errorf("publish Guard readiness receipt: %w", err)
	}
	return guard.Run(ctx)
}

// captureGuardProcess 将当前 Guard 的 canonical path、SHA 和内核进程身份绑定到事务 helper。
func captureGuardProcess(transaction recovery.Transaction, generation string) (recovery.ProcessIdentity, error) {
	executable, err := os.Executable()
	if err != nil {
		return recovery.ProcessIdentity{}, fmt.Errorf("resolve Guard executable: %w", err)
	}
	canonical, err := recovery.CanonicalExistingPath(executable)
	if err != nil {
		return recovery.ProcessIdentity{}, err
	}
	expectedPath, expectedSHA, err := expectedGuardIdentity(transaction, generation)
	if err != nil {
		return recovery.ProcessIdentity{}, err
	}
	if canonical != expectedPath {
		return recovery.ProcessIdentity{}, fmt.Errorf("running Guard executable = %q, want %q", canonical, expectedPath)
	}
	digest, err := recovery.ComputeReleaseDigest(canonical)
	if err != nil {
		return recovery.ProcessIdentity{}, fmt.Errorf("digest running Guard: %w", err)
	}
	if digest != expectedSHA {
		return recovery.ProcessIdentity{}, errors.New("running Guard digest does not match transaction helper identity")
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		return recovery.ProcessIdentity{}, fmt.Errorf("capture Guard process identity: %w", err)
	}
	stablePath, err := recovery.CanonicalExistingPath(stable.ExecutableIdentity)
	if err != nil {
		return recovery.ProcessIdentity{}, err
	}
	if stablePath != canonical {
		return recovery.ProcessIdentity{}, errors.New("Guard kernel executable identity does not match canonical helper path")
	}
	return recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: canonical, ExecutableSHA256: digest,
	}, nil
}

func expectedGuardIdentity(transaction recovery.Transaction, generation string) (string, string, error) {
	var path, digest string
	switch generation {
	case "old":
		path = filepath.Join(transaction.Paths.RecoveryDir, "super-dolphin-guard")
		digest = transaction.Identity.OldHelpers.GuardSHA256
	case "candidate":
		path = filepath.Join(transaction.Paths.Target, "Contents", "Resources", "bin", "super-dolphin-guard")
		digest = transaction.Identity.CandidateHelpers.GuardSHA256
	default:
		return "", "", fmt.Errorf("unsupported Guard generation %q", generation)
	}
	canonical, err := recovery.CanonicalExistingPath(path)
	return canonical, digest, err
}

func updaterAliveForTransaction(transaction recovery.Transaction) func(recovery.ProcessIdentity) (bool, error) {
	return func(process recovery.ProcessIdentity) (bool, error) {
		return updaterAliveExact(process, transaction)
	}
}

// updaterAliveExact 只接受事务 target/backup 内同一 PID、启动令牌和旧 updater 摘要。
func updaterAliveExact(process recovery.ProcessIdentity, transaction recovery.Transaction) (bool, error) {
	stable, err := pidregistry.CaptureStableProcessIdentity(process.PID)
	if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stable.ProcessStartToken != process.StartToken {
		return false, nil
	}
	targetUpdater := filepath.Join(transaction.Paths.Target, "Contents", "Resources", "bin", "super-dolphin-updater")
	backupUpdater := filepath.Join(transaction.Paths.Backup, "Contents", "Resources", "bin", "super-dolphin-updater")
	if process.ExecutableIdentity != targetUpdater ||
		process.ExecutableSHA256 != transaction.Identity.OldHelpers.UpdaterSHA256 {
		return false, nil
	}
	stablePath := filepath.Clean(stable.ExecutableIdentity)
	if stablePath != targetUpdater && stablePath != backupUpdater {
		return false, nil
	}
	return trustedUpdaterArtifactExists([]string{targetUpdater, backupUpdater}, process.ExecutableSHA256)
}

func trustedUpdaterArtifactExists(paths []string, expectedSHA string) (bool, error) {
	for _, path := range paths {
		digest, err := recovery.ComputeReleaseDigest(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if digest == expectedSHA {
			return true, nil
		}
	}
	return false, nil
}

// stopCandidateExact 复用 pidregistry 内核身份，在每次信号前复核并确认 exact candidate 已退出。
func stopCandidateExact(ctx context.Context, process recovery.ProcessIdentity) error {
	return pidregistry.TerminateExactProcess(ctx, pidregistry.StableProcessIdentity{
		PID: process.PID, ProcessStartToken: process.StartToken, ExecutableIdentity: process.ExecutableIdentity,
	})
}

func restartApplication(ctx context.Context, target string) error {
	if target == "" {
		return errors.New("restart target is required")
	}
	env, err := runtimeenv.DetachedCommandEnvironment(os.Environ())
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "open", "-n", target)
	cmd.Env = env
	return cmd.Run()
}
