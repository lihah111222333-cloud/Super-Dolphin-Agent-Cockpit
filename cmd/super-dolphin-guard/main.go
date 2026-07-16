package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

const guardLeaseTTL = 30 * time.Second

var ErrCandidateProcessIdentityMismatch = pidregistry.ErrStableProcessIdentityMismatch

type guardConfig struct {
	Store             *recovery.Store
	Identity          recovery.Identity
	OwnerID           string
	Now               func() time.Time
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

// Run 阻塞到 stale probation 完成一次 CAS 接管、回滚和旧版本重启。
func (guard *probationGuard) Run(ctx context.Context) error {
	if err := guard.validate(); err != nil {
		return err
	}
	transaction, err := guard.config.Store.Load(ctx, guard.config.Identity)
	if err != nil {
		return err
	}
	ready, err := guard.takeoverReady(transaction)
	if err != nil || !ready {
		return err
	}
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

// takeoverReady 只允许带 current lease 的 probation 进入接管路径。
func (guard *probationGuard) takeoverReady(transaction recovery.Transaction) (bool, error) {
	switch transaction.State {
	case recovery.StateCommitted, recovery.StateRolledBack:
		return false, nil
	case recovery.StateProbation:
		if !transaction.Probation.LeasePresent {
			return false, errors.New("guard requires a leased probation transaction")
		}
		return true, nil
	default:
		return false, errors.New("guard requires a probation transaction")
	}
}

// validate 校验 Guard 运行所需的全部显式依赖。
func (guard *probationGuard) validate() error {
	if guard == nil || guard.config.Store == nil || guard.config.Now == nil ||
		guard.config.StopCandidate == nil || guard.config.RestartOldRelease == nil {
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

// runGuardCommand 发现唯一 active transaction 并阻塞运行 detached Guard。
func runGuardCommand(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: super-dolphin-guard <absolute-transaction-root>")
	}
	store, err := recovery.NewStore(args[0])
	if err != nil {
		return err
	}
	transaction, found, err := store.SelectActive(ctx)
	if err != nil || !found {
		return err
	}
	owner, err := recovery.NewTransactionID()
	if err != nil {
		return err
	}
	return newGuard(guardConfig{
		Store: store, Identity: transaction.Identity, OwnerID: string(owner), Now: time.Now,
		StopCandidate:     stopCandidateExact,
		RestartOldRelease: restartApplication,
	}).Run(ctx)
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
