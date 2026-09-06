package resourceadmission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/content"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

const (
	resourceProfileKeyID           = "resource-admission-profile-key"
	resourceContentRef             = "resource-content/message"
	resourceContentObjectID        = "resource-content-object"
	resourceContentReference       = "resource-content-reference"
	resourceContentSize      int64 = 32
)

type resourceStoreFixture struct {
	db           *sql.DB
	dbPath       string
	store        *Store
	identities   *identity.Store
	source       identity.CurrentBinding
	target       identity.CurrentBinding
	command      orchestrationcommand.CompleteIntentRecord
	now          time.Time
	proofExpires time.Time
}

type resourceProfileKeyResolver struct{}

func (resourceProfileKeyResolver) ResolveSendProfileDigestKey(_ context.Context, keyID string, version int64) ([]byte, error) {
	if keyID != resourceProfileKeyID || version != 1 {
		return nil, fmt.Errorf("unknown resource profile digest key %q/%d", keyID, version)
	}
	return []byte("resource-admission-profile-contract-key-material"), nil
}

type resourceCurrentBindingReader struct{ store *identity.Store }

func (r resourceCurrentBindingReader) GetSendProfileCurrentBinding(
	ctx context.Context,
	dbtx storedb.DBTX,
	agentID string,
) (orchestrationcommand.ActiveBindingSelector, error) {
	binding, err := r.store.GetCurrentWithDBTX(ctx, dbtx, agentID)
	if err != nil {
		return orchestrationcommand.ActiveBindingSelector{}, err
	}
	return resourceSelector(binding)
}

func newResourceStoreFixture(t *testing.T, path string) *resourceStoreFixture {
	t.Helper()
	db := openResourceTestDB(t, path)
	commands, err := orchestrationcommand.NewStore(db)
	resourceNoError(t, err)
	identities, err := identity.NewStore(db)
	resourceNoError(t, err)
	source := createResourceIdentity(t, commands, identities, "source")
	target := createResourceIdentity(t, commands, identities, "target")
	access := newResourceAccessStore(t, db, identities)
	now := time.Now().UTC().Truncate(time.Microsecond)
	grant, accessRequest, authorization := createResourceGrant(t, access, source, target, now)
	proofExpires := authorization.ExpiresAt
	createResourceContent(t, db, source, grant, proofExpires)
	intent, profile := resourceProfileRequest(t, source, target, grant, accessRequest, authorization)
	verifier, err := orchestrationcommand.NewHMACSendProfileDigestVerifier(resourceProfileKeyResolver{})
	resourceNoError(t, err)
	profiles, err := orchestrationcommand.NewSendProfileStore(db, verifier, resourceCurrentBindingReader{store: identities})
	resourceNoError(t, err)
	record, err := profiles.Reserve(t.Context(), intent, profile)
	resourceNoError(t, err)
	assertResourceFixtureBindings(t, db, record.Command.Intent.CommandID)
	admissionNow := now.Add(time.Minute)
	store, err := NewStore(db, func() time.Time { return admissionNow })
	resourceNoError(t, err)
	return &resourceStoreFixture{db: db, dbPath: path, store: store, identities: identities,
		source: source, target: target, command: record, now: admissionNow, proofExpires: proofExpires}
}

func assertResourceFixtureBindings(t *testing.T, db *sql.DB, commandID string) {
	t.Helper()
	queries := storedb.New(db)
	profile, err := queries.GetOrchestrationCommandIntentProfile(t.Context(), storedb.GetOrchestrationCommandIntentProfileParams{CommandID: commandID})
	resourceNoError(t, err)
	source, err := queries.GetOrchestrationCurrentBinding(t.Context(), storedb.GetOrchestrationCurrentBindingParams{AgentID: profile.SourceAgentID})
	resourceNoError(t, err)
	target, err := queries.GetOrchestrationCurrentBinding(t.Context(), storedb.GetOrchestrationCurrentBindingParams{AgentID: profile.TargetAgentID})
	resourceNoError(t, err)
	if !resourceSourceBindingMatches(source, profile) || !resourceTargetBindingMatches(target, profile) {
		t.Fatalf("resource fixture binding/profile mismatch: source=%#v target=%#v profile=%#v", source, target, profile)
	}
}

func openResourceTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: path})
	resourceNoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	resourceNoError(t, sqliteruntime.RunEmbeddedMigrations(t.Context(), db))
	return db
}

func createResourceIdentity(
	t *testing.T,
	commands *orchestrationcommand.Store,
	identities *identity.Store,
	suffix string,
) identity.CurrentBinding {
	t.Helper()
	commandID := "resource-launch-" + suffix
	_, err := commands.Reserve(t.Context(), orchestrationcommand.Intent{
		CommandID: commandID, CommandKind: "launch_agent", CallerPrincipalRef: "resource-launcher/" + suffix,
		ScopeRef: "resource/scope", IdempotencyNamespace: "resource/launch/" + suffix,
		IdempotencyKeyDigest: resourceHash("launch-key-" + suffix), RequestDigest: resourceHash("launch-request-" + suffix),
		DigestKeyID: "resource-launch-key", RequestContentRef: "resource-launch-content/" + suffix,
		SourceFenceRef: "resource-launch-source/" + suffix, TargetFenceRef: "resource-launch-target/" + suffix,
	})
	resourceNoError(t, err)
	reservation, err := identities.Reserve(t.Context(), identity.ReservationRequest{
		ReservationID: "resource-identity-reservation-" + suffix, CommandID: commandID,
		AgentID: "resource-agent-" + suffix, IdentityKind: "native_subagent", SourceKind: "first_launch",
		CallerPrincipalRef: "resource-launcher/" + suffix, ScopeRef: "resource/scope", ParentAgentID: "resource-root",
		IdempotencyNamespace: "resource/identity/" + suffix,
		IdempotencyKeyDigest: resourceHash("identity-key-" + suffix), RequestDigest: resourceHash("identity-request-" + suffix),
		SourceFenceRef: "resource-launch-source/" + suffix, TargetFenceRef: "resource-launch-target/" + suffix,
		ReservationGeneration: 1,
	})
	resourceNoError(t, err)
	incarnation, err := identities.CreateIncarnation(t.Context(), identity.IncarnationRequest{
		IncarnationID: "resource-incarnation-" + suffix, ReservationID: reservation.ReservationID,
		AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		ExternalIdentityRef: "resource-external-" + suffix, ExternalEpoch: "resource-epoch-" + suffix,
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: resourceHash("incarnation-" + suffix),
		IncarnationGeneration: 1,
	})
	resourceNoError(t, err)
	preparation, err := identities.PrepareBinding(t.Context(), identity.BindingPreparationRequest{
		PreparationID: "resource-preparation-" + suffix, ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, BindingDigest: resourceHash("binding-" + suffix),
		PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: 1,
	})
	resourceNoError(t, err)
	binding, err := identities.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{
		PreparationID: preparation.PreparationID, AgentID: reservation.AgentID,
		ExpectedPreparationGeneration: 1, ExpectedBindingVersion: 0,
		IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef,
		IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef,
		ActiveProcessReceiptRef: "resource-process-receipt-" + suffix, BindingDigest: preparation.BindingDigest,
	})
	resourceNoError(t, err)
	return binding
}

func newResourceAccessStore(t *testing.T, db *sql.DB, identities *identity.Store) *accesswait.Store {
	t.Helper()
	fenceVerifier, err := eventstore.NewIdentityFenceVerifier(identities)
	resourceNoError(t, err)
	events, err := eventstore.NewStore(db, fenceVerifier)
	resourceNoError(t, err)
	store, err := accesswait.NewStore(db, identities, events)
	resourceNoError(t, err)
	return store
}

