// Package waitoperation owns the durable, non-authorizing wait-operation plan.
// It records immutable operation/target intent and append-only target facts;
// grant, wait-handle, provider, and public-response effects belong to other
// owners and are never created by this package.
package waitoperation

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
)

const (
	SchemaVersion   int64 = 1
	MaxTargetCount  int64 = 32
	MaxOperationKey       = 256
	MaxTargetKey          = 128
	maxPlanLifetime       = 60 * time.Second
)

var (
	ErrInvalidOperation          = errors.New("wait operation input is invalid")
	ErrStoreUninitialized        = errors.New("wait operation store is not initialized")
	ErrOperationNotFound         = errors.New("wait operation was not found")
	ErrTargetNotFound            = errors.New("wait operation target was not found")
	ErrReplayConflict            = errors.New("wait operation replay conflicts with the original request")
	ErrTargetConflict            = errors.New("wait operation target CAS conflicts with the current head")
	ErrDeadlineExpired           = errors.New("wait operation deadline has expired")
	ErrCommitUnknown             = errors.New("wait operation transaction commit outcome is unknown")
	ErrFenced                    = errors.New("wait operation owner is fenced query-only")
	ErrSchemaUnavailable         = errors.New("wait operation schema 136 is unavailable")
	ErrAccessOwnerRequired       = errors.New("wait operation access/wait owner is required for provisioning")
	ErrOperationStateConflict    = errors.New("wait operation state CAS conflicts with the current head")
	ErrCleanupUnknown            = errors.New("wait operation cleanup outcome is unknown")
	ErrResumeUnknown             = errors.New("wait operation resume outcome is unknown")
	ErrResumeTerminal            = errors.New("wait operation resume found a terminal resource")
	ErrStateConflict             = ErrOperationStateConflict
	ErrOperationRevisionConflict = ErrOperationStateConflict
)

// Clock supplies the authoritative UTC wall clock used for issued/recorded
// timestamps. Production wiring must inject this seam explicitly.
type Clock func() time.Time

// PlanRequest is the complete immutable operation candidate. Targets are
// expanded one-for-one into the durable target journal in target order.
type PlanRequest struct {
	OperationKey       string
	OperationDigest    string
	TargetSetDigest    string
	CallerPrincipalRef string
	ScopeRef           string
	Source             accesswait.FenceSnapshot
	DeadlineAt         time.Time
	Targets            []TargetPlan
}

// TargetPlan is the non-authorizing target candidate frozen by Plan.
type TargetPlan struct {
	Key           string
	Target        accesswait.FenceSnapshot
	Selector      accesswait.EventSelector
	AfterSequence int64
	AfterCursor   string

	// The following candidates are optional only in the sense that the owner
	// derives them deterministically when empty. Supplying one requires the
	// derived value to match, preventing a second idempotency namespace.
	OriginalGrantID    string
	GrantNamespace     string
	GrantKeyDigest     string
	GrantRequestDigest string
	WaitNamespace      string
	WaitKeyDigest      string
	WaitRequestDigest  string
}

// OperationLookup identifies one immutable operation by its exact key/digest.
type OperationLookup struct {
	Key    string
	Digest string
}

// OperationStateRequest is a fenced operation-head transition. ResolvedAt and
// UpdatedAt are deliberately absent from the successor fields: the owner
// derives both timestamps from its injected UTC clock.
type OperationStateRequest struct {
	OperationKey                 string
	OperationDigest              string
	ExpectedRevision             int64
	ExpectedStatus               string
	ExpectedUncertainPhase       string
	ExpectedCleanupRequestDigest string
	ExpectedCleanupReasonRef     string
	ExpectedResolvedAt           string
	NextStatus                   string
	NextUncertainPhase           string
	NextCleanupRequestDigest     string
	NextCleanupReasonRef         string
}

// AdvanceStateRequest is the descriptive alias used by callers of AdvanceState.
type AdvanceStateRequest = OperationStateRequest

// UpdateStateRequest is kept as an additive naming alias for owner-local callers.
type UpdateStateRequest = OperationStateRequest

// OperationStateResult is the durable operation head returned by a state CAS.
type OperationStateResult = Operation

// AdvanceStateResult is the descriptive alias used by callers of AdvanceState.
type AdvanceStateResult = OperationStateResult

// UpdateStateResult is kept as an additive naming alias for owner-local callers.
type UpdateStateResult = OperationStateResult

// Operation is the mapped operation head and its ordered latest target heads.
type Operation struct {
	OperationKey         string
	OperationDigest      string
	TargetSetDigest      string
	CallerPrincipalRef   string
	ScopeRef             string
	Source               accesswait.FenceSnapshot
	IssuedAt             time.Time
	DeadlineAt           time.Time
	TargetCount          int64
	Revision             int64
	Status               string
	UncertainPhase       string
	CleanupRequestDigest string
	CleanupReasonRef     string
	ResolvedAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	SchemaVersion        int64
	Targets              []TargetState
}

