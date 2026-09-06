// Package room contains the transport-neutral, disabled-by-default Room candidate.
//
// This package is deliberately a contract and in-memory admission seam only. It
// does not own SQLite, RecoveryCustody, providers, Fx wiring, registries, or
// external effects. A future owner must prove the same invariants before wiring
// this candidate into a production transport.
package room

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

const SchemaVersion int64 = 1

// MaxFanoutTargets is the versioned room fan-out bound.  The room contract
// must remain bounded even while its durable owner is still disabled.
const MaxFanoutTargets = 16

const (
	StatusOpen           = "open"
	StatusClosingPending = "closing_pending"
	StatusClosed         = "closed"

	CustodyPending   = "pending"
	CustodyAvailable = "available"
	CustodyAborted   = "aborted"

	OperationCreate    = "create"
	OperationJoin      = "join"
	OperationLeave     = "leave"
	OperationGet       = "get"
	OperationClose     = "close"
	OperationExpire    = "expire"
	OperationCleanup   = "cleanup"
	OperationBroadcast = "broadcast"
	OperationRound     = "round"

	ReceiptAccepted             = "accepted"
	ReceiptProviderAcknowledged = "provider_acknowledged"
	ReceiptObservedInContext    = "observed_in_context"
	ReceiptRejected             = "rejected"
	ReceiptOutcomeUnknown       = "outcome_unknown"

	FanoutApplied  = "applied"
	FanoutPartial  = "partial"
	FanoutRejected = "rejected"
)

var (
	ErrInvalidDTO         = errors.New("room dto is invalid")
	ErrInvalidState       = errors.New("room state is invalid")
	ErrAdmissionConflict  = errors.New("room dispatch admission conflict")
	ErrOperationConflict  = errors.New("room operation conflicts with the original operation")
	ErrResolutionConflict = errors.New("room custody resolution conflicts with the winning operation")
	ErrAuthorization      = errors.New("room authorization denied")
	ErrCapabilityDisabled = errors.New("room capability is disabled")
	ErrTargetReceipt      = errors.New("room target receipt is invalid")
	ErrOutcomeUnknown     = errors.New("room outcome is unknown")
	ErrOwnerClosed        = errors.New("room owner is closed")
	ErrMemberConflict     = errors.New("room membership conflicts")
)

// Principal is produced by an authentication owner, never from tool parameters.
// GrantDigest is an opaque proof reference; no token, credential, or metadata is
// accepted by this contract.
type Principal struct {
	PrincipalRef   string `json:"principal_ref"`
	ScopeRef       string `json:"scope_ref"`
	ActorPrincipal string `json:"actor_principal"`
	IssuerBinding  string `json:"issuer_binding"`
	GrantDigest    string `json:"grant_digest"`
}

// Room is the public projection needed by Room admission and authorization.
type Room struct {
	SchemaVersion          int64  `json:"schema_version"`
	RoomID                 string `json:"room_id"`
	OwnerID                string `json:"owner_id"`
	WorkspaceID            string `json:"workspace_id"`
	RunScope               string `json:"run_scope"`
	TTLUnix                int64  `json:"ttl_unix"`
	DispatchGeneration     int64  `json:"dispatch_generation"`
	MembershipRevision     int64  `json:"membership_revision"`
	MembershipSnapshotHash string `json:"membership_snapshot_hash"`
	SharedEventCursor      int64  `json:"shared_event_cursor"`
	Status                 string `json:"status"`
	CustodyResolution      string `json:"custody_resolution"`
	CloseReason            string `json:"close_reason,omitempty"`
}

// Operation is the immutable idempotency and authorization envelope.
type Operation struct {
	SchemaVersion              int64     `json:"schema_version"`
	RoomID                     string    `json:"room_id"`
	OperationID                string    `json:"operation_id"`
	OperationVersion           int64     `json:"operation_version"`
	Kind                       string    `json:"kind"`
	ExpectedMembershipRevision int64     `json:"expected_membership_revision"`
	ExpectedDispatchGeneration int64     `json:"expected_dispatch_generation"`
	IdempotencyKeyDigest       string    `json:"idempotency_key_digest"`
	RequestDigest              string    `json:"request_digest"`
	Principal                  Principal `json:"principal"`
}

