package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

const resourceReservedMigration = "133_orchestration_resource_reserved.sql"

type resourceTableContract struct {
	name     string
	columns  []string
	pk       []string
	fks      []string
	indexes  []string
	triggers []string
}

func TestResourceReservedMigrationContract(t *testing.T) {
	db := openResourceReservedDB(t)
	for _, contract := range resourceTableContracts() {
		assertResourceTableContract(t, db, contract)
	}
	assertResourceTriggerRelations(t, db)
	assertResourceWideLimits(t, db)
}

func TestResourceReservedMigrationUpgradeIsAdditiveAndIdempotent(t *testing.T) {
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 132)); err != nil {
		t.Fatalf("run migrations through v132: %v", err)
	}
	seedResourceCommandProfile(t, db)
	want := resourcePrerequisiteCounts(t, db)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, resourceReservedMigration, readMigrationTestFile(t, resourceReservedMigration))
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatalf("upgrade v132 to v133: %v", err)
	}
	assertMaxMigrationVersion(t, db, 133)
	assertMigrationMarkerCount(t, db, resourceReservedMigration, 1)
	assertResourceCounts(t, db, resourceTableNames(), 0)
	assertResourcePrerequisiteCounts(t, db, want)
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatalf("repeat v133 migration: %v", err)
	}
	assertMigrationMarkerCount(t, db, resourceReservedMigration, 1)
	assertResourceCounts(t, db, resourceTableNames(), 0)
}

func TestResourcePolicyRevisionCanonicalAndImmutable(t *testing.T) {
	db := openResourceReservedDB(t)
	revisionTwo := validResourcePolicy(2)
	if err := insertContentMap(db, resourcePolicyTable(), revisionTwo); err == nil {
		t.Fatal("policy lineage accepted revision 2 without revision 1")
	}
	insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
	revisionThree := validResourcePolicy(3)
	if err := insertContentMap(db, resourcePolicyTable(), revisionThree); err == nil {
		t.Fatal("policy lineage accepted a revision gap")
	}
	backdated := cloneContentRow(revisionTwo)
	backdated["effective_at"], backdated["created_at"] = "2035-03-01T00:00:00Z", "2035-03-01T00:00:00Z"
	if err := insertContentMap(db, resourcePolicyTable(), backdated); err == nil {
		t.Fatal("policy lineage accepted a backdated effective revision")
	}
	insertContentMapOK(t, db, resourcePolicyTable(), revisionTwo)
	duplicateScopeRevision := validResourcePolicyWithSuffix(1, "duplicate-scope-revision")
	duplicateScopeRevision["policy_lineage_id"] = "duplicate-scope-lineage"
	duplicateScopeRevision["policy_snapshot_digest"] = strings.Repeat("7", 64)
	duplicateScopeRevision["publish_receipt_digest"] = strings.Repeat("8", 64)
	if err := insertContentMap(db, resourcePolicyTable(), duplicateScopeRevision); err == nil {
		t.Fatal("policy accepted duplicate scope/kind revision across lineages")
	}
	assertResourceMutationRejected(t, db, resourcePolicyTable())
	badDigest := validResourcePolicyWithSuffix(1, "bad-digest")
	badDigest["policy_lineage_id"] = "bad-digest-lineage"
	badDigest["scope_ref"] = "bad-digest-scope"
	badDigest["policy_snapshot_digest"] = strings.Repeat("A", 64)
	badDigest["publish_receipt_digest"] = strings.Repeat("9", 64)
	if err := insertContentMap(db, resourcePolicyTable(), badDigest); err == nil {
		t.Fatal("policy accepted a non-canonical digest")
	}
	badNUL := validResourcePolicyWithSuffix(1, "bad-nul")
	badNUL["policy_lineage_id"] = "bad-nul-lineage"
	badNUL["scope_ref"] = "bad-nul-scope"
	badNUL["policy_snapshot_digest"] = strings.Repeat("b", 64)
	badNUL["publish_receipt_digest"] = strings.Repeat("d", 64)
	badNUL["publish_request_digest"] = strings.Repeat("a", 64) + "\x00"
	if err := insertContentMap(db, resourcePolicyTable(), badNUL); err == nil {
		t.Fatal("policy accepted a 64-hex digest with a NUL suffix")
	}
	assertResourcePolicyLimitsPositive(t, db)
}

