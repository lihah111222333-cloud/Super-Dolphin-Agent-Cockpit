package content

import (
	"database/sql"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
)

// reservationFromRow 严格解析reservation时间、ID-1字段、prepare登记和状态关系。
func reservationFromRow(row storedb.OrchestrationContentIngestReservation) (IngestReservation, error) {
	sourceUpdatedAt, err := parseContentTime(row.SourceBindingUpdatedAt)
	if err != nil {
		return IngestReservation{}, err
	}
	deadline, err := parseContentTime(row.AssemblyDeadlineAt)
	if err != nil {
		return IngestReservation{}, err
	}
	createdAt, updatedAt, err := parseContentAuditTimes(row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return IngestReservation{}, err
	}
	prepareStartedAt, err := parseOptionalContentTime(row.PrepareStartedAt)
	if err != nil {
		return IngestReservation{}, err
	}
	request := reserveRequestFromRow(row, sourceUpdatedAt, deadline)
	reservation := IngestReservation{
		ReserveIngestRequest: request, ObjectVersion: row.ObjectVersion,
		PrepareOperationID: row.PrepareOperationID, PrepareStartedAt: prepareStartedAt,
		State: row.State, Revision: row.Revision, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if err := request.Validate(); err != nil || !validReservationState(reservation) {
		return IngestReservation{}, ErrContentCorrupt
	}
	return reservation, nil
}

func reserveRequestFromRow(row storedb.OrchestrationContentIngestReservation, sourceUpdatedAt, deadline time.Time) ReserveIngestRequest {
	return ReserveIngestRequest{
		SchemaVersion: row.SchemaVersion, ReservationID: row.IngestReservationID, TicketID: row.IngestTicketID,
		ObjectID: row.ObjectID, OwnerID: row.OwnerID, OwnerGeneration: row.OwnerGeneration,
		WriterFenceRef: row.WriterFenceRef, SecurityTimeGeneration: row.SecurityTimeGeneration,
		WorkspaceRef: row.WorkspaceRef, CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef,
		Source: sourceBindingFromRow(row, sourceUpdatedAt), IdempotencyNamespace: row.IdempotencyNamespace,
		IdempotencyKeyDigest: row.IdempotencyKeyDigest, ProofRequestDigest: row.ProofRequestDigest,
		CandidateRefDigest: row.CandidateRefDigest, ContentIntentDigest: row.ContentIntentDigest,
		DigestKeyID: row.DigestKeyID, DigestKeyVersion: row.DigestKeyVersion, SizeBytes: row.SizeBytes,
		MediaType: row.MediaType, KeyWrapProfile: row.KeyWrapProfile, QuotaSnapshotRef: row.QuotaSnapshotRef,
		QuotaSnapshotDigest: row.QuotaSnapshotDigest, RetentionPolicyID: row.RetentionPolicyID,
		RetentionPolicyVersion: row.RetentionPolicyVersion, RetentionPolicyDigest: row.RetentionPolicyDigest,
		AssemblyDeadline: deadline,
	}
}

func sourceBindingFromRow(row storedb.OrchestrationContentIngestReservation, updatedAt time.Time) SourceBinding {
	return SourceBinding{
		AgentID: row.SourceAgentID, IdentityKind: row.SourceIdentityKind,
		IdentityReservationID: row.SourceIdentityReservationID, ReservationGeneration: row.SourceReservationGeneration,
		IncarnationID: row.SourceIncarnationID, IncarnationGeneration: row.SourceIncarnationGeneration,
		ExternalIdentityRef: row.SourceExternalIdentityRef, BindingVersion: row.SourceBindingVersion,
		BindingDigest: row.SourceBindingDigest, FenceRef: row.SourceFenceRef,
		ActiveProcessReceiptRef: row.SourceActiveProcessReceiptRef, BindingUpdatedAt: updatedAt,
	}
}

func preparedFromRows(object storedb.OrchestrationContentObject, payload storedb.OrchestrationContentPrivatePayloadReceipt) (PreparedObject, error) {
	preparedAt, err := parseContentTime(object.CreatedAt)
	if err != nil {
		return PreparedObject{}, err
	}
	recordedAt, err := parseContentTime(payload.StoredAt)
	if err != nil {
		return PreparedObject{}, err
	}
	receipt := contentobject.PutReceipt{
		SchemaVersion: payload.SchemaVersion, StoreID: payload.ObjectStoreID,
		OperationID: payload.StoreOperationID, RequestDigest: payload.StoreRequestDigest,
		ObjectID: payload.ObjectID, ObjectVersion: payload.ObjectVersion, ObjectRef: payload.PrivatePayloadRef,
		ReceiptRef: payload.StoreReceiptRef, CiphertextHash: payload.CiphertextSha256,
		CiphertextSize: payload.SealedPayloadSizeBytes, ReceiptDigest: payload.StoreReceiptDigest, RecordedAt: recordedAt,
	}
	if err := receipt.Validate(); err != nil || !preparedRowsMatch(object, payload) {
		return PreparedObject{}, ErrContentCorrupt
	}
	return PreparedObject{
		SchemaVersion: object.SchemaVersion, ReservationID: object.IngestReservationID,
		ObjectID: object.ObjectID, ObjectVersion: object.ObjectVersion, State: object.ContentState,
		KeyState: object.KeyState, KeyHandleRef: object.KeyHandleRef, KeyReceiptRef: object.KeyReceiptRef,
		KeyReceiptDigest: object.KeyReceiptDigest, NonceHex: object.NonceHex, AADDigest: object.AadDigest,
		CiphertextHash: object.CiphertextSha256, CiphertextSize: object.SealedPayloadSizeBytes,
		ObjectPutReceipt: receipt, PreparedAt: preparedAt,
	}, nil
}

func activeRefFromRows(ref storedb.OrchestrationContentRef, object storedb.OrchestrationContentObject, reservation IngestReservation) (ActiveRef, error) {
	validUntil, err := parseContentTime(ref.ProofExpiresAt)
	if err != nil {
		return ActiveRef{}, err
	}
	createdAt, err := parseContentTime(ref.CreatedAt)
	if err != nil {
		return ActiveRef{}, err
	}
	result := ActiveRef{
		SchemaVersion: ref.SchemaVersion, ReferenceID: ref.ReferenceID, CanonicalRef: ref.CanonicalRef,
		ObjectID: ref.ObjectID, ObjectVersion: ref.ObjectVersion, RefVersion: ref.RefVersion,
		TicketID: reservation.TicketID, ContentState: object.ContentState, ReferenceState: ref.ReferenceState,
		KeyState: object.KeyState, CallerPrincipalRef: ref.CallerPrincipalRef, ScopeRef: ref.ScopeRef,
		SourceAgentID: ref.SourceAgentID, SourceFenceRef: ref.SourceFenceRef,
		ProofRequestDigest: ref.ProofRequestDigest, ProofID: ref.ProofID, ProofRevision: ref.ProofRevision,
		OwnerID: ref.OwnerID, OwnerGeneration: ref.OwnerGeneration, WriterFenceRef: ref.WriterFenceRef,
		SecurityTimeGeneration: ref.SecurityTimeGeneration, OwnerProofRef: ref.OwnerProofRef,
		SnapshotDigest: ref.ProofSnapshotDigest, ValidUntil: validUntil, MediaType: ref.MediaType,
		SizeBytes: ref.SizeBytes, RetentionPolicyDigest: ref.RetentionPolicyDigest, CreatedAt: createdAt,
	}
	if !activeRowsMatch(ref, object, reservation) || result.Validate() != nil {
		return ActiveRef{}, ErrContentCorrupt
	}
	return result, nil
}

// preparedRowsMatch 校验object与private payload receipt的一一对应关系。
func preparedRowsMatch(object storedb.OrchestrationContentObject, payload storedb.OrchestrationContentPrivatePayloadReceipt) bool {
	return object.ObjectID == payload.ObjectID && object.ObjectVersion == payload.ObjectVersion &&
		object.IngestReservationID == payload.IngestReservationID && object.CiphertextSha256 == payload.CiphertextSha256 &&
		object.SealedPayloadSizeBytes == payload.SealedPayloadSizeBytes && object.ContentState == "prepared" &&
		object.KeyState == "active" && object.SchemaVersion == 1
}

// activeRowsMatch 串联object、reservation、ACL和内容metadata的exact relation。
func activeRowsMatch(ref storedb.OrchestrationContentRef, object storedb.OrchestrationContentObject, reservation IngestReservation) bool {
	return activeObjectRelationMatches(ref, object, reservation) &&
		activeAccessRelationMatches(ref, reservation) && activeContentRelationMatches(ref, reservation)
}

// activeObjectRelationMatches 校验ref、object和reservation的对象身份与状态版本。
func activeObjectRelationMatches(ref storedb.OrchestrationContentRef, object storedb.OrchestrationContentObject, reservation IngestReservation) bool {
	return ref.ObjectID == object.ObjectID && ref.ObjectVersion == object.ObjectVersion &&
		ref.IngestReservationID == reservation.ReservationID && object.IngestReservationID == reservation.ReservationID &&
		object.ContentState == "active" && object.KeyState == "active" && object.RefVersion == ref.RefVersion &&
		reservation.State == "promoted"
}

func activeAccessRelationMatches(ref storedb.OrchestrationContentRef, reservation IngestReservation) bool {
	return ref.CallerPrincipalRef == reservation.CallerPrincipalRef && ref.ScopeRef == reservation.ScopeRef &&
		ref.SourceAgentID == reservation.Source.AgentID && ref.SourceFenceRef == reservation.Source.FenceRef
}

func activeContentRelationMatches(ref storedb.OrchestrationContentRef, reservation IngestReservation) bool {
	return ref.MediaType == reservation.MediaType && ref.SizeBytes == reservation.SizeBytes &&
		ref.RetentionPolicyDigest == reservation.RetentionPolicyDigest
}

// validReservationState 校验三态revision以及外部I/O preflight登记。
func validReservationState(reservation IngestReservation) bool {
	prepared := validPreparationRegistration(reservation)
	switch reservation.State {
	case "reserved":
		return reservation.Revision == 1 && prepared
	case "sealed":
		return reservation.Revision == 2 && prepared && reservation.PrepareOperationID != ""
	case "promoted":
		return reservation.Revision == 3 && prepared && reservation.PrepareOperationID != ""
	default:
		return false
	}
}

func validPreparationRegistration(reservation IngestReservation) bool {
	if reservation.PrepareOperationID == "" {
		return reservation.PrepareStartedAt.IsZero()
	}
	want, err := StablePrepareOperationID(reservation.TicketID)
	return err == nil && reservation.PrepareOperationID == want && validContentTime(reservation.PrepareStartedAt) &&
		!reservation.PrepareStartedAt.After(reservation.AssemblyDeadline)
}

func parseContentAuditTimes(created, updated string) (time.Time, time.Time, error) {
	createdAt, err := parseContentTime(created)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	updatedAt, err := parseContentTime(updated)
	return createdAt, updatedAt, err
}

func parseContentTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !validContentTime(parsed) {
		return time.Time{}, ErrContentCorrupt
	}
	return parsed, nil
}

func parseOptionalContentTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseContentTime(value)
}

func classifyContentReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrContentNotFound
	}
	return err
}
