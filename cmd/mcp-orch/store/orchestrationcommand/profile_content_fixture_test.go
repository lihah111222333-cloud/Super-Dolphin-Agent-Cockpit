package orchestrationcommand_test

import (
	"database/sql"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/content"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	orchestrationcommand "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

const (
	profileContentCanonicalRef = "profile-content/message"
	profileContentObjectID     = "profile-content-object"
	profileContentReferenceID  = "profile-content-reference"
)

type profileContentDeletionFence struct {
	referenceID    string
	canonicalRef   string
	objectID       string
	objectVersion  int64
	refVersion     int64
	fenceKind      string
	fenceState     string
	profileDigest  string
	sourceFenceRef string
	targetFenceRef string
	schemaVersion  int64
}

func createPromotedProfileContent(
	t *testing.T,
	db *sql.DB,
	source identity.CurrentBinding,
	grant accesswait.AccessGrant,
	authorization accesswait.Authorization,
) string {
	t.Helper()
	reservation := profileContentReservation(source, grant, authorization)
	insertProfileContentRow(t, db, "orchestration_content_ingest_reservations", reservation)
	startProfileContentPreparation(t, db, reservation)
	object := profileContentObject(reservation)
	insertProfileContentRow(t, db, "orchestration_content_objects", object)
	insertProfileContentRow(t, db, "orchestration_content_private_payload_receipts", profileContentReceipt(object))
	execProfileContent(t, db, `UPDATE orchestration_content_ingest_reservations SET state = 'sealed', revision = 2 WHERE ingest_reservation_id = ?`, reservation["ingest_reservation_id"])
	execProfileContent(t, db, `UPDATE orchestration_content_objects SET content_state = 'active', revision = 2, ref_version = 1, promoted_at = ? WHERE object_id = ?`, profileTimeString(time.Now()), profileContentObjectID)
	insertProfileContentRow(t, db, "orchestration_content_refs", profileContentRef(reservation, authorization))
	execProfileContent(t, db, `UPDATE orchestration_content_ingest_reservations SET state = 'promoted', revision = 3 WHERE ingest_reservation_id = ?`, reservation["ingest_reservation_id"])
	assertPromotedProfileContent(t, db)
	return profileContentCanonicalRef
}

func startProfileContentPreparation(t *testing.T, db *sql.DB, reservation map[string]any) {
	t.Helper()
	ticketID := reservation["ingest_ticket_id"].(string)
	operationID, err := content.StablePrepareOperationID(ticketID)
	if err != nil {
		t.Fatalf("derive stable profile CONTENT prepare operation: %v", err)
	}
	startedAt := time.Now().UTC()
	deadline, err := time.Parse(time.RFC3339Nano, reservation["assembly_deadline_at"].(string))
	if err != nil || !deadline.After(startedAt) {
		t.Fatalf("profile CONTENT preparation deadline %q is not after start %s: %v", reservation["assembly_deadline_at"], startedAt, err)
	}
	startedAtText := profileTimeString(startedAt)
	result, err := db.ExecContext(t.Context(), `UPDATE orchestration_content_ingest_reservations
		SET prepare_operation_id = ?, prepare_started_at = ?, updated_at = ?
		WHERE ingest_reservation_id = ?`, operationID, startedAtText, startedAtText, reservation["ingest_reservation_id"])
	if err != nil {
		t.Fatalf("start profile CONTENT preparation: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("profile CONTENT preparation rows affected = %d, want 1: %v", affected, err)
	}
}

func assertPromotedProfileContent(t *testing.T, db *sql.DB) {
	t.Helper()
	var reservationState, contentState, keyState, referenceState, canonicalRef string
	err := db.QueryRowContext(t.Context(), `SELECT reservation.state, object.content_state, object.key_state,
		ref.reference_state, ref.canonical_ref
		FROM orchestration_content_ingest_reservations AS reservation
		JOIN orchestration_content_objects AS object ON object.ingest_reservation_id = reservation.ingest_reservation_id
		JOIN orchestration_content_refs AS ref ON ref.ingest_reservation_id = reservation.ingest_reservation_id
		WHERE object.object_id = ? AND ref.reference_id = ?`, profileContentObjectID, profileContentReferenceID).Scan(
		&reservationState, &contentState, &keyState, &referenceState, &canonicalRef,
	)
	if err != nil {
		t.Fatalf("read promoted profile CONTENT fixture: %v", err)
	}
	if reservationState != "promoted" || contentState != "active" || keyState != "active" ||
		referenceState != "live" || canonicalRef != profileContentCanonicalRef {
		t.Fatalf("profile CONTENT fixture states = %q/%q/%q/%q ref=%q, want promoted/active/active/live ref=%q",
			reservationState, contentState, keyState, referenceState, canonicalRef, profileContentCanonicalRef)
	}
}

func profileContentReservation(source identity.CurrentBinding, grant accesswait.AccessGrant, authorization accesswait.Authorization) map[string]any {
	return map[string]any{
		"ingest_reservation_id": "profile-content-reservation", "ingest_ticket_id": "profile-content-ticket",
		"object_id": profileContentObjectID, "object_version": int64(1), "owner_id": "profile-content-owner",
		"owner_generation": int64(1), "writer_fence_ref": "profile-content-writer-fence",
		"security_time_generation": int64(1), "workspace_ref": "profile-content-workspace",
		"caller_principal_ref": grant.CallerPrincipalRef, "scope_ref": grant.ScopeRef,
		"source_agent_id": source.AgentID, "source_identity_kind": source.IdentityKind,
		"source_identity_reservation_id": source.ReservationID, "source_reservation_generation": source.ReservationGeneration,
		"source_incarnation_id": source.IncarnationID, "source_incarnation_generation": source.IncarnationGeneration,
		"source_external_identity_ref": source.ExternalIdentityRef, "source_binding_version": source.BindingVersion,
		"source_binding_digest": source.BindingDigest, "source_fence_ref": source.SourceFenceRef,
		"source_active_process_receipt_ref": source.ActiveProcessReceiptRef, "source_binding_updated_at": source.UpdatedAt,
		"idempotency_namespace": "profile/content/ingest", "idempotency_key_digest": profileHash("profile-content-ingest-key"),
		"proof_request_digest": profileHash("profile-content-proof-request"), "candidate_ref_digest": profileHash("profile-content-candidate-ref"),
		"content_intent_digest": profileHash("profile-content-intent"), "digest_key_id": "profile-content-digest-key",
		"digest_key_version": int64(1), "ingest_kind": "content.single", "size_bytes": int64(32), "media_type": "text/plain",
		"permitted_sink_role": "command_request", "aead_algorithm": "aes-256-gcm", "key_wrap_profile": "kms-handle-v1",
		"aad_profile": "orch.content.single.aad/v1", "quota_snapshot_ref": "profile-content-quota-snapshot",
		"quota_snapshot_digest": profileHash("profile-content-quota"), "retention_policy_id": "profile-content-retention",
		"retention_policy_version": int64(1), "retention_policy_digest": profileHash("profile-content-retention"),
		"assembly_deadline_at": profileTimeString(authorization.ExpiresAt.Add(time.Hour)), "revision": int64(1),
		"state": "reserved", "schema_version": int64(1),
	}
}

func profileContentObject(reservation map[string]any) map[string]any {
	size := reservation["size_bytes"].(int64)
	return map[string]any{
		"object_id": reservation["object_id"], "object_version": int64(1),
		"ingest_reservation_id": reservation["ingest_reservation_id"], "owner_id": reservation["owner_id"],
		"owner_generation": reservation["owner_generation"], "writer_fence_ref": reservation["writer_fence_ref"],
		"security_time_generation": reservation["security_time_generation"], "workspace_ref": reservation["workspace_ref"],
		"source_agent_id": reservation["source_agent_id"], "source_fence_ref": reservation["source_fence_ref"],
		"aead_algorithm": "aes-256-gcm", "key_wrap_profile": "kms-handle-v1", "key_handle_ref": "profile-content-key-handle",
		"key_receipt_ref": "profile-content-key-receipt", "key_receipt_digest": profileHash("profile-content-key-receipt"),
		"nonce_hex": "000000000000000000000001", "aad_profile": "orch.content.single.aad/v1",
		"aad_digest": profileHash("profile-content-aad"), "media_type": reservation["media_type"], "size_bytes": size,
		"ciphertext_sha256": profileHash("profile-content-ciphertext"), "sealed_payload_size_bytes": size + 16,
		"retention_policy_digest": reservation["retention_policy_digest"], "content_state": "prepared", "key_state": "active",
		"ref_version": int64(0), "revision": int64(1), "promoted_at": "", "schema_version": int64(1),
	}
}

func profileContentReceipt(object map[string]any) map[string]any {
	return map[string]any{
		"object_id": object["object_id"], "object_version": object["object_version"],
		"ingest_reservation_id": object["ingest_reservation_id"], "backend_kind": "private_state_object_v1",
		"object_store_id": "profile-private-content-store", "payload_format": "aes256gcm-ciphertext-tag-v1",
		"private_payload_ref": "profile-private-payload", "sealed_payload_size_bytes": object["sealed_payload_size_bytes"],
		"ciphertext_sha256": object["ciphertext_sha256"], "store_operation_id": "profile-private-store-operation",
		"store_request_digest": profileHash("profile-private-store-request"), "store_receipt_ref": "profile-private-store-receipt",
		"store_receipt_digest": profileHash("profile-private-store-receipt"), "stored_at": profileTimeString(time.Now()),
		"schema_version": int64(1),
	}
}

func profileContentRef(reservation map[string]any, authorization accesswait.Authorization) map[string]any {
	return map[string]any{
		"reference_id": profileContentReferenceID, "canonical_ref": profileContentCanonicalRef,
		"object_id": reservation["object_id"], "object_version": reservation["object_version"],
		"ingest_reservation_id": reservation["ingest_reservation_id"], "ref_version": int64(1),
		"reference_kind": "command_request", "reference_state": "live",
		"caller_principal_ref": reservation["caller_principal_ref"], "scope_ref": reservation["scope_ref"],
		"source_agent_id": reservation["source_agent_id"], "source_fence_ref": reservation["source_fence_ref"],
		"proof_request_digest": reservation["proof_request_digest"], "proof_id": "profile-content-proof", "proof_revision": int64(1),
		"owner_id": reservation["owner_id"], "owner_generation": reservation["owner_generation"],
		"writer_fence_ref": reservation["writer_fence_ref"], "security_time_generation": reservation["security_time_generation"],
		"owner_proof_ref": "profile-content-owner-proof", "proof_snapshot_digest": profileHash("profile-content-proof-snapshot"),
		"proof_expires_at": profileTimeString(authorization.ExpiresAt), "media_type": reservation["media_type"],
		"size_bytes": reservation["size_bytes"], "retention_policy_digest": reservation["retention_policy_digest"],
		"schema_version": int64(1),
	}
}

func insertProfileContentRow(t *testing.T, db *sql.DB, table string, row map[string]any) {
	t.Helper()
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	values := make([]any, len(columns))
	marks := make([]string, len(columns))
	for index, column := range columns {
		values[index] = row[column]
		marks[index] = "?"
	}
	query := "INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	if _, err := db.ExecContext(t.Context(), query, values...); err != nil {
		t.Fatalf("insert profile CONTENT row into %s: %v", table, err)
	}
}

func execProfileContent(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("advance profile CONTENT fixture: %v", err)
	}
}