func TestResourceReservationRequiresPolicyAndExactRelations(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		db := openResourceFixtureDB(t)
		insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
		insertContentMapOK(t, db, resourceReservationTable(), validResourceReservation(1))
		assertResourceMutationRejected(t, db, resourceReservationTable())
	})
	t.Run("missing_policy", func(t *testing.T) {
		db := openResourceFixtureDB(t)
		if err := insertContentMap(db, resourceReservationTable(), validResourceReservation(1)); err == nil {
			t.Fatal("reservation without an effective policy was accepted")
		}
	})
	for _, test := range resourceReservationDriftCases() {
		t.Run(test.name, func(t *testing.T) {
			db := openResourceFixtureDB(t)
			insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
			row := validResourceReservation(1)
			test.mutate(row)
			if err := insertContentMap(db, resourceReservationTable(), row); err == nil {
				t.Fatal("reservation relation drift was accepted")
			}
		})
	}
}

func TestResourceReservationRejectsEveryOverLimitDimension(t *testing.T) {
	for _, pair := range resourceLimitPairs() {
		t.Run(pair.reserved, func(t *testing.T) {
			db := openResourceFixtureDB(t)
			insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
			row := validResourceReservation(1)
			row[pair.reserved] = int64(101)
			if err := insertContentMap(db, resourceReservationTable(), row); err == nil {
				t.Fatalf("over-limit dimension %s was accepted", pair.reserved)
			}
		})
	}
}

func TestResourceReservationRejectsStalePolicyAndIdentity(t *testing.T) {
	t.Run("policy", func(t *testing.T) {
		db := openResourceFixtureDB(t)
		insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
		insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(2))
		row := validResourceReservation(1)
		row["created_at"] = "2035-03-05T00:00:00Z"
		if err := insertContentMap(db, resourceReservationTable(), row); err == nil {
			t.Fatal("reservation accepted a superseded current policy")
		}
		current := validResourceReservation(2)
		current["created_at"] = "2035-03-05T00:00:00Z"
		insertContentMapOK(t, db, resourceReservationTable(), current)
	})
	t.Run("identity", func(t *testing.T) {
		db := openResourceFixtureDB(t)
		insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
		insertProfileBinding(t, db, "src", 2, "revoked", "2035-03-02T00:00:00Z")
		if err := insertContentMap(db, resourceReservationTable(), validResourceReservation(1)); err == nil {
			t.Fatal("reservation accepted a stale source ID binding")
		}
	})
}

func TestResourceUsageLedgerPhysicallyRejectsAllWrites(t *testing.T) {
	db := openResourceFixtureDB(t)
	insertContentMapOK(t, db, resourcePolicyTable(), validResourcePolicy(1))
	insertContentMapOK(t, db, resourceReservationTable(), validResourceReservation(1))
	usage := validResourceUsage()
	if err := insertContentMap(db, resourceUsageTable(), usage); err == nil {
		t.Fatal("disabled usage ledger accepted INSERT")
	}
	seedResourceUsageBehindInsertGuard(t, db, usage)
	for _, statement := range []string{
		"UPDATE orchestration_resource_usage_ledger SET cumulative_value = cumulative_value + 1",
		"DELETE FROM orchestration_resource_usage_ledger",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("usage ledger accepted mutation: %s", statement)
		}
	}
}

func openResourceReservedDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 133)); err != nil {
		t.Fatalf("run migrations through v133: %v", err)
	}
	assertMaxMigrationVersion(t, db, 133)
	assertMigrationMarkerCount(t, db, resourceReservedMigration, 1)
	return db
}

func openResourceFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openResourceReservedDB(t)
	seedResourceCommandProfile(t, db)
	return db
}