// AdmissionCandidate is a proposed single-chain transition. It is not a
// database record and cannot itself authorize a provider or command.
type AdmissionCandidate struct {
	SchemaVersion          int64  `json:"schema_version"`
	RoomID                 string `json:"room_id"`
	OperationID            string `json:"operation_id"`
	OperationVersion       int64  `json:"operation_version"`
	OperationDigest        string `json:"operation_digest"`
	ExpectedGeneration     int64  `json:"expected_generation"`
	NextState              string `json:"next_state"`
	NextCustodyResolution  string `json:"next_custody_resolution"`
	DispatchGeneration     int64  `json:"dispatch_generation"`
	PreviousInventoryRoot  string `json:"previous_inventory_root"`
	PreviousInventoryCount int64  `json:"previous_inventory_count"`
	CommandInventoryRoot   string `json:"command_inventory_root"`
	CommandInventoryCount  int64  `json:"command_inventory_count"`
}

// AdmissionHead is the in-memory SSOT projection used to test CAS semantics.
type AdmissionHead struct {
	SchemaVersion          int64  `json:"schema_version"`
	RoomID                 string `json:"room_id"`
	Generation             int64  `json:"generation"`
	State                  string `json:"state"`
	CustodyResolution      string `json:"custody_resolution"`
	OperationID            string `json:"operation_id,omitempty"`
	OperationVersion       int64  `json:"operation_version,omitempty"`
	OperationDigest        string `json:"operation_digest,omitempty"`
	DispatchGeneration     int64  `json:"dispatch_generation"`
	PreviousInventoryRoot  string `json:"previous_inventory_root"`
	PreviousInventoryCount int64  `json:"previous_inventory_count"`
	CommandInventoryRoot   string `json:"command_inventory_root"`
	CommandInventoryCount  int64  `json:"command_inventory_count"`
	ReceiptDigest          string `json:"receipt_digest,omitempty"`
}

// AdmissionResult records the one winner or a deterministic replay of it.
type AdmissionResult struct {
	SchemaVersion   int64  `json:"schema_version"`
	RoomID          string `json:"room_id"`
	Generation      int64  `json:"generation"`
	OperationID     string `json:"operation_id"`
	OperationDigest string `json:"operation_digest"`
	Winner          bool   `json:"winner"`
	AdmissionDigest string `json:"admission_digest"`
	ReceiptDigest   string `json:"receipt_digest,omitempty"`
}