func createResourceGrant(
	t *testing.T,
	store *accesswait.Store,
	source identity.CurrentBinding,
	target identity.CurrentBinding,
	now time.Time,
) (accesswait.AccessGrant, accesswait.AccessRequest, accesswait.Authorization) {
	t.Helper()
	issuedAt := now.Add(-time.Minute)
	grant, err := store.CreateGrant(t.Context(), accesswait.AccessGrantRequest{
		GrantID: "resource-send-grant", CallerPrincipalRef: "resource/principal", ScopeRef: "resource/scope",
		Source: resourceAccessFence(source), Target: resourceAccessFence(target), CanSend: true,
		IdempotencyNamespace: "resource/grant", IdempotencyKeyDigest: resourceHash("grant-key"),
		RequestDigest: resourceHash("grant-request"), IssuedAt: issuedAt, ExpiresAt: now.Add(4 * time.Hour),
	})
	resourceNoError(t, err)
	request := accesswait.AccessRequest{
		RequestID: "resource-send-authorization", GrantID: grant.GrantID,
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Capability: "send",
		Source: grant.Source, Target: grant.Target, RequestDigest: resourceHash("authorization-request"), RequestedAt: now,
	}
	authorization, err := store.Authorize(t.Context(), request)
	resourceNoError(t, err)
	return grant, request, authorization
}

func resourceAccessFence(binding identity.CurrentBinding) accesswait.FenceSnapshot {
	return accesswait.FenceSnapshot{
		AgentID: binding.AgentID, ReservationID: binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration, BindingVersion: binding.BindingVersion,
		BindingDigest: binding.BindingDigest, SourceFenceRef: binding.SourceFenceRef,
	}
}

func resourceProfileRequest(
	t *testing.T,
	source identity.CurrentBinding,
	target identity.CurrentBinding,
	grant accesswait.AccessGrant,
	request accesswait.AccessRequest,
	authorization accesswait.Authorization,
) (orchestrationcommand.Intent, orchestrationcommand.SendMessageProfile) {
	t.Helper()
	intent := orchestrationcommand.Intent{
		CommandID: "resource-send-command", CommandKind: "send_message",
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef,
		IdempotencyNamespace: "resource/send-command", IdempotencyKeyDigest: resourceHash("send-command-key"),
		RequestDigest: resourceHash("placeholder"), DigestKeyID: resourceProfileKeyID,
		RequestContentRef: resourceContentRef, SourceFenceRef: source.SourceFenceRef, TargetFenceRef: target.SourceFenceRef,
	}
	profile := orchestrationcommand.SendMessageProfile{
		Kind: orchestrationcommand.SendMessageProfileKind, Version: 1,
		Caller: orchestrationcommand.CallerSnapshot{
			Profile: "standalone_http_local_v1", AuthContext: "resource-auth-context", ExpiresAt: authorization.ExpiresAt,
		},
		Authorization: orchestrationcommand.AuthorizationDecision{
			Kind: orchestrationcommand.AccessGrantAuthorizationKind, Decision: "allowed", RequestVersion: 1,
			RequestID: request.RequestID, RequestDigest: request.RequestDigest, GrantID: authorization.GrantID,
			GrantRevision: authorization.GrantRevision, Capability: authorization.Capability,
			RequestedAt: request.RequestedAt, DecidedAt: authorization.AuthorizedAt, ExpiresAt: authorization.ExpiresAt,
			GrantRequestDigest: grant.RequestDigest, GrantIssuedAt: grant.IssuedAt, GrantUpdatedAt: grant.UpdatedAt,
		},
		Source: mustResourceSelector(t, source), Target: mustResourceSelector(t, target),
		CompleteIntentDigest: intent.RequestDigest, DigestDomain: orchestrationcommand.SendMessageDigestDomain,
		DigestAlgorithm: orchestrationcommand.HMACSHA256DigestAlgorithm, DigestKeyID: resourceProfileKeyID, DigestKeyVersion: 1,
	}
	digest, err := orchestrationcommand.ComputeSendProfileDigest(
		[]byte("resource-admission-profile-contract-key-material"), intent, profile,
	)
	resourceNoError(t, err)
	intent.RequestDigest, profile.CompleteIntentDigest = digest, digest
	return intent, profile
}