func seedResourceCommandProfile(t *testing.T, db *sql.DB) {
	t.Helper()
	seedCommandIntentProfileRelations(t, db)
	reservation := validContentReservation(32, "")
	insertContentMapOK(t, db, "orchestration_content_ingest_reservations", reservation)
	startContentPreparation(t, db, reservation, "")
	object := validContentObject(reservation, "")
	insertContentMapOK(t, db, "orchestration_content_objects", object)
	insertContentMapOK(t, db, "orchestration_content_private_payload_receipts", validContentReceipt(object))
	contentExecOK(t, db, `UPDATE orchestration_content_ingest_reservations SET state='sealed', revision=2 WHERE ingest_reservation_id=?`, reservation["ingest_reservation_id"])
	contentExecOK(t, db, `UPDATE orchestration_content_objects SET content_state='active', revision=2, ref_version=1, promoted_at='2035-03-01T00:00:00Z' WHERE object_id=?`, object["object_id"])
	insertContentMapOK(t, db, "orchestration_content_refs", validContentRef(reservation))
	contentExecOK(t, db, `UPDATE orchestration_content_ingest_reservations SET state='promoted', revision=3 WHERE ingest_reservation_id=?`, reservation["ingest_reservation_id"])
	if err := insertCommandIntentProfile(db, validCommandIntentProfileRow()); err != nil {
		t.Fatalf("insert RESOURCE command profile: %v", err)
	}
}

func validResourcePolicy(revision int64) map[string]any {
	return validResourcePolicyWithSuffix(revision, fmt.Sprintf("r%d", revision))
}

func validResourcePolicyWithSuffix(revision int64, suffix string) map[string]any {
	digit := fmt.Sprintf("%x", revision%15+1)
	row := map[string]any{
		"policy_snapshot_id": "resource-policy-" + suffix, "policy_lineage_id": "resource-policy-lineage",
		"policy_revision": revision, "requested_kind": "send_message", "caller_principal_ref": "principal",
		"scope_ref": "scope", "policy_applicability_digest": strings.Repeat(digit, 64),
		"dimension_set": "orch.resource.dimensions/v1", "source_policy_set_ref": "resource-policy-set-" + suffix,
		"source_policy_set_digest": strings.Repeat("3", 64), "precedence_trace_ref": "resource-trace-" + suffix,
		"precedence_trace_digest":  strings.Repeat("4", 64),
		"canonicalization_profile": "orch.resource.effective-policy/v1",
		"canonical_policy_hash":    strings.Repeat(digit, 63) + "9",
		"policy_snapshot_digest":   strings.Repeat(digit, 63) + "a",
		"effective_limits_digest":  strings.Repeat(digit, 63) + "b",
		"release_condition_digest": strings.Repeat("c", 64), "idempotency_namespace": "resource/policy/" + suffix,
		"idempotency_key_digest": strings.Repeat(digit, 63) + "d", "publish_request_digest": strings.Repeat("5", 64),
		"publish_operation_id": "resource-publish-" + suffix, "publish_operation_digest": strings.Repeat("6", 64),
		"publish_receipt_ref":    "resource-publish-receipt-" + suffix,
		"publish_receipt_digest": strings.Repeat(digit, 63) + "e", "policy_owner_id": "resource-policy-owner",
		"policy_owner_generation": int64(1), "policy_writer_fence_ref": "resource-policy-writer-fence",
		"policy_owner_proof_ref": "resource-policy-owner-proof",
		"deployment_fence_ref":   "resource-deployment-fence", "security_time_generation": int64(1),
		"effective_at": resourcePolicyEffectiveAt(revision), "valid_until": "2035-06-01T00:00:00Z",
		"schema_version": int64(1), "created_at": resourcePolicyEffectiveAt(revision),
	}
	for _, field := range resourcePolicyLimitFields() {
		row[field] = int64(100)
	}
	return row
}

func resourcePolicyEffectiveAt(revision int64) string {
	if revision == 1 {
		return "2035-03-02T00:00:00Z"
	}
	return fmt.Sprintf("2035-03-%02dT00:00:00Z", revision+2)
}

