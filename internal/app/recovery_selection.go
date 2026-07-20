package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// StartupMode 是 normal graph 与 frozen Recovery graph 的互斥启动模式。
type StartupMode string

const (
	// StartupModeNormal 允许进入 runtimeenv、provider、frontend 和 Fx preflight。
	StartupModeNormal StartupMode = "normal"
	// StartupModeRecovery 禁止进入 normal graph，只暴露恢复服务。
	StartupModeRecovery StartupMode = "recovery"
	// StartupDigestTimeout 是生产启动摘要 helper 的唯一 deadline 配置。
	StartupDigestTimeout = 15 * time.Second
)

// RecoveryProjection 是 Recovery graph 对持久 transaction 的只读投影。
type RecoveryProjection struct {
	TransactionID   recovery.TransactionID `json:"transaction_id"`
	AttemptID       string                 `json:"attempt_id"`
	State           recovery.State         `json:"state"`
	LeasePresent    bool                   `json:"lease_present"`
	LeaseOwner      string                 `json:"lease_owner"`
	LeaseGeneration uint64                 `json:"lease_generation"`
	CandidateSHA256 string                 `json:"candidate_sha256"`
	Reason          string                 `json:"reason"`
}

// StartupSelection 保存 selector 的模式判定及唯一 journal 快照。
type StartupSelection struct {
	Mode        StartupMode
	Store       *recovery.Store
	Transaction recovery.Transaction
	Projection  RecoveryProjection
	process     recovery.ProcessIdentity
}

// StartupSelectorInput 汇总 early selector 的 journal 与 exact process contract。
type StartupSelectorInput struct {
	Store                 *recovery.Store
	Process               recovery.ProcessIdentity
	ExpectedTransactionID recovery.TransactionID
	RollbackLaunch        bool
	LeaseWait             time.Duration
	// DigestTimeout 允许测试缩短真实 timer；生产调用方必须传 StartupDigestTimeout。
	DigestTimeout time.Duration
}

// SelectStartup 在 normal preflight 前只检查并返回唯一 active transaction 与 exact lease。
func SelectStartup(ctx context.Context, input StartupSelectorInput) (StartupSelection, error) {
	if input.Store == nil {
		return StartupSelection{}, errors.New("startup selector store is required")
	}
	if input.DigestTimeout <= 0 {
		return StartupSelection{}, errors.New("startup digest timeout must be positive")
	}
	transaction, found, err := discoverStartupTransaction(ctx, input)
	if err != nil {
		return recoverySelection(input, transaction, err), err
	}
	if !found {
		return StartupSelection{Mode: StartupModeNormal, Store: input.Store, process: input.Process}, nil
	}
	if err := validateStartupTransaction(ctx, transaction, input); err != nil {
		return recoverySelection(input, transaction, err), err
	}
	return StartupSelection{
		Mode: StartupModeNormal, Store: input.Store, Transaction: transaction, process: input.Process,
	}, nil
}

// HasActiveProbation 表示 selector 已返回带 exact lease 的 candidate probation。
func (selection StartupSelection) HasActiveProbation() bool {
	return selection.Transaction.State == recovery.StateProbation &&
		selection.Transaction.Identity.TransactionID != "" &&
		selection.Transaction.Probation.LeasePresent
}

// RecordReadyACK 仅在 desktop/Fx/Wails 全部就绪后写入 exact candidate ACK。
func (selection StartupSelection) RecordReadyACK(ctx context.Context, now time.Time) error {
	if !selection.HasActiveProbation() {
		return nil
	}
	if selection.Store == nil {
		return errors.New("ready ACK store is required for active probation")
	}
	transaction := selection.Transaction
	lease := transaction.Probation.Lease
	ack := recovery.BuildHealthyACK(transaction, lease.Process, now)
	_, err := selection.Store.RecordHealthyACK(ctx, transaction.Identity, lease, ack)
	return err
}

// discoverStartupTransaction 发现唯一 active journal，并有界等待 updater 写入 lease。
func discoverStartupTransaction(ctx context.Context, input StartupSelectorInput) (recovery.Transaction, bool, error) {
	transaction, found, err := input.Store.SelectActive(ctx)
	if err != nil {
		return recovery.Transaction{}, false, err
	}
	if !found && input.ExpectedTransactionID != "" {
		transaction, err = input.Store.LoadByID(ctx, input.ExpectedTransactionID)
		return transaction, err == nil, err
	}
	if found && transaction.State == recovery.StateProbation && !transaction.Probation.LeasePresent && input.LeaseWait > 0 {
		transaction, err = waitForProbationLease(ctx, input)
	}
	return transaction, found, err
}