func resourceSelector(binding identity.CurrentBinding) (orchestrationcommand.ActiveBindingSelector, error) {
	updatedAt, err := time.Parse(time.RFC3339Nano, binding.UpdatedAt)
	if err != nil {
		return orchestrationcommand.ActiveBindingSelector{}, err
	}
	return orchestrationcommand.ActiveBindingSelector{
		Version: 1, Kind: orchestrationcommand.ActiveBindingSelectorKind,
		AgentID: binding.AgentID, IdentityKind: binding.IdentityKind, ReservationID: binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration, ExternalIdentityRef: binding.ExternalIdentityRef,
		BindingVersion: binding.BindingVersion, BindingDigest: binding.BindingDigest, FenceRef: binding.SourceFenceRef,
		ActiveProcessReceiptRef: binding.ActiveProcessReceiptRef, State: binding.State,
		BindingSchemaVersion: binding.SchemaVersion, UpdatedAt: updatedAt,
	}, nil
}

func mustResourceSelector(t *testing.T, binding identity.CurrentBinding) orchestrationcommand.ActiveBindingSelector {
	t.Helper()
	selector, err := resourceSelector(binding)
	resourceNoError(t, err)
	return selector
}

func createResourceContent(
	t *testing.T,
	db *sql.DB,
	source identity.CurrentBinding,
	grant accesswait.AccessGrant,
	proofExpires time.Time,
) {
	t.Helper()
	reservation := resourceContentReservation(source, grant, proofExpires)
	insertResourceRow(t, db, "orchestration_content_ingest_reservations", reservation)
	startResourceContentPreparation(t, db, reservation)
	object := resourceContentObject(reservation)
	insertResourceRow(t, db, "orchestration_content_objects", object)
	insertResourceRow(t, db, "orchestration_content_private_payload_receipts", resourceContentReceipt(object))
	execResourceSQL(t, db, `UPDATE orchestration_content_ingest_reservations SET state = 'sealed', revision = 2 WHERE ingest_reservation_id = ?`, reservation["ingest_reservation_id"])
	execResourceSQL(t, db, `UPDATE orchestration_content_objects SET content_state = 'active', revision = 2, ref_version = 1, promoted_at = ? WHERE object_id = ?`, resourceTime(time.Now()), resourceContentObjectID)
	insertResourceRow(t, db, "orchestration_content_refs", resourceContentReferenceRow(reservation, proofExpires))
	execResourceSQL(t, db, `UPDATE orchestration_content_ingest_reservations SET state = 'promoted', revision = 3 WHERE ingest_reservation_id = ?`, reservation["ingest_reservation_id"])
}