func assertProfileContentDeletionFence(t *testing.T, db *sql.DB, record orchestrationcommand.CompleteIntentRecord, canonicalRef string) {
	t.Helper()
	var got profileContentDeletionFence
	err := db.QueryRowContext(t.Context(), `SELECT reference_id, canonical_ref, object_id, object_version, ref_version,
		fence_kind, fence_state, command_profile_digest, source_fence_ref, target_fence_ref, schema_version
		FROM orchestration_command_content_deletion_fences WHERE command_id = ?`, record.Command.Intent.CommandID).Scan(
		&got.referenceID, &got.canonicalRef, &got.objectID, &got.objectVersion, &got.refVersion,
		&got.fenceKind, &got.fenceState, &got.profileDigest, &got.sourceFenceRef, &got.targetFenceRef, &got.schemaVersion,
	)
	if err != nil {
		t.Fatalf("read profile content deletion fence: %v", err)
	}
	want := profileContentDeletionFence{
		referenceID: profileContentReferenceID, canonicalRef: canonicalRef, objectID: profileContentObjectID,
		objectVersion: 1, refVersion: 1, fenceKind: "non_terminal_command", fenceState: "held",
		profileDigest: record.Profile.CompleteIntentDigest, sourceFenceRef: record.Profile.Source.FenceRef,
		targetFenceRef: record.Profile.Target.FenceRef, schemaVersion: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile content deletion fence = %#v, want %#v", got, want)
	}
}

func profileTimeString(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
