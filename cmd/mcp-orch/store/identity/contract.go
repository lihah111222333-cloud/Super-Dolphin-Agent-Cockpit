// Package identity owns logical-agent reservations, execution incarnations, and
// append-only binding history for the orchestration sidecar.
package identity

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Typed identity errors let callers distinguish malformed input from a stale
// fence or an already-resolved binding without depending on SQLite messages.
var (
	ErrInvalidIdentity         = errors.New("identity input is invalid")
	ErrIdentityConflict        = errors.New("identity operation conflicts with existing record")
	ErrIdentityNotFound        = errors.New("identity record not found")
	ErrIdentityFence           = errors.New("identity fence comparison failed")
	ErrIdentityAlreadyResolved = errors.New("identity operation is already resolved")
)

const (
	identityKindProviderSession = "provider_session"
	identityKindNativeSubagent  = "native_subagent"
	identityKindManagedAgent    = "managed_agent"
	identityKindA2ARemoteAgent  = "a2a_remote_agent"

	sourceKindFirstLaunch       = "first_launch"
	sourceKindRecover           = "recover"
	sourceKindReplace           = "replace"
	sourceKindProviderDiscovery = "provider_discovery"
	sourceKindA2AObservation    = "a2a_observation"
)

// ReservationRequest describes one logical identity reservation candidate.
// CommandID is required for command-backed sources and must be empty for
// provider discovery or A2A observation reservations.
type ReservationRequest struct {
	ReservationID          string `json:"reservation_id"`
	CommandID              string `json:"command_id"`
	AgentID                string `json:"agent_id"`
	IdentityKind           string `json:"identity_kind"`
	SourceKind             string `json:"source_kind"`
	CallerPrincipalRef     string `json:"caller_principal_ref"`
	ScopeRef               string `json:"scope_ref"`
	ParentAgentID          string `json:"parent_agent_id"`
	IdempotencyNamespace   string `json:"idempotency_namespace"`
	IdempotencyKeyDigest   string `json:"idempotency_key_digest"`
	RequestDigest          string `json:"request_digest"`
	SourceFenceRef         string `json:"source_fence_ref"`
	TargetFenceRef         string `json:"target_fence_ref"`
	ExpectedBindingVersion int64  `json:"expected_binding_version"`
	ReservationGeneration  int64  `json:"reservation_generation"`
}