func resourceContentReservation(
	source identity.CurrentBinding,
	grant accesswait.AccessGrant,
	proofExpires time.Time,
) map[string]any {
	return map[string]any{
		"ingest_reservation_id": "resource-content-reservation", "ingest_ticket_id": "resource-content-ticket",
		"object_id": resourceContentObjectID, "object_version": int64(1), "owner_id": "resource-content-owner",
		"owner_generation": int64(1), "writer_fence_ref": "resource-content-writer-fence",
		"security_time_generation": int64(1), "workspace_ref": "resource-content-workspace",
		"caller_principal_ref": grant.CallerPrincipalRef, "scope_ref": grant.ScopeRef,
		"source_agent_id": source.AgentID, "source_identity_kind": source.IdentityKind,
		"source_identity_reservation_id": source.ReservationID, "source_reservation_generation": source.ReservationGeneration,
		"source_incarnation_id": source.IncarnationID, "source_incarnation_generation": source.IncarnationGeneration,
		"source_external_identity_ref": source.ExternalIdentityRef, "source_binding_version": source.BindingVersion,
		"source_binding_digest": source.BindingDigest, "source_fence_ref": source.SourceFenceRef,
		"source_active_process_receipt_ref": source.ActiveProcessReceiptRef, "source_binding_updated_at": source.UpdatedAt,
		"idempotency_namespace": "resource/content/ingest", "idempotency_key_digest": resourceHash("content-ingest-key"),
		"proof_request_digest": resourceHash("content-proof-request"), "candidate_ref_digest": resourceHash("content-candidate-ref"),
		"content_intent_digest": resourceHash("content-intent"), "digest_key_id": "resource-content-digest-key",
		"digest_key_version": int64(1), "ingest_kind": "content.single", "size_bytes": resourceContentSize,
		"media_type": "text/plain", "permitted_sink_role": "command_request", "aead_algorithm": "aes-256-gcm",
		"key_wrap_profile": "kms-handle-v1", "aad_profile": "orch.content.single.aad/v1",
		"quota_snapshot_ref": "resource-content-quota", "quota_snapshot_digest": resourceHash("content-quota"),
		"retention_policy_id": "resource-content-retention", "retention_policy_version": int64(1),
		"retention_policy_digest": resourceHash("content-retention"),
		"assembly_deadline_at":    resourceTime(proofExpires.Add(time.Hour)), "revision": int64(1),
		"state": "reserved", "schema_version": int64(1),
	}
}

func startResourceContentPreparation(t *testing.T, db *sql.DB, reservation map[string]any) {
	t.Helper()
	operationID, err := content.StablePrepareOperationID(reservation["ingest_ticket_id"].(string))
	resourceNoError(t, err)
	startedAt := time.Now().UTC()
	startedAtText := resourceTime(startedAt)
	execResourceSQL(t, db, `UPDATE orchestration_content_ingest_reservations
		SET prepare_operation_id = ?, prepare_started_at = ?, updated_at = ?
		WHERE ingest_reservation_id = ?`, operationID, startedAtText, startedAtText, reservation["ingest_reservation_id"])
}

func resourceContentObject(reservation map[string]any) map[string]any {
	size := reservation["size_bytes"].(int64)
	return map[string]any{
		"object_id": reservation["object_id"], "object_version": int64(1),
		"ingest_reservation_id": reservation["ingest_reservation_id"], "owner_id": reservation["owner_id"],
		"owner_generation": reservation["owner_generation"], "writer_fence_ref": reservation["writer_fence_ref"],
		"security_time_generation": reservation["security_time_generation"], "workspace_ref": reservation["workspace_ref"],
		"source_agent_id": reservation["source_agent_id"], "source_fence_ref": reservation["source_fence_ref"],
		"aead_algorithm": "aes-256-gcm", "key_wrap_profile": "kms-handle-v1",
		"key_handle_ref": "resource-content-key-handle", "key_receipt_ref": "resource-content-key-receipt",
		"key_receipt_digest": resourceHash("content-key-receipt"), "nonce_hex": "000000000000000000000001",
		"aad_profile": "orch.content.single.aad/v1", "aad_digest": resourceHash("content-aad"),
		"media_type": reservation["media_type"], "size_bytes": size,
		"ciphertext_sha256": resourceHash("content-ciphertext"), "sealed_payload_size_bytes": size + 16,
		"retention_policy_digest": reservation["retention_policy_digest"], "content_state": "prepared",
		"key_state": "active", "ref_version": int64(0), "revision": int64(1), "promoted_at": "", "schema_version": int64(1),
	}
}

