package appupdaterecovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
)

const (
	rollbackLaunchTokenBytes        = 32
	rollbackRestartDeadline         = 15 * time.Second
	rollbackRestartLockPollInterval = 10 * time.Millisecond
)

// RollbackRestartControl 保存 ACK 前 cleanup ownership 与 ACK 后 authenticated COMMIT。
type RollbackRestartControl struct {
	Process RollbackRestartProcess
	Cleanup RollbackRestartCleanup
	Commit  func(context.Context) error
}

// RollbackRestartResolver 重发现已携带 exact launch token 的旧版本进程并移交控制权。
type RollbackRestartResolver func(context.Context, string) (RollbackRestartControl, bool, error)

// RollbackRestartLauncher 启动携带 exact launch token 的旧版本进程。
type RollbackRestartLauncher func(context.Context, string) (RollbackRestartControl, error)

// RollbackRestartCleanup 认证终止已启动但未能持久 ACK 的旧版本进程。
type RollbackRestartCleanup func() error

// ConvergeRollbackRestart 在 transaction lock 内重发现或启动旧版本并持久化 ACK。
func (store *Store) ConvergeRollbackRestart(
	ctx context.Context,
	identity Identity,
	resolve RollbackRestartResolver,
	launch RollbackRestartLauncher,
) (Transaction, error) {
	if resolve == nil || launch == nil {
		return Transaction{}, errors.New("rollback restart resolver and launcher are required")
	}
	deadlineCtx, cancel := ctxutil.WithTimeout(ctx, rollbackRestartDeadline)
	defer cancel()
	return store.withRollbackRestartConvergence(deadlineCtx, identity, func(journal *journalPayload) error {
		return store.convergeRollbackRestartLocked(deadlineCtx, journal, resolve, launch)
	})
}

func (store *Store) convergeRollbackRestartLocked(
	ctx context.Context,
	journal *journalPayload,
	resolve RollbackRestartResolver,
	launch RollbackRestartLauncher,
) error {
	transaction := journal.transaction()
	if transaction.State != StateRolledBack {
		return fmt.Errorf("rollback restart from state %q: illegal transition", transaction.State)
	}
	if transaction.RollbackRestart.ACKPresent {
		return commitACKedRollbackRestart(ctx, transaction, resolve)
	}
	control, err := acquireRollbackRestartProcess(ctx, transaction, resolve, launch)
	if err != nil {
		return err
	}
	return store.persistRollbackRestartACK(ctx, journal, transaction, control)
}

// acquireRollbackRestartProcess 优先重发现 exact token 进程，仅在不存在时启动并移交 cleanup ownership。
func acquireRollbackRestartProcess(
	ctx context.Context,
	transaction Transaction,
	resolve RollbackRestartResolver,
	launch RollbackRestartLauncher,
) (RollbackRestartControl, error) {
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartControl{}, err
	}
	control, found, err := acquireResolvedRollbackRestart(ctx, transaction.RollbackRestart.LaunchToken, resolve)
	if err != nil {
		return RollbackRestartControl{}, err
	}
	if found {
		return control, nil
	}
	control, err = launch(ctx, transaction.RollbackRestart.LaunchToken)
	if err != nil {
		return RollbackRestartControl{}, fmt.Errorf("launch rolled back release: %w", err)
	}
	if err := validateRollbackRestartControl(control); err != nil {
		return RollbackRestartControl{}, cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return RollbackRestartControl{}, cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	return control, nil
}

