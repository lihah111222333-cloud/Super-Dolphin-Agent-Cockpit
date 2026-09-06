package orchestrationcommand

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

var (
	ErrAttemptNotFound      = errors.New("command attempt not found")
	ErrAttemptUnresolved    = errors.New("command attempt outcome is unresolved")
	ErrAttemptTerminal      = errors.New("command attempt is terminal")
	ErrAttemptConflict      = errors.New("command attempt identity conflicts")
	ErrAttemptPermit        = errors.New("command attempt permit mismatch")
	ErrAttemptConsumed      = errors.New("command attempt permit was already consumed")
	ErrAttemptOutcome       = errors.New("command attempt outcome is invalid")
	ErrAttemptUnknownSticky = errors.New("unknown command attempt is query-only")
	ErrAttemptTransaction   = errors.New("command attempt owner requires transaction-bound dependencies")
)

// AttemptPhase is the durable record-before-effect state for one command.
type AttemptPhase string

const (
	AttemptEffectPending    AttemptPhase = "effect_pending"
	AttemptOutcomeUnknown   AttemptPhase = "outcome_unknown"
	AttemptProviderAccepted AttemptPhase = "provider_accepted"
	AttemptProviderFailed   AttemptPhase = "provider_definitive_failure"
)

// ProviderResultKind is a typed provider boundary result.  A submission or
// receipt proves only that the provider accepted a request; it is not a local
// command completion. Unknown is sticky and can only be resolved by query.
type ProviderResultKind string

const (
	ProviderResultPending ProviderResultKind = "pending"
	// ProviderResultSubmission is persisted as the accepted outcome. The
	// submission/receipt identifiers carry the provider-side distinction.
	ProviderResultSubmission ProviderResultKind = "accepted"
	ProviderResultUnknown    ProviderResultKind = "unknown"
	ProviderResultFailure    ProviderResultKind = "definitive_failure"
	// OutcomeAccepted is retained as a semantic alias for callers migrating
	// from the in-memory candidate; it still means provider submission.
	OutcomeAccepted          = ProviderResultSubmission
	OutcomeUnknown           = ProviderResultUnknown
	OutcomeDefinitiveFailure = ProviderResultFailure
)

// ProviderResult carries exactly one provider outcome variant.
type ProviderResult struct {
	Kind         ProviderResultKind `json:"kind"`
	SubmissionID string             `json:"submission_id,omitempty"`
	ReceiptID    string             `json:"receipt_id,omitempty"`
	FailureCode  string             `json:"failure_code,omitempty"`
}

// ProviderOutcome and CommandAttempt are compatibility names for callers
// that model the boundary as an outcome/attempt pair.
type ProviderOutcome = ProviderResult

// Validate 校验 provider result 只包含一个受支持的结果类别。
func (r ProviderResult) Validate() error {
	switch r.Kind {
	case ProviderResultPending:
		return validatePendingResult(r)
	case ProviderResultSubmission:
		return validateSubmissionResult(r)
	case ProviderResultUnknown:
		return validateUnknownResult(r)
	case ProviderResultFailure:
		return validateFailureResult(r)
	default:
		return fmt.Errorf("%w: unsupported provider result %q", ErrAttemptOutcome, r.Kind)
	}
}

func validatePendingResult(r ProviderResult) error {
	if r.SubmissionID != "" || r.ReceiptID != "" || r.FailureCode != "" {
		return fmt.Errorf("%w: pending cannot carry provider identifiers", ErrAttemptOutcome)
	}
	return nil
}

// validateSubmissionResult 校验 accepted 结果必须携带 provider 标识。
func validateSubmissionResult(r ProviderResult) error {
	if err := validateProviderResultText("submission_id", r.SubmissionID, true); err != nil {
		return err
	}
	if err := validateProviderResultText("receipt_id", r.ReceiptID, true); err != nil {
		return err
	}
	if strings.TrimSpace(r.SubmissionID) == "" && strings.TrimSpace(r.ReceiptID) == "" {
		return fmt.Errorf("%w: submission requires provider submission or receipt id", ErrAttemptOutcome)
	}
	if r.FailureCode != "" {
		return fmt.Errorf("%w: submission cannot carry failure code", ErrAttemptOutcome)
	}
	return nil
}

// validateUnknownResult 校验 unknown 结果不得携带任何 provider 标识。
func validateUnknownResult(r ProviderResult) error {
	for name, value := range map[string]string{"submission_id": r.SubmissionID, "receipt_id": r.ReceiptID, "failure_code": r.FailureCode} {
		if err := validateProviderResultText(name, value, true); err != nil {
			return err
		}
	}
	if r.SubmissionID != "" || r.ReceiptID != "" || r.FailureCode != "" {
		return fmt.Errorf("%w: unknown cannot carry provider identifiers", ErrAttemptOutcome)
	}
	return nil
}