func validResourceReservation(policyRevision int64) map[string]any {
	policy := validResourcePolicy(policyRevision)
	row := map[string]any{
		"reservation_id": "resource-reservation", "reservation_version": int64(1),
		"resource_snapshot_id": "resource-snapshot", "resource_snapshot_digest": strings.Repeat("1", 64),
		"resource_status": "reserved", "requested_kind": "send_message", "command_id": "profile-command",
		"command_version": int64(1), "command_profile_digest": strings.Repeat("a", 64),
		"caller_principal_ref": "principal", "scope_ref": "scope", "source_agent_id": "src",
		"source_binding_version": int64(1), "source_fence_ref": "source-fence-src", "target_agent_id": "dst",
		"target_binding_version": int64(1), "target_fence_ref": "source-fence-dst",
		"identity_fence_digest": strings.Repeat("2", 64), "content_reference_id": "content-reference",
		"content_ref": "content-ref-1", "content_object_id": "content-object", "content_object_version": int64(1),
		"content_ref_version": int64(1), "content_owner_snapshot_digest": strings.Repeat("f", 64),
		"content_admission_snapshot_digest": strings.Repeat("3", 64),
		"policy_snapshot_id":                policy["policy_snapshot_id"], "policy_revision": policyRevision,
		"policy_snapshot_digest": policy["policy_snapshot_digest"], "policy_canonical_hash": policy["canonical_policy_hash"],
		"effective_limits_digest": policy["effective_limits_digest"],
		"usage_digest_domain":     "orch.resource.usage-ledger/v1", "usage_watermark": int64(0),
		"usage_digest":             "c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31",
		"release_condition_digest": policy["release_condition_digest"], "idempotency_namespace": "resource/reservation",
		"idempotency_key_digest": strings.Repeat("4", 64), "reservation_request_digest": strings.Repeat("5", 64),
		"reservation_operation_id": "resource-reserve-operation", "reservation_operation_digest": strings.Repeat("6", 64),
		"reservation_receipt_ref": "resource-reservation-receipt", "reservation_receipt_digest": strings.Repeat("7", 64),
		"admission_owner_id": "resource-admission-owner", "admission_owner_generation": int64(1),
		"admission_writer_fence_ref":     "resource-admission-writer-fence",
		"admission_deployment_fence_ref": "resource-admission-deployment-fence", "security_time_generation": int64(1),
		"owner_proof_ref": "resource-owner-proof", "valid_until": "2035-05-01T00:00:00Z",
		"schema_version": int64(1), "created_at": "2035-03-03T00:00:00Z",
	}
	for _, pair := range resourceLimitPairs() {
		row[pair.reserved] = int64(1)
	}
	return row
}

func validResourceUsage() map[string]any {
	policy := validResourcePolicy(1)
	return map[string]any{
		"usage_entry_id": "resource-usage-1", "reservation_id": "resource-reservation", "reservation_version": int64(1),
		"ledger_sequence": int64(1), "previous_usage_digest": "c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31",
		"usage_entry_digest": strings.Repeat("8", 64), "command_id": "profile-command", "command_version": int64(1),
		"command_profile_digest": strings.Repeat("a", 64), "identity_fence_digest": strings.Repeat("2", 64),
		"content_admission_snapshot_digest": strings.Repeat("3", 64), "policy_snapshot_id": policy["policy_snapshot_id"],
		"policy_revision": int64(1), "policy_snapshot_digest": policy["policy_snapshot_digest"],
		"effective_limits_digest":  policy["effective_limits_digest"],
		"release_condition_digest": policy["release_condition_digest"], "dimension_set": "orch.resource.dimensions/v1",
		"dimension": "input_tokens", "unit": "token", "delta_value": int64(1), "cumulative_value": int64(1),
		"attempt_id": "attempt-1", "attempt_fence_ref": "attempt-fence-1", "measurement_source_ref": "provider-meter",
		"measurement_id": "measurement-1", "source_sequence": int64(1), "observed_at": "2035-03-04T00:00:00Z",
		"measurement_receipt_ref": "measurement-receipt", "measurement_receipt_digest": strings.Repeat("9", 64),
		"usage_operation_id": "usage-operation-1", "usage_request_digest": strings.Repeat("a", 64),
		"admission_owner_id": "resource-admission-owner", "admission_owner_generation": int64(1),
		"admission_writer_fence_ref":     "resource-admission-writer-fence",
		"admission_deployment_fence_ref": "resource-admission-deployment-fence", "security_time_generation": int64(1),
		"schema_version": int64(1), "recorded_at": "2035-03-04T00:00:00Z",
	}
}