// acquireResolvedRollbackRestart 校验 resolver 的 cleanup ownership，并收口返回点取消窗口。
func acquireResolvedRollbackRestart(
	ctx context.Context,
	token string,
	resolve RollbackRestartResolver,
) (RollbackRestartControl, bool, error) {
	control, found, err := resolve(ctx, token)
	if err != nil {
		return RollbackRestartControl{}, false, fmt.Errorf("resolve rollback restart launch token: %w", err)
	}
	if found {
		if err := validateRollbackRestartControl(control); err != nil {
			return RollbackRestartControl{}, false, cleanupRollbackRestartLaunch(control.Cleanup, err)
		}
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		if !found {
			return RollbackRestartControl{}, false, err
		}
		return RollbackRestartControl{}, false, cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	return control, found, nil
}

func (store *Store) persistRollbackRestartACK(
	ctx context.Context,
	journal *journalPayload,
	transaction Transaction,
	control RollbackRestartControl,
) error {
	if err := validateRollbackRestartControl(control); err != nil {
		return cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	updated := *journal
	updated.RollbackRestart.ACKPresent = true
	updated.RollbackRestart.ACK = RollbackRestartACK{
		LaunchToken:    transaction.RollbackRestart.LaunchToken,
		Process:        control.Process,
		AcknowledgedAt: store.now().UTC().Format(time.RFC3339Nano),
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	if err := store.writeLocked(updated); err != nil {
		return cleanupRollbackRestartLaunch(control.Cleanup, err)
	}
	*journal = updated
	return control.Commit(ctx)
}

// commitACKedRollbackRestart 只对 durable ACK 绑定的 exact process 幂等重放 COMMIT。
func commitACKedRollbackRestart(ctx context.Context, transaction Transaction, resolve RollbackRestartResolver) error {
	if err := rollbackRestartContextError(ctx); err != nil {
		return err
	}
	control, found, err := resolve(ctx, transaction.RollbackRestart.LaunchToken)
	if err != nil {
		return fmt.Errorf("resolve ACKed rollback restart launch token: %w", err)
	}
	if !found {
		return errors.New("ACKed rollback restart process is missing before COMMIT")
	}
	if err := validateRollbackRestartControl(control); err != nil {
		return err
	}
	if control.Process != transaction.RollbackRestart.ACK.Process {
		return errors.New("ACKed rollback restart process identity changed")
	}
	if err := rollbackRestartContextError(ctx); err != nil {
		return err
	}
	return control.Commit(ctx)
}

func validateRollbackRestartControl(control RollbackRestartControl) error {
	if err := validateRollbackRestartProcess(control.Process); err != nil {
		return err
	}
	if control.Cleanup == nil || control.Commit == nil {
		return errors.New("rollback restart cleanup and COMMIT controls are required")
	}
	return nil
}

func rollbackRestartContextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
		return nil
	}
}

func cleanupRollbackRestartLaunch(cleanup RollbackRestartCleanup, primary error) error {
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, cleanup())
}

func (store *Store) withRollbackRestartConvergence(
	ctx context.Context,
	identity Identity,
	action func(*journalPayload) error,
) (Transaction, error) {
	ticker := time.NewTicker(rollbackRestartLockPollInterval)
	defer ticker.Stop()
	for {
		transaction, err := store.withExact(ctx, identity, action)
		if !errors.Is(err, ErrTransactionBusy) {
			return transaction, err
		}
		select {
		case <-ctx.Done():
			return Transaction{}, context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

func (store *Store) beginRollbackRestartIntentLocked(journal *journalPayload) error {
	if journal.RollbackRestart.IntentPresent {
		return nil
	}
	token, err := newRollbackLaunchToken()
	if err != nil {
		return err
	}
	journal.RollbackRestart = RollbackRestartRecord{
		IntentPresent: true,
		LaunchToken:   token,
		IntentAt:      store.now().UTC().Format(time.RFC3339Nano),
	}
	return nil
}

func newRollbackLaunchToken() (string, error) {
	raw := make([]byte, rollbackLaunchTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate rollback restart launch token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// validateRollbackRestartRecord 校验 rollback restart intent 与 transaction 状态一致。
func validateRollbackRestartRecord(transaction Transaction) error {
	record := transaction.RollbackRestart
	requiresIntent := transaction.State == StateRollbackPending || transaction.State == StateRolledBack
	if !record.IntentPresent {
		return validateAbsentRollbackRestart(record, requiresIntent)
	}
	if !requiresIntent {
		return fmt.Errorf("rollback restart intent is invalid in state %q", transaction.State)
	}
	if !validLowerHex(record.LaunchToken, rollbackLaunchTokenBytes*2) {
		return errors.New("rollback restart launch token must be 64 lowercase hex characters")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.IntentAt); err != nil {
		return errors.New("rollback restart intent_at must be RFC3339Nano")
	}
	return validateRollbackRestartACK(transaction.State, record)
}

func validateAbsentRollbackRestart(record RollbackRestartRecord, requiresIntent bool) error {
	if requiresIntent {
		return errors.New("rollback state requires durable restart intent")
	}
	if record != (RollbackRestartRecord{}) {
		return errors.New("rollback restart record is partial")
	}
	return nil
}

// validateRollbackRestartACK 校验持久 ACK 仅绑定 rolled_back 状态及原 launch token。
func validateRollbackRestartACK(state State, record RollbackRestartRecord) error {
	if !record.ACKPresent {
		if record.ACK != (RollbackRestartACK{}) {
			return errors.New("rollback restart ACK must be zero when absent")
		}
		return nil
	}
	if state != StateRolledBack || record.ACK.LaunchToken != record.LaunchToken {
		return errors.New("rollback restart ACK identity mismatch")
	}
	if err := validateRollbackRestartProcess(record.ACK.Process); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, record.ACK.AcknowledgedAt); err != nil {
		return errors.New("rollback restart acknowledged_at must be RFC3339Nano")
	}
	return nil
}

func validateRollbackRestartProcess(process RollbackRestartProcess) error {
	if process.PID <= 1 || strings.TrimSpace(process.StartToken) == "" ||
		strings.TrimSpace(process.ExecutableIdentity) == "" {
		return errors.New("rollback restart process identity is incomplete")
	}
	if err := validateDigest("rollback restart executable", process.ExecutableSHA256); err != nil {
		return err
	}
	return nil
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
