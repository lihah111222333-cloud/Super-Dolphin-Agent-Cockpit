package identity

import (
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// reservationFromRow 将 SQLC reservation 行逐字段校验并映射为 store DTO。
func reservationFromRow(row storedb.OrchestrationIdentityReservation) (Reservation, error) {
	commandID := stringPointer(row.CommandID)
	record := Reservation{
		ReservationID: row.ReservationID, CommandID: commandID, AgentID: row.AgentID,
		IdentityKind: row.IdentityKind, SourceKind: row.SourceKind,
		CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef,
		ParentAgentID: row.ParentAgentID, IdempotencyNamespace: row.IdempotencyNamespace,
		IdempotencyKeyDigest: row.IdempotencyKeyDigest, RequestDigest: row.RequestDigest,
		SourceFenceRef: row.SourceFenceRef, TargetFenceRef: row.TargetFenceRef,
		ExpectedBindingVersion: row.ExpectedBindingVersion, ReservationGeneration: row.ReservationGeneration,
		State: row.State, SchemaVersion: row.SchemaVersion, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	request := ReservationRequest{
		ReservationID: record.ReservationID, CommandID: stringValue(commandID), AgentID: record.AgentID,
		IdentityKind: record.IdentityKind, SourceKind: record.SourceKind,
		CallerPrincipalRef: record.CallerPrincipalRef, ScopeRef: record.ScopeRef,
		ParentAgentID: record.ParentAgentID, IdempotencyNamespace: record.IdempotencyNamespace,
		IdempotencyKeyDigest: record.IdempotencyKeyDigest, RequestDigest: record.RequestDigest,
		SourceFenceRef: record.SourceFenceRef, TargetFenceRef: record.TargetFenceRef,
		ExpectedBindingVersion: record.ExpectedBindingVersion, ReservationGeneration: record.ReservationGeneration,
	}
	if err := request.Validate(); err != nil {
		return Reservation{}, err
	}
	if !validReservationState(record.State) || record.SchemaVersion != 1 {
		return Reservation{}, invalidStored("reservation state or schema version")
	}
	if err := validateTimestamp("created_at", record.CreatedAt); err != nil {
		return Reservation{}, err
	}
	if err := validateTimestamp("updated_at", record.UpdatedAt); err != nil {
		return Reservation{}, err
	}
	return record, nil
}

func incarnationFromRow(row storedb.OrchestrationExecutionIncarnation) (Incarnation, error) {
	record := Incarnation{
		IncarnationID: row.IncarnationID, ReservationID: row.ReservationID, AgentID: row.AgentID,
		IdentityKind: row.IdentityKind, ExternalIdentityRef: row.ExternalIdentityRef,
		ExternalEpoch: row.ExternalEpoch, SourceFenceRef: row.SourceFenceRef,
		IdentityDigest: row.IdentityDigest, IncarnationGeneration: row.IncarnationGeneration,
		SchemaVersion: row.SchemaVersion, CreatedAt: row.CreatedAt,
	}
	request := IncarnationRequest{
		IncarnationID: record.IncarnationID, ReservationID: record.ReservationID,
		AgentID: record.AgentID, IdentityKind: record.IdentityKind,
		ExternalIdentityRef: record.ExternalIdentityRef, ExternalEpoch: record.ExternalEpoch,
		SourceFenceRef: record.SourceFenceRef, IdentityDigest: record.IdentityDigest,
		IncarnationGeneration: record.IncarnationGeneration,
	}
	if err := request.Validate(); err != nil {
		return Incarnation{}, err
	}
	if record.SchemaVersion != 1 {
		return Incarnation{}, invalidStored("incarnation schema version")
	}
	if err := validateTimestamp("created_at", record.CreatedAt); err != nil {
		return Incarnation{}, err
	}
	return record, nil
}

// preparationFromRow 将 SQLC preparation 行逐字段校验并映射为 store DTO。
func preparationFromRow(row storedb.OrchestrationBindingPreparation) (BindingPreparation, error) {
	record := BindingPreparation{
		PreparationID: row.PreparationID, ReservationID: row.ReservationID, IncarnationID: row.IncarnationID,
		AgentID: row.AgentID, IdentityKind: row.IdentityKind, PreparationGeneration: row.PreparationGeneration,
		ExpectedBindingVersion: row.ExpectedBindingVersion, ExpectedIncarnationGeneration: row.ExpectedIncarnationGeneration,
		BindingDigest: row.BindingDigest, SourceFenceRef: row.SourceFenceRef,
		ActiveProcessReceiptRef: row.ActiveProcessReceiptRef, State: row.State,
		SchemaVersion: row.SchemaVersion, CreatedAt: row.CreatedAt, ResolvedAt: row.ResolvedAt,
	}
	request := BindingPreparationRequest{
		PreparationID: record.PreparationID, ReservationID: record.ReservationID,
		IncarnationID: record.IncarnationID, AgentID: record.AgentID, IdentityKind: record.IdentityKind,
		SourceFenceRef: record.SourceFenceRef, ActiveProcessReceiptRef: record.ActiveProcessReceiptRef,
		BindingDigest: record.BindingDigest, PreparationGeneration: record.PreparationGeneration,
		ExpectedBindingVersion: record.ExpectedBindingVersion, ExpectedIncarnationGeneration: record.ExpectedIncarnationGeneration,
	}
	if err := request.Validate(); err != nil {
		return BindingPreparation{}, err
	}
	if !validPreparationState(record.State) || record.SchemaVersion != 1 {
		return BindingPreparation{}, invalidStored("preparation state or schema version")
	}
	if err := validateTimestamp("created_at", record.CreatedAt); err != nil {
		return BindingPreparation{}, err
	}
	if record.ResolvedAt != "" {
		if err := validateTimestamp("resolved_at", record.ResolvedAt); err != nil {
			return BindingPreparation{}, err
		}
	}
	return record, nil
}

// currentFromRow 将 SQLC current binding 历史行逐字段校验并映射为 store DTO。
func currentFromRow(row storedb.OrchestrationCurrentBinding) (CurrentBinding, error) {
	record := CurrentBinding{
		AgentID: row.AgentID, BindingVersion: row.BindingVersion, ReservationID: row.ReservationID,
		IncarnationID: row.IncarnationID, IdentityKind: row.IdentityKind,
		ReservationGeneration: row.ReservationGeneration, IncarnationGeneration: row.IncarnationGeneration,
		ExternalIdentityRef: row.ExternalIdentityRef, SourceFenceRef: row.SourceFenceRef,
		ActiveProcessReceiptRef: row.ActiveProcessReceiptRef, BindingDigest: row.BindingDigest,
		State: row.State, SchemaVersion: row.SchemaVersion, UpdatedAt: row.UpdatedAt,
	}
	if err := validateIdentityTexts(
		identityTextField{"agent_id", record.AgentID, 128, false},
		identityTextField{"reservation_id", record.ReservationID, 128, false},
		identityTextField{"incarnation_id", record.IncarnationID, 128, false},
		identityTextField{"external_identity_ref", record.ExternalIdentityRef, 512, false},
		identityTextField{"source_fence_ref", record.SourceFenceRef, 512, false},
		identityTextField{"active_process_receipt_ref", record.ActiveProcessReceiptRef, 512, false}); err != nil {
		return CurrentBinding{}, err
	}
	if err := validateIdentityKind(record.IdentityKind); err != nil {
		return CurrentBinding{}, err
	}
	if err := validateDigest("binding_digest", record.BindingDigest); err != nil {
		return CurrentBinding{}, err
	}
	if record.BindingVersion <= 0 || record.ReservationGeneration <= 0 || record.IncarnationGeneration <= 0 || record.SchemaVersion != 1 {
		return CurrentBinding{}, invalidStored("current binding version or schema")
	}
	if !validCurrentState(record.State) {
		return CurrentBinding{}, invalidStored("current binding state")
	}
	if err := validateTimestamp("updated_at", record.UpdatedAt); err != nil {
		return CurrentBinding{}, err
	}
	return record, nil
}

// currentBindingFromRow keeps the mapper name explicit at current-binding call
// sites while sharing the one field-complete implementation above.
func currentBindingFromRow(row storedb.OrchestrationCurrentBinding) (CurrentBinding, error) {
	return currentFromRow(row)
}

func stringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateTimestamp(name, value string) error {
	if value == "" {
		return invalidStored(name + " is empty")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() {
		return invalidStored(name + " is invalid")
	}
	return nil
}

func invalidStored(message string) error {
	return fmt.Errorf("%w: stored %s", ErrInvalidIdentity, message)
}

func validReservationState(value string) bool {
	switch value {
	case "reserved", "claimed", "identity_pending", "activation_pending", "outcome_unknown", "activated", "rejected", "failed", "expired":
		return true
	default:
		return false
	}
}

func validPreparationState(value string) bool {
	switch value {
	case "prepared_non_authorizing", "finalized", "aborted", "outcome_unknown":
		return true
	default:
		return false
	}
}

func validCurrentState(value string) bool {
	switch value {
	case "active", "revoking", "revoked":
		return true
	default:
		return false
	}
}