// TargetState is one immutable target journal row. Lifecycle changes are
// append-only and must use TargetTransitionRequest's expected predecessor.
type TargetState struct {
	OperationKey         string
	OperationDigest      string
	Key                  string
	TargetIndex          int64
	Version              int64
	TargetDigest         string
	RecordDigest         string
	PreviousRecordDigest string
	Target               accesswait.FenceSnapshot
	Selector             accesswait.EventSelector
	AfterSequence        int64
	AfterCursor          string
	DeadlineAt           time.Time
	OriginalGrantID      string
	GrantNamespace       string
	GrantKeyDigest       string
	GrantRequestDigest   string
	ResolvedGrantID      *string
	GrantRevision        int64
	GrantStatus          string
	WaitNamespace        string
	WaitKeyDigest        string
	WaitRequestDigest    string
	ResolvedWaitHandleID *string
	WaitRevision         int64
	WaitStatus           string
	GrantCleanupState    string
	WaitCleanupState     string
	Phase                string
	UncertainPhase       string
	ErrorCode            string
	ErrorRef             string
	ResolvedAt           *time.Time
	RecordedAt           time.Time
	SchemaVersion        int64
}

// TargetTransitionRequest is a fenced append to one target journal chain.
// Empty RecordedAt/ResolvedAt values are owner-derived from the injected UTC
// clock; no SQL update or in-place journal rewrite is permitted.
type TargetTransitionRequest struct {
	OperationKey         string
	OperationDigest      string
	TargetKey            string
	ExpectedVersion      int64
	ExpectedPhase        string
	ExpectedRecordDigest string
	RecordDigest         string
	ResolvedGrantID      *string
	GrantRevision        int64
	GrantStatus          string
	ResolvedWaitHandleID *string
	WaitRevision         int64
	WaitStatus           string
	GrantCleanupState    string
	WaitCleanupState     string
	NextPhase            string
	UncertainPhase       string
	ErrorCode            string
	ErrorRef             string
	ResolvedAt           *time.Time
	RecordedAt           time.Time
}

// Owner is the narrow wait-operation persistence surface.
type Owner interface {
	Plan(context.Context, PlanRequest) (Operation, error)
	Get(context.Context, OperationLookup) (Operation, error)
	List(context.Context, OperationLookup) ([]TargetState, error)
	AppendTarget(context.Context, TargetTransitionRequest) (TargetState, error)
	CheckSchema(context.Context) error
}

// Provisioner is the optional owner extension for durable access/wait
// provisioning. Plan-only owners remain valid Owner implementations.
type Provisioner interface {
	Owner
	ProvisionTarget(context.Context, ProvisionTargetRequest) (TargetState, error)
}

// CleanupTargetRequest identifies one terminal target and its immutable cleanup
// request. The owner reads all resource identities from the durable journal.
type CleanupTargetRequest struct {
	OperationKey         string
	OperationDigest      string
	TargetKey            string
	ExpectedVersion      int64
	ExpectedPhase        string
	ExpectedRecordDigest string
	CleanupRequestDigest string
	CleanupReasonRef     string
}

// TargetCleanupRequest is a compatibility alias for owner-local callers.
type TargetCleanupRequest = CleanupTargetRequest

// CleanupTargetResult is the durable target head returned after cleanup.
type CleanupTargetResult = TargetState

// Cleaner is the optional candidate extension for durable target cleanup.
type Cleaner interface {
	Owner
	CleanupTarget(context.Context, CleanupTargetRequest) (CleanupTargetResult, error)
}

// ResumeTargetRequest identifies an outcome-unknown target for durable
// read-back. It never carries a caller-generated resource or timestamp.
type ResumeTargetRequest struct {
	OperationKey           string
	OperationDigest        string
	TargetKey              string
	ExpectedVersion        int64
	ExpectedPhase          string
	ExpectedUncertainPhase string
	ExpectedRecordDigest   string
}

// TargetResumeRequest is a compatibility alias for owner-local callers.
type TargetResumeRequest = ResumeTargetRequest

// ResumeTargetResult is the durable target head returned after reconciliation.
type ResumeTargetResult = TargetState

// Resumer is the optional candidate extension for unknown-effect read-back.
type Resumer interface {
	Owner
	ResumeTarget(context.Context, ResumeTargetRequest) (ResumeTargetResult, error)
}

var _ Owner = (*Store)(nil)
var _ Provisioner = (*Store)(nil)
var _ Cleaner = (*Store)(nil)
var _ Resumer = (*Store)(nil)
