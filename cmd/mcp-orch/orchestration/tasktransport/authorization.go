package tasktransport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// This file is a transport-neutral authorization candidate. It validates
// server-owned snapshots only; it never persists a grant, allocates a worker,
// reads an event source, or invokes a provider.

const (
	AuthorizationSchemaVersion = 1
	AuthorizationDecisionKind  = "structural_preflight"

	ActionDiscover Action = "discover"
	ActionRead     Action = "read"
	ActionSend     Action = "send"

	ParentChildRelation ParentRelation = "parent_child"
	PeerRelation        ParentRelation = "peer"
)

var (
	ErrAuthorizationInvalid = errors.New("task authorization claim is invalid")
	ErrUnauthorized         = errors.New("task authorization denied")
	ErrCallerUnavailable    = errors.New("authenticated task caller is unavailable")
	ErrGrantActionMismatch  = errors.New("grant action does not match request")
	ErrGrantRevoked         = errors.New("task authorization grant is revoked")
	ErrGrantExpired         = errors.New("task authorization grant is expired")
	ErrGrantNotYetValid     = errors.New("task authorization grant is not yet valid")
	ErrRevocationConflict   = errors.New("task authorization revocation conflicts")
	ErrParentMismatch       = errors.New("task authorization parent binding does not match")
	ErrCursorMismatch       = errors.New("task authorization cursor does not match")
)

// Action is the closed action discriminator for independent discover/read/send
// grants. A grant for one action cannot be reused for another action.
type Action string

// ParentRelation distinguishes an explicit parent-child binding from a peer
// relation; neither relation is inferred from task IDs or directory layout.
type ParentRelation string

// CallerBinding is a server-authenticated task caller snapshot. The source
// fence and issuer are copied from common's private attribution context.
type CallerBinding struct {
	PrincipalRef  string                `json:"principal_ref"`
	AuthContextID string                `json:"auth_context_id"`
	ScopeRef      string                `json:"scope_ref"`
	Profile       string                `json:"profile"`
	TaskID        string                `json:"task_id"`
	SessionID     string                `json:"session_id"`
	BootEpoch     string                `json:"boot_epoch"`
	Source        common.ExecutionFence `json:"source"`
	Issuer        common.IssuerBinding  `json:"issuer"`
	ExpiresAt     time.Time             `json:"expires_at"`
}

// ParentBinding is the explicit source/target relationship carried by a grant.
// ParentEvidenceRef is opaque evidence; this candidate does not verify it.
type ParentBinding struct {
	SchemaVersion     int                   `json:"schema_version"`
	Relation          ParentRelation        `json:"relation"`
	ParentTaskID      string                `json:"parent_task_id"`
	ChildTaskID       string                `json:"child_task_id"`
	ParentScopeRef    string                `json:"parent_scope_ref"`
	ChildScopeRef     string                `json:"child_scope_ref"`
	ParentFence       common.ExecutionFence `json:"parent_fence"`
	ChildFence        common.ExecutionFence `json:"child_fence"`
	ParentEvidenceRef string                `json:"parent_evidence_ref"`
	BindingDigest     string                `json:"binding_digest"`
}

// GrantRef is the minimal immutable reference used by request and cursor
// claims. Revision and revocation epoch are both required replay fences.
type GrantRef struct {
	SchemaVersion   int    `json:"schema_version"`
	GrantID         string `json:"grant_id"`
	GrantRevision   int64  `json:"grant_revision"`
	RevocationEpoch int64  `json:"revocation_epoch"`
	GrantDigest     string `json:"grant_digest"`
}

// GrantClaim is a non-authorizing projection of the grant owner state. Digest
// fields are opaque proof references: this candidate checks canonical shape
// and exact replay equality only; it never treats a client-supplied digest as
// an authentication signature or durable owner fact.
type GrantClaim struct {
	SchemaVersion            int           `json:"schema_version"`
	GrantID                  string        `json:"grant_id"`
	Action                   Action        `json:"action"`
	CallerPrincipalRef       string        `json:"caller_principal_ref"`
	ScopeRef                 string        `json:"scope_ref"`
	SourceScopeRef           string        `json:"source_scope_ref"`
	TargetScopeRef           string        `json:"target_scope_ref"`
	Parent                   ParentBinding `json:"parent"`
	GrantRevision            int64         `json:"grant_revision"`
	RevocationEpoch          int64         `json:"revocation_epoch"`
	CapabilityEvidenceCursor string        `json:"capability_evidence_cursor"`
	RequestDigest            string        `json:"request_digest"`
	GrantDigest              string        `json:"grant_digest"`
	Status                   string        `json:"status"`
	IssuedAt                 time.Time     `json:"issued_at"`
	ExpiresAt                time.Time     `json:"expires_at"`
	RevokedAt                *time.Time    `json:"revoked_at,omitempty"`
	ExpiredAt                *time.Time    `json:"expired_at,omitempty"`
}