func seedResourceUsageBehindInsertGuard(t *testing.T, db *sql.DB, usage map[string]any) {
	t.Helper()
	const trigger = "trg_orchestration_resource_usage_insert_disabled"
	var triggerSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, trigger).Scan(&triggerSQL); err != nil {
		t.Fatalf("read usage insert guard: %v", err)
	}
	if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
		t.Fatalf("drop usage insert guard for fixture: %v", err)
	}
	insertContentMapOK(t, db, resourceUsageTable(), usage)
	if _, err := db.Exec(triggerSQL); err != nil {
		t.Fatalf("restore usage insert guard: %v", err)
	}
}

type resourceLimitPair struct{ limit, reserved string }

func resourceLimitPairs() []resourceLimitPair {
	return []resourceLimitPair{
		{"limit_agent_count", "reserved_agent_count"}, {"limit_in_flight_count", "reserved_in_flight_count"},
		{"limit_fanout_count", "reserved_fanout_count"}, {"limit_concurrency_count", "reserved_concurrency_count"},
		{"limit_wall_time_ms", "reserved_wall_time_ms"}, {"limit_input_tokens", "reserved_input_tokens"},
		{"limit_output_tokens", "reserved_output_tokens"}, {"limit_cost_microunit", "reserved_cost_microunit"},
		{"limit_content_bytes", "reserved_content_bytes"}, {"limit_ingress_bytes", "reserved_ingress_bytes"},
	}
}

func resourcePolicyLimitFields() []string {
	fields := make([]string, 0, len(resourceLimitPairs()))
	for _, pair := range resourceLimitPairs() {
		fields = append(fields, pair.limit)
	}
	return fields
}

func assertResourcePolicyLimitsPositive(t *testing.T, db *sql.DB) {
	t.Helper()
	for index, field := range resourcePolicyLimitFields() {
		row := validResourcePolicyWithSuffix(1, fmt.Sprintf("zero-%d", index))
		row["policy_lineage_id"] = fmt.Sprintf("zero-lineage-%d", index)
		row["scope_ref"] = fmt.Sprintf("zero-scope-%d", index)
		digit := fmt.Sprintf("%x", index+5)
		row["policy_snapshot_digest"] = strings.Repeat(digit, 64)
		row["publish_receipt_digest"] = strings.Repeat(digit, 63) + "f"
		row[field] = int64(0)
		if err := insertContentMap(db, resourcePolicyTable(), row); err == nil {
			t.Fatalf("policy accepted zero %s", field)
		}
	}
}

type resourceDriftCase struct {
	name   string
	mutate func(map[string]any)
}

func resourceReservationDriftCases() []resourceDriftCase {
	return []resourceDriftCase{
		{"command_profile", func(row map[string]any) { row["command_profile_digest"] = strings.Repeat("b", 64) }},
		{"source_fence", func(row map[string]any) { row["source_fence_ref"] = "wrong-source-fence" }},
		{"content", func(row map[string]any) { row["content_owner_snapshot_digest"] = strings.Repeat("e", 64) }},
		{"policy_hash", func(row map[string]any) { row["policy_canonical_hash"] = strings.Repeat("d", 64) }},
		{"release_condition", func(row map[string]any) { row["release_condition_digest"] = strings.Repeat("d", 64) }},
		{"usage_watermark", func(row map[string]any) { row["usage_watermark"] = int64(1) }},
		{"usage_digest", func(row map[string]any) { row["usage_digest"] = strings.Repeat("0", 64) }},
	}
}

func resourceTableContracts() []resourceTableContract {
	return []resourceTableContract{
		resourcePolicyContract(), resourceReservationContract(), resourceUsageContract(),
	}
}