func validateFailureResult(r ProviderResult) error {
	if err := validateProviderResultText("failure_code", r.FailureCode, false); err != nil {
		return err
	}
	if strings.TrimSpace(r.FailureCode) == "" || r.SubmissionID != "" || r.ReceiptID != "" {
		return fmt.Errorf("%w: definitive failure requires only failure code", ErrAttemptOutcome)
	}
	return nil
}

// validateProviderResultText 校验 provider 标识的规范文本边界。
func validateProviderResultText(name, value string, allowEmpty bool) error {
	if value == "" && allowEmpty {
		return nil
	}
	if value == "" || strings.TrimSpace(value) != value || len(value) > 256 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrAttemptOutcome, name)
	}
	return nil
}

// AttemptRequest binds a command intent to one wakeup fence. AttemptID may be
// supplied by a trusted caller; empty values are deterministically derived.
type AttemptRequest struct {
	CommandID            string `json:"command_id"`
	AttemptID            string `json:"attempt_id,omitempty"`
	WakeupID             string `json:"wakeup_id"`
	WakeupFence          string `json:"wakeup_fence"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	RequestDigest        string `json:"request_digest"`
}

// Attempt is an immutable command/attempt identity plus mutable provider
// outcome. All provider material is durable before an external call.
type Attempt struct {
	CommandID            string         `json:"command_id"`
	AttemptID            string         `json:"attempt_id"`
	WakeupID             string         `json:"wakeup_id"`
	WakeupFence          string         `json:"wakeup_fence"`
	IdempotencyNamespace string         `json:"idempotency_namespace"`
	IdempotencyKeyDigest string         `json:"idempotency_key_digest"`
	RequestDigest        string         `json:"request_digest"`
	AttemptNumber        int64          `json:"attempt_number"`
	DispatchReceipt      string         `json:"dispatch_receipt"`
	Permit               string         `json:"permit"`
	Nonce                string         `json:"nonce"`
	Phase                AttemptPhase   `json:"phase"`
	Result               ProviderResult `json:"result"`
}

// CommandAttempt is the compatibility name for the durable command attempt。
type CommandAttempt = Attempt

// DispatchPermit is the only material that a provider adapter may use for a
// call. It is valid for a provider effect only after ConsumePermit atomically
// consumes the corresponding pending attempt; it remains the exact material
// required by ReconcileUnknown after an indeterminate effect. The record keeps
// the original values because a consumed permit must never be re-issued.
type DispatchPermit struct {
	CommandID       string
	AttemptID       string
	WakeupID        string
	WakeupFence     string
	RequestDigest   string
	AttemptNumber   int64
	DispatchReceipt string
	Permit          string
	Nonce           string
}

// ProviderResultRequest binds a result to every immutable attempt field.
type ProviderResultRequest struct {
	DispatchPermit
	Result ProviderResult
}

// ProviderSubmitter 是候选 provider 边界；实现只返回 typed outcome，不携带原始正文。
type ProviderSubmitter interface {
	Submit(context.Context, DispatchPermit) (ProviderResult, error)
}

// IntentReader is the narrow command-intent dependency consumed by the
// attempt owner. It prevents the gateway from depending on command internals.
type IntentReader interface {
	Get(ctx context.Context, commandID string) (Record, error)
}

// AttemptAdmission is the explicit accepted/custody precondition for opening
// an attempt. Implementations must verify the authoritative command admission
// and custody receipt; the attempt owner never treats a missing dependency as
// an implicit admission and never turns this check into provider authority.
type AttemptAdmission interface {
	VerifyAttemptAdmission(ctx context.Context, req AttemptRequest, intent Record) error
}

// TransactionalAttemptIntentReader is the transaction-bound form of the
// intent lookup consumed while opening an attempt. Implementations must read
// from dbtx; using an independent *sql.DB connection would break the owner
// BEGIN IMMEDIATE boundary.
type TransactionalAttemptIntentReader interface {
	GetTx(ctx context.Context, dbtx storedb.DBTX, commandID string) (Record, error)
}

// TransactionalAttemptAdmission verifies custody/admission using the same
// transaction that reads the intent and inserts the attempt. A legacy
// context-only verifier is intentionally insufficient for this owner.
type TransactionalAttemptAdmission interface {
	VerifyAttemptAdmissionTx(ctx context.Context, dbtx storedb.DBTX, req AttemptRequest, intent Record) error
}