// Reservation is the validated durable logical identity reservation.
type Reservation struct {
	ReservationID          string  `json:"reservation_id"`
	CommandID              *string `json:"command_id"`
	AgentID                string  `json:"agent_id"`
	IdentityKind           string  `json:"identity_kind"`
	SourceKind             string  `json:"source_kind"`
	CallerPrincipalRef     string  `json:"caller_principal_ref"`
	ScopeRef               string  `json:"scope_ref"`
	ParentAgentID          string  `json:"parent_agent_id"`
	IdempotencyNamespace   string  `json:"idempotency_namespace"`
	IdempotencyKeyDigest   string  `json:"idempotency_key_digest"`
	RequestDigest          string  `json:"request_digest"`
	SourceFenceRef         string  `json:"source_fence_ref"`
	TargetFenceRef         string  `json:"target_fence_ref"`
	ExpectedBindingVersion int64   `json:"expected_binding_version"`
	ReservationGeneration  int64   `json:"reservation_generation"`
	State                  string  `json:"state"`
	SchemaVersion          int64   `json:"schema_version"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
}

// IncarnationRequest describes an immutable execution incarnation candidate.
type IncarnationRequest struct {
	IncarnationID         string `json:"incarnation_id"`
	ReservationID         string `json:"reservation_id"`
	AgentID               string `json:"agent_id"`
	IdentityKind          string `json:"identity_kind"`
	ExternalIdentityRef   string `json:"external_identity_ref"`
	ExternalEpoch         string `json:"external_epoch"`
	SourceFenceRef        string `json:"source_fence_ref"`
	IdentityDigest        string `json:"identity_digest"`
	IncarnationGeneration int64  `json:"incarnation_generation"`
}

// Incarnation is the immutable execution identity associated with a reservation.
type Incarnation struct {
	IncarnationID         string `json:"incarnation_id"`
	ReservationID         string `json:"reservation_id"`
	AgentID               string `json:"agent_id"`
	IdentityKind          string `json:"identity_kind"`
	ExternalIdentityRef   string `json:"external_identity_ref"`
	ExternalEpoch         string `json:"external_epoch"`
	SourceFenceRef        string `json:"source_fence_ref"`
	IdentityDigest        string `json:"identity_digest"`
	IncarnationGeneration int64  `json:"incarnation_generation"`
	SchemaVersion         int64  `json:"schema_version"`
	CreatedAt             string `json:"created_at"`
}

// BindingPreparationRequest describes a non-authorizing binding candidate.
type BindingPreparationRequest struct {
	PreparationID                 string `json:"preparation_id"`
	ReservationID                 string `json:"reservation_id"`
	IncarnationID                 string `json:"incarnation_id"`
	AgentID                       string `json:"agent_id"`
	IdentityKind                  string `json:"identity_kind"`
	SourceFenceRef                string `json:"source_fence_ref"`
	ActiveProcessReceiptRef       string `json:"active_process_receipt_ref"`
	BindingDigest                 string `json:"binding_digest"`
	PreparationGeneration         int64  `json:"preparation_generation"`
	ExpectedBindingVersion        int64  `json:"expected_binding_version"`
	ExpectedIncarnationGeneration int64  `json:"expected_incarnation_generation"`
}

// BindingPreparation is a durable, non-authorizing binding candidate.
type BindingPreparation struct {
	PreparationID                 string `json:"preparation_id"`
	ReservationID                 string `json:"reservation_id"`
	IncarnationID                 string `json:"incarnation_id"`
	AgentID                       string `json:"agent_id"`
	IdentityKind                  string `json:"identity_kind"`
	PreparationGeneration         int64  `json:"preparation_generation"`
	ExpectedBindingVersion        int64  `json:"expected_binding_version"`
	ExpectedIncarnationGeneration int64  `json:"expected_incarnation_generation"`
	BindingDigest                 string `json:"binding_digest"`
	SourceFenceRef                string `json:"source_fence_ref"`
	ActiveProcessReceiptRef       string `json:"active_process_receipt_ref"`
	State                         string `json:"state"`
	SchemaVersion                 int64  `json:"schema_version"`
	CreatedAt                     string `json:"created_at"`
	ResolvedAt                    string `json:"resolved_at"`
}

// FinalizeBindingRequest carries all expected CAS material and the authoritative
// active-process receipt needed to turn a preparation into an active binding.
type FinalizeBindingRequest struct {
	PreparationID                 string `json:"preparation_id"`
	AgentID                       string `json:"agent_id"`
	ExpectedPreparationGeneration int64  `json:"expected_preparation_generation"`
	ExpectedBindingVersion        int64  `json:"expected_binding_version"`
	IncarnationID                 string `json:"incarnation_id"`
	ExternalIdentityRef           string `json:"external_identity_ref"`
	IdentityKind                  string `json:"identity_kind"`
	SourceFenceRef                string `json:"source_fence_ref"`
	ActiveProcessReceiptRef       string `json:"active_process_receipt_ref"`
	BindingDigest                 string `json:"binding_digest"`
}

// AbortBindingRequest closes an unresolved preparation with caller-supplied
// cleanup evidence; the reason is validated but not stored in migration 128.
type AbortBindingRequest struct {
	PreparationID                 string `json:"preparation_id"`
	AgentID                       string `json:"agent_id"`
	ExpectedPreparationGeneration int64  `json:"expected_preparation_generation"`
	ReasonRef                     string `json:"reason_ref"`
}

// RevokeBindingRequest carries the expected current binding fence for a close.
type RevokeBindingRequest struct {
	AgentID                string `json:"agent_id"`
	ExpectedBindingVersion int64  `json:"expected_binding_version"`
	IncarnationID          string `json:"incarnation_id"`
	SourceFenceRef         string `json:"source_fence_ref"`
}

// CloseBindingRequest is an explicit alias for the binding close operation.
type CloseBindingRequest = RevokeBindingRequest

// CurrentBinding is one append-only binding history row. GetCurrent returns the
// latest version, including a revoked terminal row.
type CurrentBinding struct {
	AgentID                 string `json:"agent_id"`
	BindingVersion          int64  `json:"binding_version"`
	ReservationID           string `json:"reservation_id"`
	IncarnationID           string `json:"incarnation_id"`
	IdentityKind            string `json:"identity_kind"`
	ReservationGeneration   int64  `json:"reservation_generation"`
	IncarnationGeneration   int64  `json:"incarnation_generation"`
	ExternalIdentityRef     string `json:"external_identity_ref"`
	SourceFenceRef          string `json:"source_fence_ref"`
	ActiveProcessReceiptRef string `json:"active_process_receipt_ref"`
	BindingDigest           string `json:"binding_digest"`
	State                   string `json:"state"`
	SchemaVersion           int64  `json:"schema_version"`
	UpdatedAt               string `json:"updated_at"`
}

// IdentityRepository 是编排层使用的窄身份 owner，不暴露 legacy agent/session 写者。
type IdentityRepository interface {
	Reserve(ctx context.Context, req ReservationRequest) (Reservation, error)
	CreateIncarnation(ctx context.Context, req IncarnationRequest) (Incarnation, error)
	PrepareBinding(ctx context.Context, req BindingPreparationRequest) (BindingPreparation, error)
	FinalizeBinding(ctx context.Context, req FinalizeBindingRequest) (CurrentBinding, error)
	AbortBinding(ctx context.Context, req AbortBindingRequest) (BindingPreparation, error)
	RevokeBinding(ctx context.Context, req RevokeBindingRequest) (CurrentBinding, error)
	CloseBinding(ctx context.Context, req CloseBindingRequest) (CurrentBinding, error)
	GetCurrent(ctx context.Context, agentID string) (CurrentBinding, error)
}

// Validate 在执行 SQL 前拒绝不完整或越界的 reservation 输入。
func (r ReservationRequest) Validate() error {
	return validateReservationIdentity(r)
}

// Validate 在执行 SQL 前拒绝不完整或越界的 incarnation 输入。
func (r IncarnationRequest) Validate() error {
	return validateIncarnationIdentity(r)
}

// Validate 在执行 SQL 前拒绝不完整或越界的 preparation 输入。
func (r BindingPreparationRequest) Validate() error {
	return validatePreparationIdentity(r)
}

// Validate 在打开写事务前拒绝不完整或越界的 finalize 输入。
func (r FinalizeBindingRequest) Validate() error {
	return validateFinalizeIdentity(r)
}

// Validate 在打开写事务前拒绝不完整或越界的 abort 输入。
func (r AbortBindingRequest) Validate() error {
	return validateAbortIdentity(r)
}

// Validate 在打开写事务前拒绝不完整或越界的 revoke 输入。
func (r RevokeBindingRequest) Validate() error {
	return validateRevokeIdentity(r)
}

type identityTextField struct {
	name, value string
	limit       int
	allowEmpty  bool
}

func validateIdentityTexts(fields ...identityTextField) error {
	for _, field := range fields {
		if err := validateText(field.name, field.value, field.limit, field.allowEmpty); err != nil {
			return err
		}
	}
	return nil
}

func validateReservationIdentity(r ReservationRequest) error {
	if err := validateIdentityTexts(
		identityTextField{"reservation_id", r.ReservationID, 128, false},
		identityTextField{"agent_id", r.AgentID, 128, false},
		identityTextField{"caller_principal_ref", r.CallerPrincipalRef, 512, false},
		identityTextField{"scope_ref", r.ScopeRef, 512, false},
		identityTextField{"idempotency_namespace", r.IdempotencyNamespace, 256, false},
		identityTextField{"source_fence_ref", r.SourceFenceRef, 512, false},
		identityTextField{"target_fence_ref", r.TargetFenceRef, 512, false},
		identityTextField{"parent_agent_id", r.ParentAgentID, 128, true}); err != nil {
		return err
	}
	if err := validateIdentityKind(r.IdentityKind); err != nil {
		return err
	}
	if err := validateSourceKind(r.SourceKind); err != nil {
		return err
	}
	if err := validateCommandBySource(r.SourceKind, r.CommandID); err != nil {
		return err
	}
	return validateReservationDigestsAndVersions(r)
}

func validateReservationDigestsAndVersions(r ReservationRequest) error {
	if err := validateDigest("idempotency_key_digest", r.IdempotencyKeyDigest); err != nil {
		return err
	}
	if err := validateDigest("request_digest", r.RequestDigest); err != nil {
		return err
	}
	if r.ExpectedBindingVersion < 0 || r.ReservationGeneration <= 0 {
		return invalidField("reservation versions must be nonnegative/positive")
	}
	return nil
}

func validateIncarnationIdentity(r IncarnationRequest) error {
	if err := validateIdentityTexts(
		identityTextField{"incarnation_id", r.IncarnationID, 128, false},
		identityTextField{"reservation_id", r.ReservationID, 128, false},
		identityTextField{"agent_id", r.AgentID, 128, false},
		identityTextField{"external_identity_ref", r.ExternalIdentityRef, 512, false},
		identityTextField{"external_epoch", r.ExternalEpoch, 128, false},
		identityTextField{"source_fence_ref", r.SourceFenceRef, 512, false}); err != nil {
		return err
	}
	if err := validateIdentityKind(r.IdentityKind); err != nil {
		return err
	}
	if err := validateDigest("identity_digest", r.IdentityDigest); err != nil {
		return err
	}
	if r.IncarnationGeneration <= 0 {
		return invalidField("incarnation_generation must be positive")
	}
	return nil
}

// validatePreparationIdentity 校验准备记录的身份、回执引用和代际边界。
func validatePreparationIdentity(r BindingPreparationRequest) error {
	if err := validateIdentityTexts(
		identityTextField{"preparation_id", r.PreparationID, 128, false},
		identityTextField{"reservation_id", r.ReservationID, 128, false},
		identityTextField{"incarnation_id", r.IncarnationID, 128, false},
		identityTextField{"agent_id", r.AgentID, 128, false},
		identityTextField{"source_fence_ref", r.SourceFenceRef, 512, false},
		identityTextField{"active_process_receipt_ref", r.ActiveProcessReceiptRef, 512, true}); err != nil {
		return err
	}
	if err := validateIdentityKind(r.IdentityKind); err != nil {
		return err
	}
	if err := validateDigest("binding_digest", r.BindingDigest); err != nil {
		return err
	}
	if r.PreparationGeneration <= 0 || r.ExpectedBindingVersion < 0 || r.ExpectedIncarnationGeneration <= 0 {
		return invalidField("binding generations are invalid")
	}
	return nil
}

// validateFinalizeIdentity 校验 finalize 请求的完整绑定 fence 与活动进程回执。
func validateFinalizeIdentity(r FinalizeBindingRequest) error {
	if err := validateIdentityTexts(
		identityTextField{"preparation_id", r.PreparationID, 128, false},
		identityTextField{"agent_id", r.AgentID, 128, false},
		identityTextField{"incarnation_id", r.IncarnationID, 128, false},
		identityTextField{"external_identity_ref", r.ExternalIdentityRef, 512, false},
		identityTextField{"source_fence_ref", r.SourceFenceRef, 512, false},
		identityTextField{"active_process_receipt_ref", r.ActiveProcessReceiptRef, 512, false}); err != nil {
		return err
	}
	if err := validateIdentityKind(r.IdentityKind); err != nil {
		return err
	}
	if err := validateDigest("binding_digest", r.BindingDigest); err != nil {
		return err
	}
	if r.ExpectedPreparationGeneration <= 0 || r.ExpectedBindingVersion < 0 {
		return invalidField("finalize generations are invalid")
	}
	return nil
}

func validateAbortIdentity(r AbortBindingRequest) error {
	if err := validateIdentityTexts(
		identityTextField{"preparation_id", r.PreparationID, 128, false},
		identityTextField{"agent_id", r.AgentID, 128, false},
		identityTextField{"reason_ref", r.ReasonRef, 512, false}); err != nil {
		return err
	}
	if r.ExpectedPreparationGeneration <= 0 {
		return invalidField("expected_preparation_generation must be positive")
	}
	return nil
}

func validateRevokeIdentity(r RevokeBindingRequest) error {
	if err := validateIdentityTexts(
		identityTextField{"agent_id", r.AgentID, 128, false},
		identityTextField{"incarnation_id", r.IncarnationID, 128, false},
		identityTextField{"source_fence_ref", r.SourceFenceRef, 512, false}); err != nil {
		return err
	}
	if r.ExpectedBindingVersion <= 0 {
		return invalidField("expected_binding_version must be positive")
	}
	return nil
}

// validateText 校验不带隐式 trim、默认或容错回退的标识文本。
func validateText(name, value string, limit int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return invalidField(fmt.Sprintf("%s must be bounded canonical text", name))
	}
	return nil
}

func validateDigest(name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return invalidField(fmt.Sprintf("%s must be lowercase 256-bit hex", name))
	}
	if _, err := hex.DecodeString(value); err != nil {
		return invalidField(fmt.Sprintf("%s is not hexadecimal", name))
	}
	return nil
}

func validateIdentityKind(value string) error {
	switch value {
	case identityKindProviderSession, identityKindNativeSubagent, identityKindManagedAgent, identityKindA2ARemoteAgent:
		return nil
	default:
		return invalidField("identity_kind is unsupported")
	}
}

func validateSourceKind(value string) error {
	switch value {
	case sourceKindFirstLaunch, sourceKindRecover, sourceKindReplace, sourceKindProviderDiscovery, sourceKindA2AObservation:
		return nil
	default:
		return invalidField("source_kind is unsupported")
	}
}

func validateCommandBySource(sourceKind, commandID string) error {
	commandRequired := sourceKind == sourceKindFirstLaunch || sourceKind == sourceKindRecover || sourceKind == sourceKindReplace
	if commandRequired {
		return validateText("command_id", commandID, 128, false)
	}
	if commandID != "" {
		return invalidField("command_id must be absent for observation source")
	}
	return nil
}

func invalidField(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidIdentity, message)
}