func resourcePolicyContract() resourceTableContract {
	return resourceTableContract{
		name: resourcePolicyTable(), pk: []string{"policy_snapshot_id"},
		columns: []string{
			"policy_snapshot_id", "policy_lineage_id", "policy_revision", "requested_kind",
			"caller_principal_ref", "scope_ref", "policy_applicability_digest", "dimension_set",
			"source_policy_set_ref", "source_policy_set_digest", "precedence_trace_ref", "precedence_trace_digest",
			"canonicalization_profile", "canonical_policy_hash", "policy_snapshot_digest", "effective_limits_digest",
			"release_condition_digest", "limit_agent_count", "limit_in_flight_count", "limit_fanout_count",
			"limit_concurrency_count", "limit_wall_time_ms", "limit_input_tokens", "limit_output_tokens",
			"limit_cost_microunit", "limit_content_bytes", "limit_ingress_bytes", "idempotency_namespace",
			"idempotency_key_digest", "publish_request_digest", "publish_operation_id", "publish_operation_digest",
			"publish_receipt_ref", "publish_receipt_digest", "policy_owner_id", "policy_owner_generation",
			"policy_writer_fence_ref", "policy_owner_proof_ref", "deployment_fence_ref", "security_time_generation", "effective_at",
			"valid_until", "schema_version", "created_at",
		},
		indexes: []string{"idx_orchestration_resource_policy_scope"},
		triggers: []string{
			"trg_orchestration_resource_policy_no_delete", "trg_orchestration_resource_policy_no_update",
			"trg_orchestration_resource_policy_revision_contiguous",
		},
	}
}

func resourceReservationContract() resourceTableContract {
	return resourceTableContract{
		name: resourceReservationTable(), pk: []string{"reservation_id"},
		columns: []string{
			"reservation_id", "reservation_version", "resource_snapshot_id", "resource_snapshot_digest",
			"resource_status", "requested_kind", "command_id", "command_version", "command_profile_digest",
			"caller_principal_ref", "scope_ref", "source_agent_id", "source_binding_version", "source_fence_ref",
			"target_agent_id", "target_binding_version", "target_fence_ref", "identity_fence_digest",
			"content_reference_id", "content_ref", "content_object_id", "content_object_version", "content_ref_version",
			"content_owner_snapshot_digest", "content_admission_snapshot_digest", "policy_snapshot_id", "policy_revision",
			"policy_snapshot_digest", "policy_canonical_hash", "effective_limits_digest", "reserved_agent_count",
			"reserved_in_flight_count", "reserved_fanout_count", "reserved_concurrency_count", "reserved_wall_time_ms",
			"reserved_input_tokens", "reserved_output_tokens", "reserved_cost_microunit", "reserved_content_bytes",
			"reserved_ingress_bytes", "usage_digest_domain", "usage_watermark", "usage_digest", "release_condition_digest",
			"idempotency_namespace", "idempotency_key_digest", "reservation_request_digest", "reservation_operation_id",
			"reservation_operation_digest", "reservation_receipt_ref", "reservation_receipt_digest", "admission_owner_id",
			"admission_owner_generation", "admission_writer_fence_ref", "admission_deployment_fence_ref",
			"security_time_generation", "owner_proof_ref", "valid_until", "schema_version", "created_at",
		},
		fks: []string{
			"command_id->orchestration_command_intent_profiles(command_id)/RESTRICT",
			"content_reference_id,content_ref,content_object_id,content_object_version,content_ref_version->orchestration_content_refs(reference_id,canonical_ref,object_id,object_version,ref_version)/RESTRICT",
			"policy_snapshot_id,policy_revision,policy_snapshot_digest,effective_limits_digest,release_condition_digest->orchestration_resource_effective_policy_snapshots(policy_snapshot_id,policy_revision,policy_snapshot_digest,effective_limits_digest,release_condition_digest)/RESTRICT",
			"source_agent_id,source_binding_version->orchestration_current_bindings(agent_id,binding_version)/RESTRICT",
			"target_agent_id,target_binding_version->orchestration_current_bindings(agent_id,binding_version)/RESTRICT",
		},
		indexes: []string{
			"idx_orchestration_resource_reservation_agents", "idx_orchestration_resource_reservation_policy",
			"idx_orchestration_resource_reservation_scope",
		},
		triggers: []string{
			"trg_orchestration_resource_reservation_exact_relation", "trg_orchestration_resource_reservation_no_delete",
			"trg_orchestration_resource_reservation_no_update",
		},
	}
}

