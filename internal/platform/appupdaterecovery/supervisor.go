package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const defaultSupervisorPollInterval = 100 * time.Millisecond

// ProbationSupervisorConfig 注入 supervisor 的 exact lease、进程探针和恢复副作用。
type ProbationSupervisorConfig struct {
	Store             *Store
	Identity          Identity
	Lease             ProbationLease
	ProcessAlive      func(ProcessIdentity) (bool, error)
	StopCandidate     func(context.Context, ProcessIdentity) error
	RestartOldRelease func(context.Context, Transaction) error
	ObservationPeriod time.Duration
	PollInterval      time.Duration
	Now               func() time.Time
}

// ProbationSupervisor 以阻塞 Run(ctx) 生命周期监督一个 exact probation。
type ProbationSupervisor struct {
	config ProbationSupervisorConfig
}

// NewProbationSupervisor 校验 supervisor 依赖，不允许缺失探针或恢复动作。
func NewProbationSupervisor(config ProbationSupervisorConfig) (*ProbationSupervisor, error) {
	if err := validateProbationSupervisorConfig(config); err != nil {
		return nil, err
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultSupervisorPollInterval
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ProbationSupervisor{config: config}, nil
}

// validateProbationSupervisorConfig 校验 supervisor 的 exact lease、回滚副作用与时间边界。
func validateProbationSupervisorConfig(config ProbationSupervisorConfig) error {
	if config.Store == nil {
		return errors.New("probation supervisor store is required")
	}
	if err := validateIdentity(config.Identity); err != nil {
		return err
	}
	if err := validatePersistedLease(config.Lease); err != nil {
		return err
	}
	if config.ProcessAlive == nil || config.StopCandidate == nil || config.RestartOldRelease == nil {
		return errors.New("probation supervisor process probe, exact stopper, and restart action are required")
	}
	if config.ObservationPeriod <= 0 {
		return errors.New("probation observation period must be positive")
	}
	if config.PollInterval < 0 {
		return errors.New("probation poll interval cannot be negative")
	}
	return nil
}

// Run 阻塞直到 healthy commit、exact rollback 或 ctx 取消。
func (supervisor *ProbationSupervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("probation supervisor context is required")
	}
	ticker := time.NewTicker(supervisor.config.PollInterval)
	defer ticker.Stop()
	for {
		done, err := supervisor.inspect(ctx)
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// inspect 校验持久状态和 current lease，再进入单轮 probation 监督。
func (supervisor *ProbationSupervisor) inspect(ctx context.Context) (bool, error) {
	transaction, err := supervisor.config.Store.Load(ctx, supervisor.config.Identity)
	if err != nil {
		return false, err
	}
	switch transaction.State {
	case StateCommitted, StateRolledBack:
		return true, nil
	case StateProbation:
	default:
		return false, fmt.Errorf("probation supervisor observed state %q", transaction.State)
	}
	if !transaction.Probation.LeasePresent || transaction.Probation.Lease != supervisor.config.Lease {
		return false, ErrProbationLeaseMismatch
	}
	return supervisor.inspectProcess(ctx, transaction)
}

// inspectProcess 探测 exact candidate；崩溃或探针失败都先回滚再重启旧 release。
func (supervisor *ProbationSupervisor) inspectProcess(ctx context.Context, transaction Transaction) (bool, error) {
	alive, probeErr := supervisor.config.ProcessAlive(supervisor.config.Lease.Process)
	if probeErr != nil {
		return true, errors.Join(probeErr, supervisor.rollbackAndRestart(ctx))
	}
	if !alive {
		return true, supervisor.rollbackAndRestart(ctx)
	}
	return supervisor.inspectACKAndDeadline(ctx, transaction)
}

// inspectACKAndDeadline 在 observation 完成后提交，或在 lease 到期后回滚。
func (supervisor *ProbationSupervisor) inspectACKAndDeadline(ctx context.Context, transaction Transaction) (bool, error) {
	if transaction.Probation.ACKPresent {
		ready, err := supervisor.observationComplete(transaction.Probation.ACK)
		if err != nil {
			return false, err
		}
		if ready {
			_, err := supervisor.config.Store.commitHealthyClaimed(ctx, supervisor.config.Identity, supervisor.config.Lease)
			return true, err
		}
	}
	expiresAt, err := parseProbationTime("lease expiry", supervisor.config.Lease.ExpiresAt)
	if err != nil {
		return false, err
	}
	if !supervisor.config.Now().UTC().Before(expiresAt) {
		return true, supervisor.rollbackAndRestart(ctx)
	}
	return false, nil
}

func (supervisor *ProbationSupervisor) observationComplete(ack HealthyACK) (bool, error) {
	ackAt, err := parseProbationTime("ACK", ack.AcknowledgedAt)
	if err != nil {
		return false, err
	}
	readyAt := ackAt.Add(supervisor.config.ObservationPeriod)
	return !supervisor.config.Now().UTC().Before(readyAt), nil
}

func (supervisor *ProbationSupervisor) rollbackAndRestart(ctx context.Context) error {
	if err := supervisor.config.StopCandidate(ctx, supervisor.config.Lease.Process); err != nil {
		return fmt.Errorf("stop exact probation candidate: %w", err)
	}
	transaction, err := supervisor.config.Store.RollbackClaimed(ctx, supervisor.config.Identity, supervisor.config.Lease)
	if err != nil {
		return err
	}
	return supervisor.config.RestartOldRelease(ctx, transaction)
}
