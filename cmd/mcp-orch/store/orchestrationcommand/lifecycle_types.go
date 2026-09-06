package orchestrationcommand

import (
	"context"
	"errors"
	"math"
	"time"
)

var (
	ErrInvalidTransition  = errors.New("invalid command transition")
	ErrTransitionConflict = errors.New("command transition fence conflict")
	ErrOperationConflict  = errors.New("command operation idempotency conflict")
	ErrLifecycleNotFound  = errors.New("command lifecycle not prepared")
	ErrEvidenceRejected   = errors.New("command transition evidence rejected")
)

// Phase 是命令日志的规范阶段；任何阶段快照都不等于实际执行许可。
type Phase string

const (
	PhaseCustodyPending       Phase = "recovery_custody_pending"
	PhaseAccepted             Phase = "accepted"
	PhaseDispatchPending      Phase = "dispatch_custody_pending"
	PhaseDispatched           Phase = "dispatched"
	PhaseEffectPending        Phase = "effect_authorization_pending"
	PhaseTokenIssued          Phase = "effect_token_issued"
	PhaseEffectStarted        Phase = "external_effect_started"
	PhaseProviderAcknowledged Phase = "provider_acknowledged"
	PhaseCompleted            Phase = "completed"
	PhaseDispatchAborted      Phase = "dispatch_aborted_before_call"
	PhaseAbortedBeforeIssue   Phase = "effect_aborted_before_issue"
	PhaseAbortedBeforeStart   Phase = "effect_aborted_before_start"
	PhaseCleanupPending       Phase = "abort_cleanup_pending"
	PhaseUnknown              Phase = "outcome_unknown"
	PhaseRejected             Phase = "rejected"
	PhaseFailed               Phase = "failed"
)

// Trigger 表达已验证事实引发的转换，不隐式执行 provider 操作。
type Trigger string

const (
	TriggerPrepare              Trigger = "prepare_accept"
	TriggerAccept               Trigger = "accept"
	TriggerAbortAccept          Trigger = "abort_accept"
	TriggerBeginDispatch        Trigger = "begin_dispatch"
	TriggerReclaimUnsent        Trigger = "reclaim_unsent"
	TriggerConfirmDispatch      Trigger = "confirm_dispatch"
	TriggerRedeemEffect         Trigger = "redeem_effect"
	TriggerIssueToken           Trigger = "issue_token"
	TriggerStartEffect          Trigger = "start_effect"
	TriggerProviderAck          Trigger = "provider_ack"
	TriggerComplete             Trigger = "complete"
	TriggerMarkUnknown          Trigger = "mark_unknown"
	TriggerReconcileAck         Trigger = "reconcile_ack"
	TriggerReconcileComplete    Trigger = "reconcile_complete"
	TriggerReconcileFailed      Trigger = "reconcile_failed"
	TriggerReconcileUnstarted   Trigger = "reconcile_unstarted"
	TriggerAbortDispatch        Trigger = "abort_dispatch"
	TriggerAbortBeforeIssue     Trigger = "abort_before_issue"
	TriggerAbortBeforeStart     Trigger = "abort_before_start"
	TriggerBeginCleanup         Trigger = "begin_cleanup"
	TriggerFinishCleanup        Trigger = "finish_cleanup"
	TriggerRejectBeforeDispatch Trigger = "reject_before_dispatch"
	TriggerFailEffect           Trigger = "fail_effect"
)

// TransitionRequest 固定原操作、预期日志版本和证据引用；没有 timeout->retry 分支。
type TransitionRequest struct {
	CommandID       string  `json:"command_id"`
	OperationID     string  `json:"operation_id"`
	ExpectedVersion int64   `json:"expected_version"`
	Trigger         Trigger `json:"trigger"`
	AttemptID       string  `json:"attempt_id"`
	EvidenceRef     string  `json:"evidence_ref"`
}

// Snapshot 是某条不可变操作的历史结果；GetState 才读取最新版本，重放不冒充当前许可。
type Snapshot struct {
	CommandID        string    `json:"command_id"`
	CommandVersion   int64     `json:"command_version"`
	Version          int64     `json:"version"`
	OperationID      string    `json:"operation_id"`
	OperationDigest  string    `json:"operation_digest"`
	Phase            Phase     `json:"phase"`
	AcceptResolution string    `json:"accept_resolution"`
	AttemptID        string    `json:"attempt_id"`
	Trigger          Trigger   `json:"trigger"`
	EvidenceRef      string    `json:"evidence_ref"`
	UncertainPhase   Phase     `json:"uncertain_phase"`
	SchemaVersion    int64     `json:"schema_version"`
	RecordedAt       time.Time `json:"recorded_at"`
}

// Verification 包含回执验证必须绑定的不可变意图、前驱快照、原请求和目标阶段。
type Verification struct {
	Intent    Record            `json:"intent"`
	Current   Snapshot          `json:"current"`
	Request   TransitionRequest `json:"request"`
	NextPhase Phase             `json:"next_phase"`
}

// ReceiptVerifier 只能读取并校验已有权威回执；不能签发 permit、调用 provider 或修改命令。
// 验证必须覆盖来源/目标身份、意图摘要、attempt、操作及对应阶段的完整证明；非空引用不是证明。
type ReceiptVerifier interface {
	VerifyCommandTransition(context.Context, Verification) error
}

type verifiedTransition struct {
	Request              TransitionRequest
	Current              Snapshot
	NextPhase            Phase
	NextAcceptResolution string
	UncertainPhase       Phase
	OperationDigest      string
	StartsAttempt        bool
}

// validateTransitionRequest 拒绝不完整的操作身份、旧/溢出版本以及隐式证据。
func validateTransitionRequest(r TransitionRequest) error {
	if err := validateText("command_id", r.CommandID, 128); err != nil {
		return err
	}
	if err := validateText("operation_id", r.OperationID, 128); err != nil {
		return err
	}
	if r.ExpectedVersion <= 0 || r.ExpectedVersion == math.MaxInt64 || r.Trigger == TriggerPrepare {
		return ErrInvalidTransition
	}
	if err := validateText("evidence_ref", r.EvidenceRef, 512); err != nil {
		return err
	}
	if r.AttemptID != "" {
		return validateText("attempt_id", r.AttemptID, 128)
	}
	return nil
}
