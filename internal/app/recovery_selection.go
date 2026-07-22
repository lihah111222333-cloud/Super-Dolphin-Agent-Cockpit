package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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

// RecoveryPublicErrorCode 是 Recovery 浏览器边界允许的稳定错误码。
type RecoveryPublicErrorCode string

const (
	// RecoveryPublicCodeStartupFailed 表示启动前检查未完成。
	RecoveryPublicCodeStartupFailed RecoveryPublicErrorCode = "RECOVERY_STARTUP_FAILED"
	// RecoveryPublicCodeStateFailed 表示无法刷新 Recovery 状态。
	RecoveryPublicCodeStateFailed RecoveryPublicErrorCode = "RECOVERY_STATE_FAILED"
	// RecoveryPublicCodeCheckFailed 表示候选版本检查失败。
	RecoveryPublicCodeCheckFailed RecoveryPublicErrorCode = "RECOVERY_CHECK_FAILED"
	// RecoveryPublicCodeRetryFailed 表示事务重放失败。
	RecoveryPublicCodeRetryFailed RecoveryPublicErrorCode = "RECOVERY_RETRY_FAILED"
	// RecoveryPublicCodeRestoreFailed 表示回滚恢复失败。
	RecoveryPublicCodeRestoreFailed RecoveryPublicErrorCode = "RECOVERY_RESTORE_FAILED"
	// RecoveryPublicCodeUnknownFailure 表示公开错误编码未能完成。
	RecoveryPublicCodeUnknownFailure RecoveryPublicErrorCode = "RECOVERY_UNKNOWN_FAILURE"

	recoveryReasonWireSeparator     = "|"
	recoveryFallbackDiagnosticInput = "recovery public error encoding failed"
)

// RecoveryPublicFailure 是可跨 Recovery 浏览器边界的受控错误描述。
type RecoveryPublicFailure struct {
	Code          RecoveryPublicErrorCode
	PublicMessage string
	DiagnosticID  string
}

// NewRecoveryPublicFailure 从原始服务端错误派生白名单错误码和不透明诊断标识；无效输入显式返回错误。
func NewRecoveryPublicFailure(code RecoveryPublicErrorCode, cause error) (RecoveryPublicFailure, error) {
	if cause == nil {
		return RecoveryPublicFailure{}, errors.New("Recovery public failure cause is required")
	}
	publicMessage, ok := recoveryPublicMessage(code)
	if !ok {
		return RecoveryPublicFailure{}, errors.New("Recovery public failure code is not allowlisted")
	}
	return newRecoveryPublicFailure(code, publicMessage, cause.Error()), nil
}

// WireValue 返回 Reason 字段的安全 code|diagnosticID 表示。
func (failure RecoveryPublicFailure) WireValue() string {
	return string(failure.Code) + recoveryReasonWireSeparator + failure.DiagnosticID
}

// RecoveryFallbackFailure 为公开错误编码失败的阻断响应创建固定的白名单错误，不包含原始原因。
func RecoveryFallbackFailure() RecoveryPublicFailure {
	publicMessage, _ := recoveryPublicMessage(RecoveryPublicCodeUnknownFailure)
	return newRecoveryPublicFailure(RecoveryPublicCodeUnknownFailure, publicMessage, recoveryFallbackDiagnosticInput)
}

// NormalizeRecoveryReason 把任意历史或运行时文本收敛为安全的 Recovery reason wire 值。
func NormalizeRecoveryReason(reason string) string {
	if reason == "" {
		return ""
	}
	if code, diagnosticID, ok := parseRecoveryReason(reason); ok {
		return string(code) + recoveryReasonWireSeparator + diagnosticID
	}
	publicMessage, _ := recoveryPublicMessage(RecoveryPublicCodeStartupFailed)
	return newRecoveryPublicFailure(RecoveryPublicCodeStartupFailed, publicMessage, reason).WireValue()
}

// newRecoveryPublicFailure 丢弃原始文本，只保留白名单文案与可关联的 SHA-256 诊断标识。
func newRecoveryPublicFailure(code RecoveryPublicErrorCode, publicMessage string, raw string) RecoveryPublicFailure {
	sum := sha256.Sum256([]byte(raw))
	return RecoveryPublicFailure{
		Code:          code,
		PublicMessage: publicMessage,
		DiagnosticID:  hex.EncodeToString(sum[:]),
	}
}

// recoveryPublicMessage 只返回审核过的公开文案；调用方对未知码显式返回错误。
func recoveryPublicMessage(code RecoveryPublicErrorCode) (string, bool) {
	switch code {
	case RecoveryPublicCodeStartupFailed:
		return "Recovery mode started because the previous startup did not complete.", true
	case RecoveryPublicCodeStateFailed:
		return "Recovery state could not be loaded. Please restart Recovery.", true
	case RecoveryPublicCodeCheckFailed:
		return "Recovery check could not be completed. You can retry or restore the previous release.", true
	case RecoveryPublicCodeRetryFailed:
		return "Recovery retry could not be completed. You can retry or restore the previous release.", true
	case RecoveryPublicCodeRestoreFailed:
		return "Recovery restore could not be completed. Review diagnostics before trying again.", true
	case RecoveryPublicCodeUnknownFailure:
		return "Recovery action could not be completed safely. Review the diagnostic ID before trying again.", true
	default:
		return "", false
	}
}