// TargetReceipt is deliberately one target per item. Aggregate status never
// replaces these item outcomes.
type TargetReceipt struct {
	SchemaVersion       int64  `json:"schema_version"`
	OperationID         string `json:"operation_id,omitempty"`
	OperationDigest     string `json:"operation_digest,omitempty"`
	TargetID            string `json:"target_id"`
	TargetMappingDigest string `json:"target_mapping_digest"`
	DeliveryBranch      string `json:"delivery_branch"`
	ResourceDecision    string `json:"resource_decision"`
	Status              string `json:"status"`
	ReceiptID           string `json:"receipt_id,omitempty"`
	ReceiptDigest       string `json:"receipt_digest,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
}

// FanoutResult contains a complete, independently validated receipt for every
// planned target. Status=applied only means Room saga finalization.
type FanoutResult struct {
	SchemaVersion int64  `json:"schema_version"`
	RoomID        string `json:"room_id"`
	OperationID   string `json:"operation_id"`
	// OperationDigest is optional for compatibility with pre-binding result
	// projections; when present it must match the submitted operation digest.
	OperationDigest  string          `json:"operation_digest,omitempty"`
	Status           string          `json:"status"`
	PlannedTargetIDs []string        `json:"planned_target_ids"`
	Receipts         []TargetReceipt `json:"receipts"`
}

// Service is the future owner seam. The only implementation supplied here is
// DisabledService, which fails closed and has no mutation side effects.
type Service interface {
	Submit(context.Context, Operation) (Operation, error)
	ValidateFanout(context.Context, Operation, FanoutResult) error
}

// Arbiter serializes candidate CAS in memory. It is intentionally not durable;
// callers must not treat it as SQLite or RecoveryCustody authority.
type Arbiter struct {
	mu   sync.Mutex
	head AdmissionHead
	// admitted retains the immutable candidate so an abort-restored head can
	// still distinguish an exact replay from a mutated request.
	admitted *AdmissionCandidate
}

// NewArbiter 校验初始 head 并创建仅用于候选验证的内存单赢家仲裁器。
func NewArbiter(head AdmissionHead) (*Arbiter, error) {
	if err := head.Validate(); err != nil {
		return nil, err
	}
	return &Arbiter{head: head}, nil
}

// Head 返回当前内存 admission head 的值快照，不承诺 durable ownership。
func (a *Arbiter) Head() AdmissionHead {
	if a == nil {
		return AdmissionHead{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.head
}

// TryAdmit 以单赢家 CAS 推进 head；后续请求只能精确回放或 fail-closed。
func (a *Arbiter) TryAdmit(candidate AdmissionCandidate) (AdmissionResult, error) {
	if a == nil {
		return AdmissionResult{}, ErrAdmissionConflict
	}
	if err := candidate.Validate(); err != nil {
		return AdmissionResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if replay, handled, err := replayOrCheckHead(a.head, candidate, a.admitted); handled {
		return replay, err
	}
	admitted := candidate
	a.head = AdmissionHead{
		SchemaVersion: SchemaVersion, RoomID: a.head.RoomID, Generation: a.head.Generation + 1,
		State: candidate.NextState, CustodyResolution: candidate.NextCustodyResolution,
		OperationID: candidate.OperationID, OperationVersion: candidate.OperationVersion, OperationDigest: candidate.OperationDigest,
		DispatchGeneration: candidate.DispatchGeneration, PreviousInventoryRoot: candidate.PreviousInventoryRoot,
		PreviousInventoryCount: candidate.PreviousInventoryCount, CommandInventoryRoot: candidate.CommandInventoryRoot,
		CommandInventoryCount: candidate.CommandInventoryCount,
	}
	a.admitted = &admitted
	return admissionResult(a.head, true), nil
}

// replayOrCheckHead 执行 room、幂等键、代际和 safe-floor 的 CAS 前置检查。
func replayOrCheckHead(head AdmissionHead, candidate AdmissionCandidate, admitted ...*AdmissionCandidate) (AdmissionResult, bool, error) {
	if err := validateReplayRoom(head, candidate); err != nil {
		return AdmissionResult{}, true, err
	}
	if head.OperationID == candidate.OperationID {
		return replayAdmission(head, candidate, admitted...)
	}
	if err := validateNewAdmission(head, candidate); err != nil {
		return AdmissionResult{}, true, err
	}
	return AdmissionResult{}, false, nil
}

func validateReplayRoom(head AdmissionHead, candidate AdmissionCandidate) error {
	if head.RoomID != candidate.RoomID {
		return ErrAdmissionConflict
	}
	return nil
}

func replayAdmission(head AdmissionHead, candidate AdmissionCandidate, admitted ...*AdmissionCandidate) (AdmissionResult, bool, error) {
	if head.OperationDigest != candidate.OperationDigest || !sameAdmittedCandidate(head, candidate, admitted...) {
		return AdmissionResult{}, true, ErrOperationConflict
	}
	return admissionResult(head, false), true, nil
}

func sameAdmittedCandidate(head AdmissionHead, candidate AdmissionCandidate, admitted ...*AdmissionCandidate) bool {
	if len(admitted) > 0 && admitted[0] != nil {
		return *admitted[0] == candidate
	}
	return candidateMatchesAdmittedHead(head, candidate)
}

// validateNewAdmission 校验新 admission 与当前 head 的代际和 inventory 绑定。
func validateNewAdmission(head AdmissionHead, candidate AdmissionCandidate) error {
	if head.Generation != candidate.ExpectedGeneration || head.State != StatusOpen || !headSafe(head) {
		return ErrAdmissionConflict
	}
	if candidate.DispatchGeneration != head.DispatchGeneration || candidate.PreviousInventoryRoot != head.CommandInventoryRoot || candidate.PreviousInventoryCount != head.CommandInventoryCount {
		return ErrAdmissionConflict
	}
	return nil
}

// candidateMatchesAdmittedHead prevents a same-digest replay from changing
// the immutable admission envelope. Custody resolution does not alter the
// admission generation, state, dispatch generation, or inventory projection
// on the successful replay path, so those fields remain safe replay fences.
// candidateMatchesAdmittedHead 校验回放 candidate 未改变已冻结的 admission 包络。
func candidateMatchesAdmittedHead(head AdmissionHead, candidate AdmissionCandidate) bool {
	return candidate.ExpectedGeneration == head.Generation-1 &&
		candidate.OperationVersion == head.OperationVersion &&
		candidate.NextState == head.State &&
		candidate.DispatchGeneration == head.DispatchGeneration &&
		candidate.PreviousInventoryRoot == head.PreviousInventoryRoot &&
		candidate.PreviousInventoryCount == head.PreviousInventoryCount &&
		candidate.CommandInventoryRoot == head.CommandInventoryRoot &&
		candidate.CommandInventoryCount == head.CommandInventoryCount
}

// headSafe 判断 head 是否处于可接受新 operation 的 safe floor。
func headSafe(head AdmissionHead) bool {
	return head.CustodyResolution == CustodyAvailable || head.CustodyResolution == CustodyAborted
}

// Resolve 为 pending head 安装唯一 custody winner；abort 恢复前一 inventory 且不触发外部副作用。
func (a *Arbiter) Resolve(operationID, operationDigest, resolution, receiptDigest string) error {
	if a == nil {
		return ErrResolutionConflict
	}
	if err := validateResolutionInput(operationID, operationDigest, resolution, receiptDigest); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !resolutionMatches(a.head, operationID, operationDigest) {
		return ErrResolutionConflict
	}
	applyResolution(&a.head, resolution, receiptDigest)
	return nil
}

// validateResolutionInput 校验单赢家 resolution 的不透明 receipt 绑定。
func validateResolutionInput(operationID, operationDigest, resolution, receiptDigest string) error {
	if operationID == "" || operationDigest == "" || resolution == CustodyPending {
		return ErrResolutionConflict
	}
	if err := validateText(ErrResolutionConflict, "operation_id", operationID, 256); err != nil {
		return err
	}
	if err := validateDigest(ErrResolutionConflict, "operation_digest", operationDigest); err != nil {
		return err
	}
	if err := validateDigest(ErrResolutionConflict, "receipt_digest", receiptDigest); err != nil {
		return err
	}
	if resolution != CustodyAvailable && resolution != CustodyAborted {
		return ErrResolutionConflict
	}
	return nil
}

// resolutionMatches 确认 resolution 仍针对唯一 pending winner。
func resolutionMatches(head AdmissionHead, operationID, operationDigest string) bool {
	return head.CustodyResolution == CustodyPending && head.OperationID == operationID && head.OperationDigest == operationDigest
}

// applyResolution 应用 receipt winner；abort 分支恢复冻结前 inventory。
func applyResolution(head *AdmissionHead, resolution, receiptDigest string) {
	head.CustodyResolution = resolution
	head.ReceiptDigest = receiptDigest
	if resolution == CustodyAborted {
		head.State = StatusOpen
		head.CommandInventoryRoot = head.PreviousInventoryRoot
		head.CommandInventoryCount = head.PreviousInventoryCount
	}
}

func admissionResult(head AdmissionHead, winner bool) AdmissionResult {
	return AdmissionResult{SchemaVersion: SchemaVersion, RoomID: head.RoomID, Generation: head.Generation, OperationID: head.OperationID, OperationDigest: head.OperationDigest, Winner: winner, AdmissionDigest: head.OperationDigest, ReceiptDigest: head.ReceiptDigest}
}

// validateText 统一校验 DTO 文本的 canonical UTF-8 和长度边界。
func validateText(base error, name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", base, name)
	}
	return nil
}