func resourceUsageContract() resourceTableContract {
	return resourceTableContract{
		name: resourceUsageTable(), pk: []string{"usage_entry_id"},
		columns: []string{
			"usage_entry_id", "reservation_id", "reservation_version", "ledger_sequence", "previous_usage_digest",
			"usage_entry_digest", "command_id", "command_version", "command_profile_digest", "identity_fence_digest",
			"content_admission_snapshot_digest", "policy_snapshot_id", "policy_revision", "policy_snapshot_digest",
			"effective_limits_digest", "release_condition_digest", "dimension_set", "dimension", "unit", "delta_value",
			"cumulative_value", "attempt_id", "attempt_fence_ref", "measurement_source_ref", "measurement_id",
			"source_sequence", "observed_at", "measurement_receipt_ref", "measurement_receipt_digest",
			"usage_operation_id", "usage_request_digest", "admission_owner_id", "admission_owner_generation",
			"admission_writer_fence_ref", "admission_deployment_fence_ref", "security_time_generation",
			"schema_version", "recorded_at",
		},
		fks: []string{
			"policy_snapshot_id,policy_revision,policy_snapshot_digest,effective_limits_digest,release_condition_digest->orchestration_resource_effective_policy_snapshots(policy_snapshot_id,policy_revision,policy_snapshot_digest,effective_limits_digest,release_condition_digest)/RESTRICT",
			"reservation_id,reservation_version->orchestration_resource_reservations(reservation_id,reservation_version)/RESTRICT",
		},
		indexes: []string{"idx_orchestration_resource_usage_dimension", "idx_orchestration_resource_usage_reservation"},
		triggers: []string{
			"trg_orchestration_resource_usage_insert_disabled", "trg_orchestration_resource_usage_no_delete",
			"trg_orchestration_resource_usage_no_update",
		},
	}
}

func assertResourceTableContract(t *testing.T, db *sql.DB, contract resourceTableContract) {
	t.Helper()
	if got := tableColumns(t, db, contract.name); !slicesEqual(got, contract.columns) {
		t.Fatalf("%s columns = %v, want %v", contract.name, got, contract.columns)
	}
	assertPrimaryKey(t, db, contract.name, contract.pk)
	assertNotNullColumns(t, db, contract.name, contract.columns)
	assertTableSQLContains(t, db, contract.name, []string{"STRICT"})
	assertContentForeignKeys(t, db, contract.name, contract.fks)
	assertContentNames(t, db, "index", contract.name, contract.indexes)
	assertContentNames(t, db, "trigger", contract.name, contract.triggers)
	assertResourceNoBlobAffinity(t, db, contract.name)
}