// CursorClaim binds an exclusive source cursor to grant, caller session and
// both execution fences. It is not a bearer capability.
type CursorClaim struct {
	SchemaVersion         int                   `json:"schema_version"`
	Domain                string                `json:"domain"`
	SourceOwner           string                `json:"source_owner"`
	SourceOwnerGeneration int64                 `json:"source_owner_generation"`
	SourceID              string                `json:"source_id"`
	SourceEpoch           string                `json:"source_epoch"`
	Lane                  string                `json:"lane"`
	StreamID              string                `json:"stream_id"`
	SessionID             string                `json:"session_id"`
	BootEpoch             string                `json:"boot_epoch"`
	SelectorDigest        string                `json:"selector_digest"`
	AfterSequence         int64                 `json:"after_sequence"`
	AfterCursor           string                `json:"after_cursor,omitempty"`
	Grant                 GrantRef              `json:"grant"`
	SourceFence           common.ExecutionFence `json:"source_fence"`
	TargetFence           common.ExecutionFence `json:"target_fence"`
}

// AuthorizationRequest is the pure preflight input for one protected action.
// It carries opaque content/resource references for send without owning them.
type AuthorizationRequest struct {
	SchemaVersion          int           `json:"schema_version"`
	RequestID              string        `json:"request_id"`
	Action                 Action        `json:"action"`
	Caller                 CallerBinding `json:"caller"`
	Grant                  GrantClaim    `json:"grant"`
	GrantRef               GrantRef      `json:"grant_ref"`
	Parent                 ParentBinding `json:"parent"`
	Cursor                 *CursorClaim  `json:"cursor,omitempty"`
	ContentRef             string        `json:"content_ref,omitempty"`
	ResourceReservationRef string        `json:"resource_reservation_ref,omitempty"`
	IntentDigest           string        `json:"intent_digest"`
	RequestedAt            time.Time     `json:"requested_at"`
}

// EffectCounters makes the candidate's zero-side-effect boundary measurable.
type EffectCounters struct {
	GrantWrites          int64 `json:"grant_writes"`
	ContentWrites        int64 `json:"content_writes"`
	ResourceReservations int64 `json:"resource_reservations"`
	CommandWrites        int64 `json:"command_writes"`
	AttemptWrites        int64 `json:"attempt_writes"`
	ProviderCalls        int64 `json:"provider_calls"`
	CursorWrites         int64 `json:"cursor_writes"`
	EventWrites          int64 `json:"event_writes"`
}

// AuthorizationDecision is a preflight result. An authorized candidate still
// has zero effects because actual side effects belong to later owners.
type AuthorizationDecision struct {
	Action        Action         `json:"action"`
	Status        string         `json:"status"`
	DecisionKind  string         `json:"decision_kind"`
	Authoritative bool           `json:"authoritative"`
	GrantRef      GrantRef       `json:"grant_ref"`
	Parent        ParentBinding  `json:"parent"`
	Effects       EffectCounters `json:"effects"`
}

// RevokeGrantRequest is the pure CAS shape for advancing a grant's revocation
// epoch. Apply returns a new snapshot and performs no persistence.
type RevokeGrantRequest struct {
	SchemaVersion   int           `json:"schema_version"`
	GrantRef        GrantRef      `json:"grant_ref"`
	Caller          CallerBinding `json:"caller"`
	Parent          ParentBinding `json:"parent"`
	ReasonRef       string        `json:"reason_ref"`
	OperationDigest string        `json:"operation_digest"`
	RequestedAt     time.Time     `json:"requested_at"`
}

// CallerBindingFromContext resolves only common's private authenticated
// context values; missing/invalid values never become an anonymous caller.
// CallerBindingFromContext 从 common 私有上下文解析已认证 task caller。
func CallerBindingFromContext(ctx context.Context, now time.Time) (CallerBinding, error) {
	if ctx == nil || !validUTC(now) {
		return CallerBinding{}, ErrCallerUnavailable
	}
	principal, principalErr := common.RequireCallerPrincipal(ctx)
	if principalErr != nil {
		return CallerBinding{}, ErrCallerUnavailable
	}
	attribution, attributionErr := common.RequireCallerAttribution(ctx)
	if attributionErr != nil {
		return CallerBinding{}, ErrCallerUnavailable
	}
	binding := callerBindingFromSnapshots(principal, attribution)
	if err := binding.ValidateAt(now); err != nil {
		return CallerBinding{}, ErrCallerUnavailable
	}
	return binding, nil
}

// Evaluate performs strict, read-only authorization preflight. It returns a
// zero-effect denied result on every error and never calls an effect owner.
// Evaluate 执行严格只读授权预检，任何拒绝都返回零副作用结果。
func Evaluate(ctx context.Context, request AuthorizationRequest, now time.Time) (AuthorizationDecision, error) {
	denied := deniedDecision()
	caller, err := CallerBindingFromContext(ctx, now)
	if err != nil || !sameCaller(caller, request.Caller) {
		return denied, ErrUnauthorized
	}
	if err := request.ValidateAt(now); err != nil {
		return denied, err
	}
	if request.Grant.Status == "revoked" {
		return denied, ErrGrantRevoked
	}
	if request.Grant.Status == "expired" {
		return denied, ErrGrantExpired
	}
	return AuthorizationDecision{Action: request.Action, Status: "authorized", DecisionKind: AuthorizationDecisionKind, Authoritative: false, GrantRef: request.Grant.Ref(), Parent: request.Parent, Effects: zeroEffects()}, nil
}