func resourceContentReceipt(object map[string]any) map[string]any {
	return map[string]any{
		"object_id": object["object_id"], "object_version": object["object_version"],
		"ingest_reservation_id": object["ingest_reservation_id"], "backend_kind": "private_state_object_v1",
		"object_store_id": "resource-private-content-store", "payload_format": "aes256gcm-ciphertext-tag-v1",
		"private_payload_ref": "resource-private-payload", "sealed_payload_size_bytes": object["sealed_payload_size_bytes"],
		"ciphertext_sha256": object["ciphertext_sha256"], "store_operation_id": "resource-private-store-operation",
		"store_request_digest": resourceHash("private-store-request"), "store_receipt_ref": "resource-private-store-receipt",
		"store_receipt_digest": resourceHash("private-store-receipt"), "stored_at": resourceTime(time.Now()), "schema_version": int64(1),
	}
}

func resourceContentReferenceRow(reservation map[string]any, proofExpires time.Time) map[string]any {
	return map[string]any{
		"reference_id": resourceContentReference, "canonical_ref": resourceContentRef,
		"object_id": reservation["object_id"], "object_version": reservation["object_version"],
		"ingest_reservation_id": reservation["ingest_reservation_id"], "ref_version": int64(1),
		"reference_kind": "command_request", "reference_state": "live",
		"caller_principal_ref": reservation["caller_principal_ref"], "scope_ref": reservation["scope_ref"],
		"source_agent_id": reservation["source_agent_id"], "source_fence_ref": reservation["source_fence_ref"],
		"proof_request_digest": reservation["proof_request_digest"], "proof_id": "resource-content-proof",
		"proof_revision": int64(1), "owner_id": reservation["owner_id"], "owner_generation": reservation["owner_generation"],
		"writer_fence_ref": reservation["writer_fence_ref"], "security_time_generation": reservation["security_time_generation"],
		"owner_proof_ref": "resource-content-owner-proof", "proof_snapshot_digest": resourceHash("content-proof-snapshot"),
		"proof_expires_at": resourceTime(proofExpires), "media_type": reservation["media_type"],
		"size_bytes": reservation["size_bytes"], "retention_policy_digest": reservation["retention_policy_digest"],
		"schema_version": int64(1),
	}
}

func insertResourceRow(t *testing.T, db *sql.DB, table string, row map[string]any) {
	t.Helper()
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	values, marks := make([]any, len(columns)), make([]string, len(columns))
	for index, column := range columns {
		values[index], marks[index] = row[column], "?"
	}
	query := "INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	if _, err := db.ExecContext(t.Context(), query, values...); err != nil {
		t.Fatalf("insert resource fixture row into %s: %v", table, err)
	}
}

func execResourceSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("execute resource fixture SQL: %v", err)
	}
}

func resourcePolicyRequest(t *testing.T, fixture *resourceStoreFixture, revision int64, suffix string) PublishEffectivePolicyRequest {
	t.Helper()
	request := PublishEffectivePolicyRequest{
		SchemaVersion: SchemaVersion, SnapshotID: "resource-policy-snapshot-" + suffix,
		LineageID: "resource-policy-lineage", Revision: revision, RequestedKind: RequestedKindSendMessage,
		CallerPrincipalRef: fixture.command.Command.Intent.CallerPrincipalRef,
		ScopeRef:           fixture.command.Command.Intent.ScopeRef,
		SourcePolicySetRef: "resource-policy-source/" + suffix, SourcePolicySetDigest: resourceHash("policy-source-" + suffix),
		PrecedenceTraceRef:    "resource-policy-precedence/" + suffix,
		PrecedenceTraceDigest: resourceHash("policy-precedence-" + suffix), EffectiveLimits: resourceLimits(),
		ReleaseConditionDigest: resourceHash("release-condition-" + suffix),
		IdempotencyNamespace:   "resource/policy/" + suffix, IdempotencyKeyDigest: resourceHash("policy-key-" + suffix),
		PublishOperationID: "resource-policy-operation-" + suffix, PublishReceiptRef: "resource-policy-receipt/" + suffix,
		PolicyOwner: PolicyOwnerFence{
			OwnerID: "resource-policy-owner", OwnerGeneration: 7, WriterFenceRef: "resource-policy-writer-fence/7",
			OwnerProofRef: "resource-policy-owner-proof/7", DeploymentFenceRef: "resource-deployment-fence/7",
			SecurityTimeGeneration: 11,
		},
		EffectiveAt: fixture.now.Add(-time.Minute), ValidUntil: fixture.now.Add(2 * time.Hour),
	}
	finalized, err := FinalizePublishEffectivePolicyRequest(request)
	resourceNoError(t, err)
	return finalized
}