// parseRecoveryReason 只保留已验证的安全 wire 值，供刷新后的 transaction 投影复用诊断标识。
func parseRecoveryReason(value string) (RecoveryPublicErrorCode, string, bool) {
	codeValue, diagnosticID, found := strings.Cut(value, recoveryReasonWireSeparator)
	code := RecoveryPublicErrorCode(codeValue)
	if !found || !isKnownRecoveryPublicCode(code) || !isRecoveryDiagnosticID(diagnosticID) {
		return "", "", false
	}
	return code, diagnosticID, true
}

// isKnownRecoveryPublicCode 保持 reason 只能复用已发布的稳定码，未知码必须重新归类。
func isKnownRecoveryPublicCode(code RecoveryPublicErrorCode) bool {
	switch code {
	case RecoveryPublicCodeStartupFailed, RecoveryPublicCodeStateFailed, RecoveryPublicCodeCheckFailed,
		RecoveryPublicCodeRetryFailed, RecoveryPublicCodeRestoreFailed:
		return true
	default:
		return false
	}
}

// isRecoveryDiagnosticID 拒绝自由文本，确保诊断标识始终是固定长度的小写 SHA-256。
func isRecoveryDiagnosticID(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

// RecoveryAction 复用 contract 中面向 Recovery UI 的稳定动作枚举。
type RecoveryAction = contract.RecoveryAction
// RecoveryFailure 复用 contract 中可安全展示的结构化恢复失败元数据。
type RecoveryFailure = contract.RecoveryFailure

const (
	RecoveryActionWaitThenRetry                  = contract.RecoveryActionWaitThenRetry
	RecoveryActionRestartApplication             = contract.RecoveryActionRestartApplication
	RecoveryActionPreserveStateExportDiagnostics = contract.RecoveryActionPreserveStateExportDiagnostics
)

var (
	ErrUpdateTransactionAmbiguous = contract.ErrUpdateTransactionAmbiguous
	ErrUpdateSignatureInvalid     = contract.ErrUpdateSignatureInvalid
	ErrUpdateIntegrityInvalid     = contract.ErrUpdateIntegrityInvalid
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
	Failure     RecoveryFailure
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
		selection, publicFailureErr := recoverySelection(input, transaction, err)
		if publicFailureErr != nil {
			return StartupSelection{}, publicFailureErr
		}
		return selection, err
	}
	if !found {
		return StartupSelection{Mode: StartupModeNormal, Store: input.Store, process: input.Process}, nil
	}
	if err := validateStartupTransaction(ctx, transaction, input); err != nil {
		selection, publicFailureErr := recoverySelection(input, transaction, err)
		if publicFailureErr != nil {
			return StartupSelection{}, publicFailureErr
		}
		return selection, err
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
		return fmt.Errorf("%w: probation candidate release digest mismatch", contract.ErrUpdateIntegrityInvalid)
	}
	return nil
}

// recoverySelection 用公开失败 envelope 投影 Recovery 状态，并将编码不变量失败显式返回给调用方。
func recoverySelection(input StartupSelectorInput, transaction recovery.Transaction, cause error) (StartupSelection, error) {
	failure, err := NewRecoveryPublicFailure(RecoveryPublicCodeStartupFailed, cause)
	if err != nil {
		return StartupSelection{}, err
	}
	projection := projectRecoveryTransaction(transaction, failure.WireValue())
	structuredFailure := RecoveryFailureForError(cause, transaction.Identity.TransactionID)
	return StartupSelection{
		Mode: StartupModeRecovery, Store: input.Store, Transaction: transaction, Projection: projection, Failure: structuredFailure, process: input.Process,
	}, nil
}

// RecoveryFailureForError 将已知恢复错误映射为结构化且可安全展示的失败元数据。
func RecoveryFailureForError(err error, transactionID recovery.TransactionID) RecoveryFailure {
	switch {
	case errors.Is(err, contract.ErrUpdateTransactionAmbiguous):
		failure, _ := contract.RecoveryFailureForCode("UPDATE_TRANSACTION_AMBIGUOUS", string(transactionID))
		return failure
	case errors.Is(err, contract.ErrUpdateSignatureInvalid):
		failure, _ := contract.RecoveryFailureForCode("UPDATE_SIGNATURE_INVALID", string(transactionID))
		return failure
	case errors.Is(err, contract.ErrUpdateIntegrityInvalid):
		failure, _ := contract.RecoveryFailureForCode("UPDATE_INTEGRITY_INVALID", string(transactionID))
		return failure
	}
	return RecoveryFailure{}
}

// RecoveryReasonForFailure 返回既有调用方可用的稳定安全失败文案。
func RecoveryReasonForFailure(code string) string {
	switch code {
	case "UPDATE_TRANSACTION_AMBIGUOUS":
		return "Update transaction state is ambiguous; recovery state was preserved."
	case "UPDATE_SIGNATURE_INVALID":
		return "Update signature verification failed; recovery state was preserved."
	case "UPDATE_INTEGRITY_INVALID":
		return "Update integrity verification failed; recovery state was preserved."
	default:
		return "Recovery action is required; state was preserved."
	}
}

// projectRecoveryTransaction 将事务快照和安全 reason 编码为 Recovery 图的只读投影。
func projectRecoveryTransaction(transaction recovery.Transaction, reason string) RecoveryProjection {
	projection := RecoveryProjection{Reason: NormalizeRecoveryReason(reason)}
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
