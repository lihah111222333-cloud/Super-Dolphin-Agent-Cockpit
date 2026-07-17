package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

// StartupMode 是 normal graph 与 frozen Recovery graph 的互斥启动模式。
type StartupMode string

const (
	// StartupModeNormal 允许进入 runtimeenv、provider、frontend 和 Fx preflight。
	StartupModeNormal StartupMode = "normal"
	// StartupModeRecovery 禁止进入 normal graph，只暴露恢复服务。
	StartupModeRecovery StartupMode = "recovery"
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
}

// StartupSelectorInput 汇总 early selector 的 journal 与 exact process contract。
type StartupSelectorInput struct {
	Store                 *recovery.Store
	Process               recovery.ProcessIdentity
	ExpectedTransactionID recovery.TransactionID
	LeaseWait             time.Duration
}

// SelectStartup 在 normal preflight 前只检查并返回唯一 active transaction 与 exact lease。
func SelectStartup(ctx context.Context, input StartupSelectorInput) (StartupSelection, error) {
	if input.Store == nil {
		return StartupSelection{}, errors.New("startup selector store is required")
	}
	transaction, found, err := discoverStartupTransaction(ctx, input)
	if err != nil {
		return recoverySelection(input.Store, transaction, err), err
	}
	if !found {
		return StartupSelection{Mode: StartupModeNormal, Store: input.Store}, nil
	}
	if err := validateStartupTransaction(ctx, transaction, input); err != nil {
		return recoverySelection(input.Store, transaction, err), err
	}
	return StartupSelection{Mode: StartupModeNormal, Store: input.Store, Transaction: transaction}, nil
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
		return recovery.Transaction{}, false, errors.New("expected probation transaction is missing")
	}
	if found && transaction.State == recovery.StateProbation && !transaction.Probation.LeasePresent && input.LeaseWait > 0 {
		transaction, err = waitForProbationLease(ctx, input)
	}
	return transaction, found, err
}

func validateStartupTransaction(ctx context.Context, transaction recovery.Transaction, input StartupSelectorInput) error {
	if input.ExpectedTransactionID != "" && transaction.Identity.TransactionID != input.ExpectedTransactionID {
		return errors.New("probation transaction identity does not match launch contract")
	}
	return validateProbationCandidate(ctx, transaction, input.Process)
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
func validateProbationCandidate(ctx context.Context, transaction recovery.Transaction, process recovery.ProcessIdentity) error {
	if transaction.State != recovery.StateProbation {
		return fmt.Errorf("active transaction state %q requires Recovery", transaction.State)
	}
	if !transaction.Probation.LeasePresent {
		return errors.New("active probation lease is missing")
	}
	if transaction.Probation.Lease.Process != process {
		return errors.New("candidate process identity does not match probation lease")
	}
	digest, err := recovery.ComputeReleaseDigestContext(ctx, transaction.Paths.Target)
	if err != nil {
		return fmt.Errorf("verify probation candidate: %w", err)
	}
	if digest != transaction.Identity.CandidateRelease.SHA256 {
		return errors.New("probation candidate release digest mismatch")
	}
	return nil
}

func recoverySelection(store *recovery.Store, transaction recovery.Transaction, cause error) StartupSelection {
	projection := projectRecoveryTransaction(transaction, cause.Error())
	return StartupSelection{Mode: StartupModeRecovery, Store: store, Transaction: transaction, Projection: projection}
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