func resourceReserveRequest(t *testing.T, fixture *resourceStoreFixture, suffix string) ReserveRequest {
	t.Helper()
	request := ReserveRequest{
		SchemaVersion: SchemaVersion, ReservationID: "resource-reservation-" + suffix,
		ResourceSnapshotID: "resource-snapshot-" + suffix,
		CommandID:          fixture.command.Command.Intent.CommandID, CommandVersion: 1,
		CommandProfileDigest:           fixture.command.Profile.CompleteIntentDigest,
		IdentityFenceDigest:            resourceHash("identity-fence-" + suffix),
		ContentAdmissionSnapshotDigest: resourceHash("content-admission-" + suffix), Requested: resourceRequested(),
		IdempotencyNamespace:   "resource/reservation/" + suffix,
		IdempotencyKeyDigest:   resourceHash("reservation-key-" + suffix),
		ReservationOperationID: "resource-reservation-operation-" + suffix,
		ReservationReceiptRef:  "resource-reservation-receipt/" + suffix,
		AdmissionOwner: AdmissionOwnerFence{
			OwnerID: "resource-admission-owner", OwnerGeneration: 5,
			WriterFenceRef: "resource-admission-writer-fence/5", DeploymentFenceRef: "resource-admission-deployment/5",
			SecurityTimeGeneration: 13, OwnerProofRef: "resource-admission-owner-proof/5",
		},
	}
	finalized, err := FinalizeReserveRequest(request)
	resourceNoError(t, err)
	return finalized
}

func resourceLimits() ResourceVector {
	return ResourceVector{AgentCount: 10, InFlightCount: 10, FanoutCount: 10, ConcurrencyCount: 10,
		WallTimeMS: 60_000, InputTokens: 1_000, OutputTokens: 1_000, CostMicrounits: 100_000,
		ContentBytes: resourceContentSize, IngressBytes: resourceContentSize}
}

func resourceRequested() ResourceVector {
	return ResourceVector{AgentCount: 1, InFlightCount: 1, FanoutCount: 1, ConcurrencyCount: 1,
		WallTimeMS: 1_000, InputTokens: 100, OutputTokens: 100, CostMicrounits: 1_000,
		ContentBytes: resourceContentSize, IngressBytes: resourceContentSize}
}

func resourceResolveRequest(snapshot ReservationSnapshot) ResolveReservedSnapshotRequest {
	return ResolveReservedSnapshotRequest{
		SchemaVersion: snapshot.SchemaVersion, CommandID: snapshot.CommandID, CommandVersion: snapshot.CommandVersion,
		CommandProfileDigest: snapshot.CommandProfileDigest, IdentityFenceDigest: snapshot.IdentityFenceDigest,
		ContentAdmissionSnapshotDigest: snapshot.Content.AdmissionSnapshotDigest,
	}
}

func revokeResourceBinding(t *testing.T, fixture *resourceStoreFixture, binding identity.CurrentBinding) {
	t.Helper()
	_, err := fixture.identities.RevokeBinding(t.Context(), identity.RevokeBindingRequest{
		AgentID: binding.AgentID, ExpectedBindingVersion: binding.BindingVersion,
		IncarnationID: binding.IncarnationID, SourceFenceRef: binding.SourceFenceRef,
	})
	resourceNoError(t, err)
}

func resourceTableCount(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count resource table %s: %v", table, err)
	}
	return count
}

func resourceHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func resourceTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func resourceNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected resource fixture error: %v", err)
	}
}

func resourceFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name+".sqlite")
}