func assertResourceNoBlobAffinity(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, affinity string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &affinity, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		if strings.EqualFold(affinity, "BLOB") {
			t.Fatalf("%s.%s uses forbidden BLOB affinity", table, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
}

func assertResourceTriggerRelations(t *testing.T, db *sql.DB) {
	t.Helper()
	triggerSQL := resourceSchemaSQL(t, db, "trigger", "trg_orchestration_resource_reservation_exact_relation")
	fragments := []string{
		"source_binding.reservation_id = profile.source_reservation_id",
		"source_binding.reservation_generation = profile.source_reservation_generation",
		"source_binding.incarnation_id = profile.source_incarnation_id",
		"source_binding.incarnation_generation = profile.source_incarnation_generation",
		"source_binding.identity_kind = profile.source_identity_kind",
		"source_binding.external_identity_ref = profile.source_external_identity_ref",
		"source_binding.binding_digest = profile.source_binding_digest",
		"source_binding.source_fence_ref = profile.source_fence_ref",
		"source_binding.active_process_receipt_ref = profile.source_active_process_receipt_ref",
		"source_binding.state = profile.source_binding_state",
		"source_binding.schema_version = profile.source_binding_schema_version",
		"julianday(source_binding.updated_at) = julianday(profile.source_binding_updated_at)",
		"target_binding.reservation_id = profile.target_reservation_id",
		"target_binding.reservation_generation = profile.target_reservation_generation",
		"target_binding.incarnation_id = profile.target_incarnation_id",
		"target_binding.incarnation_generation = profile.target_incarnation_generation",
		"target_binding.identity_kind = profile.target_identity_kind",
		"target_binding.external_identity_ref = profile.target_external_identity_ref",
		"target_binding.binding_digest = profile.target_binding_digest",
		"target_binding.source_fence_ref = profile.target_fence_ref",
		"target_binding.active_process_receipt_ref = profile.target_active_process_receipt_ref",
		"target_binding.state = profile.target_binding_state",
		"target_binding.schema_version = profile.target_binding_schema_version",
		"julianday(target_binding.updated_at) = julianday(profile.target_binding_updated_at)",
		"content_ref.caller_principal_ref = NEW.caller_principal_ref",
		"content_ref.scope_ref = NEW.scope_ref",
		"content_ref.source_agent_id = NEW.source_agent_id",
		"content_ref.source_fence_ref = NEW.source_fence_ref",
		"content_ingest.caller_principal_ref = NEW.caller_principal_ref",
		"content_ingest.scope_ref = NEW.scope_ref",
		"content_ingest.source_agent_id = NEW.source_agent_id",
		"content_ingest.source_fence_ref = NEW.source_fence_ref",
		"policy.canonical_policy_hash = NEW.policy_canonical_hash",
		"policy.release_condition_digest = NEW.release_condition_digest",
		"newer_policy.policy_revision > policy.policy_revision",
	}
	for _, fragment := range fragments {
		if !strings.Contains(triggerSQL, fragment) {
			t.Fatalf("reservation relation trigger missing %q", fragment)
		}
	}
}

func assertResourceWideLimits(t *testing.T, db *sql.DB) {
	t.Helper()
	policySQL := resourceSchemaSQL(t, db, "table", resourcePolicyTable())
	reservationSQL := resourceSchemaSQL(t, db, "table", resourceReservationTable())
	for _, pair := range resourceLimitPairs() {
		if !strings.Contains(policySQL, "CHECK("+pair.limit+" > 0)") {
			t.Fatalf("policy schema lacks positive guard for %s", pair.limit)
		}
		if !strings.Contains(reservationSQL, "CHECK("+pair.reserved+" > 0)") {
			t.Fatalf("reservation schema lacks positive guard for %s", pair.reserved)
		}
	}
	if !strings.Contains(policySQL, "dimension_set = 'orch.resource.dimensions/v1'") {
		t.Fatal("policy schema lacks the canonical dimension set")
	}
	for _, fragment := range []string{
		"usage_watermark = 0",
		"c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31",
	} {
		if !strings.Contains(reservationSQL, fragment) {
			t.Fatalf("reservation schema lacks canonical fragment %q", fragment)
		}
	}
}

func resourceSchemaSQL(t *testing.T, db *sql.DB, kind, name string) string {
	t.Helper()
	var sqlText string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type=? AND name=?`, kind, name).Scan(&sqlText); err != nil {
		t.Fatalf("read %s SQL for %s: %v", kind, name, err)
	}
	return sqlText
}

func assertResourceMutationRejected(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	for _, statement := range []string{"UPDATE " + table + " SET created_at=created_at", "DELETE FROM " + table} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("immutable table %s accepted %s", table, statement)
		}
	}
}

func resourcePrerequisiteCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	tables := []string{
		"orchestration_command_intents", "orchestration_command_intent_profiles",
		"orchestration_content_ingest_reservations", "orchestration_content_objects",
		"orchestration_content_private_payload_receipts", "orchestration_content_refs",
		"orchestration_command_content_deletion_fences",
	}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		counts[table] = resourceTableRowCount(t, db, table)
	}
	return counts
}

func assertResourcePrerequisiteCounts(t *testing.T, db *sql.DB, want map[string]int) {
	t.Helper()
	for table, count := range want {
		if got := resourceTableRowCount(t, db, table); got != count {
			t.Fatalf("upgrade changed %s rows: got %d, want %d", table, got, count)
		}
	}
}

func assertResourceCounts(t *testing.T, db *sql.DB, tables []string, want int) {
	t.Helper()
	for _, table := range tables {
		if got := resourceTableRowCount(t, db, table); got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}

func resourceTableRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func resourceTableNames() []string {
	return []string{resourcePolicyTable(), resourceReservationTable(), resourceUsageTable()}
}

func resourcePolicyTable() string { return "orchestration_resource_effective_policy_snapshots" }

func resourceReservationTable() string { return "orchestration_resource_reservations" }

func resourceUsageTable() string { return "orchestration_resource_usage_ledger" }