// Apply advances a valid active grant to a revoked snapshot without writing it.
// Caller and grant validity are checked at the supplied evaluation time; the
// request's RequestedAt is only an input fence and cannot bypass that check.
// Apply 将 active grant 纯函数推进到 revoked 快照，不写入任何 owner。
func (r RevokeGrantRequest) Apply(grant GrantClaim, now time.Time) (GrantClaim, error) {
	if err := r.ValidateAt(now); err != nil {
		return GrantClaim{}, err
	}
	// Keep the CAS identity fence ahead of time-of-use checks so stale
	// references remain conflicts, while a matching grant is still revalidated
	// against the actual evaluation time below.
	if err := grant.validateShape(); err != nil {
		return GrantClaim{}, err
	}
	if err := validateRevokeIdentity(r, grant); err != nil {
		return GrantClaim{}, err
	}
	if err := grant.ValidateAt(now); err != nil {
		return GrantClaim{}, err
	}
	if err := validateRevokeTime(r, grant, now); err != nil {
		return GrantClaim{}, err
	}
	revokedAt := now
	grant.Status, grant.RevokedAt, grant.ExpiredAt = "revoked", &revokedAt, nil
	grant.GrantRevision++
	grant.RevocationEpoch++
	return grant, nil
}

// validateRevokeIdentity 比较撤销请求与 grant 的完整 caller/parent fence。
func validateRevokeIdentity(request RevokeGrantRequest, grant GrantClaim) error {
	if grant.Status != "active" || !sameGrantRef(request.GrantRef, grant.Ref()) {
		return ErrRevocationConflict
	}
	if request.Caller.PrincipalRef != grant.CallerPrincipalRef || request.Caller.ScopeRef != grant.ScopeRef || request.Caller.TaskID != grant.Parent.ParentTaskID || !sameFence(request.Caller.Source, grant.Parent.ParentFence) {
		return ErrRevocationConflict
	}
	if !sameParent(request.Parent, grant.Parent) {
		return ErrRevocationConflict
	}
	return nil
}

// validateRevokeTime 拒绝无效、未来或已过期的撤销时间。
func validateRevokeTime(request RevokeGrantRequest, grant GrantClaim, now time.Time) error {
	if !validUTC(now) {
		return ErrAuthorizationInvalid
	}
	if request.RequestedAt.Before(grant.IssuedAt) || request.RequestedAt.After(now) {
		return ErrRevocationConflict
	}
	if !now.Before(grant.ExpiresAt) {
		return ErrGrantExpired
	}
	return nil
}

// Ref returns the immutable grant reference used by request/cursor fences.
// Ref 返回供 request/cursor 使用的不可变 grant reference。
func (g GrantClaim) Ref() GrantRef {
	return GrantRef{SchemaVersion: AuthorizationSchemaVersion, GrantID: g.GrantID, GrantRevision: g.GrantRevision, RevocationEpoch: g.RevocationEpoch, GrantDigest: g.GrantDigest}
}

// IsZero reports whether any side-effect counter was incremented.
// IsZero 判断副作用计数是否全部为零。
func (c EffectCounters) IsZero() bool {
	return c == (EffectCounters{})
}

// Validate rejects nonzero counters and malformed decision status.
// Validate 拒绝非零副作用计数和未知 decision 状态。
func (d AuthorizationDecision) Validate() error {
	if d.Status != "authorized" && d.Status != "denied" {
		return fmt.Errorf("%w: decision status is unsupported", ErrAuthorizationInvalid)
	}
	if d.DecisionKind != AuthorizationDecisionKind {
		return fmt.Errorf("%w: decision kind is not structural preflight", ErrAuthorizationInvalid)
	}
	if !d.Effects.IsZero() {
		return fmt.Errorf("%w: decision carries side effects", ErrAuthorizationInvalid)
	}
	if d.Authoritative {
		return fmt.Errorf("%w: preflight decision cannot be authoritative", ErrAuthorizationInvalid)
	}
	if d.Status == "authorized" {
		if !validAction(d.Action) {
			return fmt.Errorf("%w: decision action is unsupported", ErrAuthorizationInvalid)
		}
		if err := d.GrantRef.Validate(); err != nil {
			return err
		}
		if err := d.Parent.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func deniedDecision() AuthorizationDecision {
	return AuthorizationDecision{Status: "denied", DecisionKind: AuthorizationDecisionKind, Authoritative: false, Effects: zeroEffects()}
}

func zeroEffects() EffectCounters { return EffectCounters{} }