// validateStartupTransaction 按启动目的严格验证 probation candidate 或已激活 rollback 进程。
func validateStartupTransaction(ctx context.Context, transaction recovery.Transaction, input StartupSelectorInput) error {
	if input.ExpectedTransactionID != "" && transaction.Identity.TransactionID != input.ExpectedTransactionID {
		return errors.New("startup transaction identity does not match launch contract")
	}
	switch transaction.State {
	case recovery.StateProbation:
		if input.RollbackLaunch {
			return errors.New("rollback launch cannot target active probation")
		}
		return validateProbationCandidate(ctx, transaction, input.Process, input.DigestTimeout)
	case recovery.StateRolledBack:
		if !input.RollbackLaunch {
			return errors.New("rolled_back transaction requires authenticated rollback launch")
		}
		return recovery.ValidateActivatedRollbackLaunch(transaction, input.Process)
	default:
		return fmt.Errorf("active transaction state %q requires Recovery", transaction.State)
	}
}

// waitForProbationLease 在 candidate 先于 updater lease 落盘启动时做有界阻塞。
func waitForProbationLease(ctx context.Context, input StartupSelectorInput) (recovery.Transaction, error) {
	deadline := time.NewTimer(input.LeaseWait)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		transaction, found, err := input.Store.SelectActive(ctx)
		if err != nil {
			return transaction, err
		}
		if !found || transaction.Identity.TransactionID != input.ExpectedTransactionID {
			return transaction, errors.New("probation transaction changed while waiting for lease")
		}
		if transaction.Probation.LeasePresent {
			return transaction, nil
		}
		select {
		case <-ctx.Done():
			return transaction, context.Cause(ctx)
		case <-deadline.C:
			return transaction, errors.New("timed out waiting for probation lease")
		case <-ticker.C:
		}
	}
}

// validateProbationCandidate 校验 probation state、lease、进程及候选摘要。
func validateProbationCandidate(
	ctx context.Context,
	transaction recovery.Transaction,
	process recovery.ProcessIdentity,
	digestTimeout time.Duration,
) error {
	if transaction.State != recovery.StateProbation {
		return fmt.Errorf("active transaction state %q requires Recovery", transaction.State)
	}
	if !transaction.Probation.LeasePresent {
		return errors.New("active probation lease is missing")
	}
	if transaction.Probation.Lease.Process != process {
		return errors.New("candidate process identity does not match probation lease")
	}
	digestCtx, cancelDigest := platformconfig.WithTimeout(ctx, digestTimeout)
	defer cancelDigest()
	digest, err := recovery.ComputeReleaseDigestContext(digestCtx, transaction.Paths.Target)
	if err != nil {
		return fmt.Errorf("verify probation candidate: %w", err)
	}
	if digest != transaction.Identity.CandidateRelease.SHA256 {
		return errors.New("probation candidate release digest mismatch")
	}
	return nil
}

func recoverySelection(input StartupSelectorInput, transaction recovery.Transaction, cause error) StartupSelection {
	projection := projectRecoveryTransaction(transaction, cause.Error())
	return StartupSelection{
		Mode: StartupModeRecovery, Store: input.Store, Transaction: transaction, Projection: projection, process: input.Process,
	}
}

func projectRecoveryTransaction(transaction recovery.Transaction, reason string) RecoveryProjection {
	projection := RecoveryProjection{Reason: reason}
	if transaction.Identity.TransactionID != "" {
		projection.TransactionID = transaction.Identity.TransactionID
		projection.AttemptID = transaction.Identity.AttemptID
		projection.State = transaction.State
		projection.CandidateSHA256 = transaction.Identity.CandidateRelease.SHA256
		projection.LeasePresent = transaction.Probation.LeasePresent
		if transaction.Probation.LeasePresent {
			projection.LeaseOwner = transaction.Probation.Lease.OwnerID
			projection.LeaseGeneration = transaction.Probation.Lease.Generation
		}
	}
	return projection
}
