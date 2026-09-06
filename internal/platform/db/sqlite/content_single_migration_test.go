package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

type contentTableContract struct {
	name     string
	columns  []string
	pk       []string
	fks      []string
	indexes  []string
	triggers []string
}

// TestContentSingleMigrationContract 聚合 CONTENT-1 五表 schema、迁移、身份绑定、
// 密文元数据生命周期、命令引用围栏和 append-only 约束。
func TestContentSingleMigrationContract(t *testing.T) {
	t.Run("schema", assertContentSingleSchema)
	t.Run("upgrade_empty_v131", assertContentUpgradeFromEmptyV131)
	t.Run("quarantine_nonempty_v131", assertContentUpgradeQuarantinesProfile)
	t.Run("canonical_text", assertContentCanonicalText)
	t.Run("size_bounds", assertContentSizeBounds)
	t.Run("identity_and_metadata_drift", assertContentRelationDrift)
	t.Run("nonce_and_key_reuse", assertContentNonceAndKeyReuse)
	t.Run("lifecycle_and_profile_fence", assertContentLifecycleAndProfileFence)
	t.Run("profile_failure_is_atomic", assertContentProfileFailureAtomic)
}

func assertContentSingleSchema(t *testing.T) {
	t.Helper()
	db := openContentSingleDB(t)
	contracts := contentTableContracts()
	if got := contentOwnedTables(t, db); !slicesEqual(got, contentTableNames(contracts)) {
		t.Fatalf("CONTENT-1 tables = %v, want %v", got, contentTableNames(contracts))
	}
	for _, contract := range contracts {
		contract := contract
		t.Run(contract.name, func(t *testing.T) {
			if got := tableColumns(t, db, contract.name); !slicesEqual(got, contract.columns) {
				t.Fatalf("%s columns = %v, want %v", contract.name, got, contract.columns)
			}
			assertPrimaryKey(t, db, contract.name, contract.pk)
			assertTableSQLContains(t, db, contract.name, []string{"STRICT"})
			assertContentForeignKeys(t, db, contract.name, contract.fks)
			assertContentNames(t, db, "index", contract.name, contract.indexes)
			assertContentNames(t, db, "trigger", contract.name, contract.triggers)
			assertContentHasNoSecretColumns(t, db, contract.name)
		})
	}
	assertContentProfileTriggers(t, db)
	assertContentHasNoTerminalSecretStates(t, db, contracts)
}

func assertContentUpgradeFromEmptyV131(t *testing.T) {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, contentMigrationsThrough131Dir(t)); err != nil {
		t.Fatalf("apply migrations through v131: %v", err)
	}
	assertMaxMigrationVersion(t, db, 131)
	assertProfileRowCount(t, db, 0)
	dir := contentMigration132Dir(t)
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatalf("upgrade empty v131 to v132: %v", err)
	}
	assertMaxMigrationVersion(t, db, 132)
	assertMigrationMarkerCount(t, db, "132_orchestration_content_single.sql", 1)
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatalf("repeat v132 migration: %v", err)
	}
	assertMigrationMarkerCount(t, db, "132_orchestration_content_single.sql", 1)
}

func assertContentUpgradeQuarantinesProfile(t *testing.T) {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, contentMigrationsThrough131Dir(t)); err != nil {
		t.Fatalf("apply migrations through v131: %v", err)
	}
	seedCommandIntentProfileRelations(t, db)
	profile := validCommandIntentProfileRow()
	if err := insertCommandIntentProfile(db, profile); err != nil {
		t.Fatalf("insert v131 profile fixture: %v", err)
	}
	dir := contentMigration132Dir(t)
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatalf("upgrade nonempty v131 profile to quarantine: %v", err)
	}
	assertMaxMigrationVersion(t, db, 132)
	assertMigrationMarkerCount(t, db, "132_orchestration_content_single.sql", 1)
	assertProfileRowCount(t, db, 1)
	var digest, reason string
	if err := db.QueryRow(`SELECT complete_intent_digest, reason
		FROM orchestration_legacy_command_content_quarantine WHERE command_id='profile-command'`).Scan(&digest, &reason); err != nil {
		t.Fatalf("read legacy CONTENT quarantine: %v", err)
	}
	if digest != profile["complete_intent_digest"] || reason != "legacy_missing_content_owner_ref" {
		t.Fatalf("legacy CONTENT quarantine = digest:%q reason:%q", digest, reason)
	}
	assertLegacyContentQuarantineImmutable(t, db)
	for _, table := range []string{"orchestration_content_ingest_reservations", "orchestration_content_objects", "orchestration_content_refs", "orchestration_command_content_deletion_fences"} {
		assertContentTableRowCount(t, db, table, 0)
	}
	if err := RunMigrations(context.Background(), db, dir); err != nil {
		t.Fatalf("repeat nonempty v131 quarantine migration: %v", err)
	}
}

func assertContentTableRowCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func assertContentSizeBounds(t *testing.T) {
	t.Helper()
	for _, test := range []struct {
		size int64
		ok   bool
	}{{1, true}, {65536, true}, {0, false}, {65537, false}} {
		t.Run(fmt.Sprintf("size_%d", test.size), func(t *testing.T) {
			db := openContentSingleDB(t)
			seedContentSourceIdentity(t, db)
			err := insertContentMap(db, "orchestration_content_ingest_reservations", validContentReservation(test.size, ""))
			if (err == nil) != test.ok {
				t.Fatalf("insert size %d error = %v, want success=%v", test.size, err, test.ok)
			}
		})
	}
}

func assertContentRelationDrift(t *testing.T) {
	t.Helper()
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *sql.DB, map[string]any)
	}{
		{"binding_metadata", func(_ *testing.T, _ *sql.DB, row map[string]any) {
			row["source_external_identity_ref"] = "forged-external-ref"
		}},
		{"latest_binding", func(t *testing.T, db *sql.DB, _ map[string]any) {
			insertProfileBinding(t, db, "src", 2, "revoked", "2035-01-02T00:00:00Z")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openContentSingleDB(t)
			seedContentSourceIdentity(t, db)
			row := validContentReservation(32, "")
			test.mutate(t, db, row)
			if err := insertContentMap(db, "orchestration_content_ingest_reservations", row); err == nil {
				t.Fatal("identity relation drift was accepted")
			}
		})
	}
	t.Run("object_metadata", assertContentObjectMetadataDrift)
}

func assertContentObjectMetadataDrift(t *testing.T) {
	t.Helper()
	db := openContentSingleDB(t)
	seedContentSourceIdentity(t, db)
	reservation := validContentReservation(32, "")
	insertContentMapOK(t, db, "orchestration_content_ingest_reservations", reservation)
	startContentPreparation(t, db, reservation, "")
	object := validContentObject(reservation, "")
	object["media_type"] = "application/forged"
	if err := insertContentMap(db, "orchestration_content_objects", object); err == nil {
		t.Fatal("object/reservation metadata mismatch was accepted")
	}
}

func assertContentNonceAndKeyReuse(t *testing.T) {
	t.Helper()
	for _, field := range []string{"nonce_hex", "key_handle_ref"} {
		t.Run(field, func(t *testing.T) {
			db := openContentSingleDB(t)
			seedContentSourceIdentity(t, db)
			firstReservation := validContentReservation(32, "-a")
			insertContentMapOK(t, db, "orchestration_content_ingest_reservations", firstReservation)
			startContentPreparation(t, db, firstReservation, "-a")
			firstObject := validContentObject(firstReservation, "-a")
			insertContentMapOK(t, db, "orchestration_content_objects", firstObject)
			secondReservation := validContentReservation(32, "-b")
			if field == "nonce_hex" {
				secondReservation["owner_id"] = firstReservation["owner_id"]
			}
			insertContentMapOK(t, db, "orchestration_content_ingest_reservations", secondReservation)
			startContentPreparation(t, db, secondReservation, "-b")
			secondObject := validContentObject(secondReservation, "-b")
			secondObject[field] = firstObject[field]
			if err := insertContentMap(db, "orchestration_content_objects", secondObject); err == nil {
				t.Fatalf("duplicate %s was accepted", field)
			}
		})
	}
}

func assertContentLifecycleAndProfileFence(t *testing.T) {
	t.Helper()
	db := openContentSingleDB(t)
	seedCommandIntentProfileRelations(t, db)
	reservation := validContentReservation(32, "")
	insertContentMapOK(t, db, "orchestration_content_ingest_reservations", reservation)
	startContentPreparation(t, db, reservation, "")
	object := validContentObject(reservation, "")
	insertContentMapOK(t, db, "orchestration_content_objects", object)
	assertContentState(t, db, "orchestration_content_objects", "content_state", "prepared")
	insertContentMapOK(t, db, "orchestration_content_private_payload_receipts", validContentReceipt(object))
	contentExecOK(t, db, `UPDATE orchestration_content_ingest_reservations SET state='sealed', revision=2 WHERE ingest_reservation_id=?`, reservation["ingest_reservation_id"])
	assertContentState(t, db, "orchestration_content_ingest_reservations", "state", "sealed")
	contentExecOK(t, db, `UPDATE orchestration_content_objects SET content_state='active', revision=2, ref_version=1, promoted_at='2035-03-01T00:00:00Z' WHERE object_id=?`, object["object_id"])
	assertContentState(t, db, "orchestration_content_objects", "content_state", "active")
	insertContentMapOK(t, db, "orchestration_content_refs", validContentRef(reservation))
	contentExecOK(t, db, `UPDATE orchestration_content_ingest_reservations SET state='promoted', revision=3 WHERE ingest_reservation_id=?`, reservation["ingest_reservation_id"])
	assertContentState(t, db, "orchestration_content_ingest_reservations", "state", "promoted")
	if err := insertCommandIntentProfile(db, validCommandIntentProfileRow()); err != nil {
		t.Fatalf("insert profile bound to live CONTENT ref: %v", err)
	}
	assertContentFenceHeld(t, db)
	assertAllContentTablesImmutable(t, db)
}

func assertContentProfileFailureAtomic(t *testing.T) {
	t.Helper()
	db := openContentSingleDB(t)
	seedCommandIntentProfileRelations(t, db)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin failing profile transaction: %v", err)
	}
	fixture := contentRollbackCommand()
	if err := insertContentCommand(tx, fixture); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert rollback command base: %v", err)
	}
	row := validCommandIntentProfileRow()
	row["command_id"] = fixture.CommandID
	if err := insertContentMap(tx, "orchestration_command_intent_profiles", row); err == nil {
		_ = tx.Rollback()
		t.Fatal("profile without a live CONTENT owner ref was accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback failed profile transaction: %v", err)
	}
	for _, table := range []string{"orchestration_command_intents", "orchestration_command_intent_profiles", "orchestration_command_content_deletion_fences"} {
		assertContentCommandRows(t, db, table, fixture.CommandID, 0)
	}
}

func openContentSingleDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, contentMigrationsThrough132Dir(t)); err != nil {
		t.Fatalf("run migrations through v132: %v", err)
	}
	assertMaxMigrationVersion(t, db, 132)
	assertMigrationMarkerCount(t, db, "132_orchestration_content_single.sql", 1)
	return db
}

func enableContentForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
}

func contentMigrationsThrough131Dir(t *testing.T) string {
	t.Helper()
	dir := migrationsThrough130Dir(t)
	writeMigrationTestFile(t, dir, "131_orchestration_command_intent_profiles.sql", readMigrationTestFile(t, "131_orchestration_command_intent_profiles.sql"))
	return dir
}

func contentMigrationsThrough132Dir(t *testing.T) string {
	t.Helper()
	dir := contentMigrationsThrough131Dir(t)
	writeMigrationTestFile(t, dir, "132_orchestration_content_single.sql", readMigrationTestFile(t, "132_orchestration_content_single.sql"))
	return dir
}

func contentMigration132Dir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "132_orchestration_content_single.sql", readMigrationTestFile(t, "132_orchestration_content_single.sql"))
	return dir
}

func contentTableContracts() []contentTableContract {
	return []contentTableContract{
		contentReservationContract(), contentObjectContract(), contentReceiptContract(),
		contentRefContract(), contentFenceContract(),
	}
}

func contentReservationContract() contentTableContract {
	return contentTableContract{
		name: "orchestration_content_ingest_reservations",
		columns: []string{
			"ingest_reservation_id", "ingest_ticket_id", "object_id", "object_version", "owner_id",
			"owner_generation", "writer_fence_ref", "security_time_generation", "workspace_ref",
			"caller_principal_ref", "scope_ref", "source_agent_id", "source_identity_kind",
			"source_identity_reservation_id", "source_reservation_generation", "source_incarnation_id",
			"source_incarnation_generation", "source_external_identity_ref", "source_binding_version",
			"source_binding_digest", "source_fence_ref", "source_active_process_receipt_ref",
			"source_binding_updated_at", "idempotency_namespace", "idempotency_key_digest",
			"proof_request_digest", "candidate_ref_digest", "content_intent_digest", "digest_key_id",
			"digest_key_version", "ingest_kind", "size_bytes", "media_type", "permitted_sink_role",
			"aead_algorithm", "key_wrap_profile", "aad_profile", "quota_snapshot_ref",
			"quota_snapshot_digest", "retention_policy_id", "retention_policy_version",
			"retention_policy_digest", "assembly_deadline_at", "prepare_operation_id", "prepare_started_at",
			"revision", "state", "schema_version",
			"created_at", "updated_at",
		},
		pk: []string{"ingest_reservation_id"},
		fks: []string{
			"source_agent_id,source_binding_version->orchestration_current_bindings(agent_id,binding_version)/RESTRICT",
			"source_identity_reservation_id,source_agent_id,source_identity_kind->orchestration_identity_reservations(reservation_id,agent_id,identity_kind)/RESTRICT",
			"source_incarnation_id,source_identity_reservation_id,source_agent_id,source_identity_kind->orchestration_execution_incarnations(incarnation_id,reservation_id,agent_id,identity_kind)/RESTRICT",
		},
		indexes: []string{"idx_orchestration_content_ingest_state_deadline"},
		triggers: []string{
			"trg_orchestration_content_ingest_exact_identity", "trg_orchestration_content_ingest_immutable_metadata",
			"trg_orchestration_content_ingest_no_delete", "trg_orchestration_content_ingest_state_machine",
		},
	}
}

func contentObjectContract() contentTableContract {
	return contentTableContract{
		name: "orchestration_content_objects",
		columns: []string{
			"object_id", "object_version", "ingest_reservation_id", "owner_id", "owner_generation",
			"writer_fence_ref", "security_time_generation", "workspace_ref", "source_agent_id",
			"source_fence_ref", "aead_algorithm", "key_wrap_profile", "key_handle_ref", "key_receipt_ref",
			"key_receipt_digest", "nonce_hex", "aad_profile", "aad_digest", "media_type", "size_bytes",
			"ciphertext_sha256", "sealed_payload_size_bytes", "retention_policy_digest", "content_state",
			"key_state", "ref_version", "revision", "promoted_at", "schema_version", "created_at", "updated_at",
		},
		pk: []string{"object_id", "object_version"},
		fks: []string{
			"ingest_reservation_id,object_id,object_version->orchestration_content_ingest_reservations(ingest_reservation_id,object_id,object_version)/RESTRICT",
		},
		indexes: []string{"idx_orchestration_content_objects_state"},
		triggers: []string{
			"trg_orchestration_content_objects_exact_reservation", "trg_orchestration_content_objects_immutable_metadata",
			"trg_orchestration_content_objects_no_delete", "trg_orchestration_content_objects_prepared_to_active",
		},
	}
}

func contentReceiptContract() contentTableContract {
	return contentTableContract{
		name: "orchestration_content_private_payload_receipts",
		columns: []string{
			"object_id", "object_version", "ingest_reservation_id", "backend_kind", "object_store_id",
			"payload_format", "private_payload_ref", "sealed_payload_size_bytes", "ciphertext_sha256", "store_operation_id",
			"store_request_digest", "store_receipt_ref", "store_receipt_digest", "stored_at", "schema_version",
		},
		pk: []string{"object_id", "object_version"},
		fks: []string{
			"object_id,object_version,ingest_reservation_id->orchestration_content_objects(object_id,object_version,ingest_reservation_id)/RESTRICT",
		},
		triggers: []string{
			"trg_orchestration_content_payload_exact_relation", "trg_orchestration_content_payload_no_delete",
			"trg_orchestration_content_payload_no_update",
		},
	}
}

func contentRefContract() contentTableContract {
	return contentTableContract{
		name: "orchestration_content_refs",
		columns: []string{
			"reference_id", "canonical_ref", "object_id", "object_version", "ingest_reservation_id",
			"ref_version", "reference_kind", "reference_state", "caller_principal_ref", "scope_ref",
			"source_agent_id", "source_fence_ref", "proof_request_digest", "proof_id", "proof_revision",
			"owner_id", "owner_generation", "writer_fence_ref", "security_time_generation", "owner_proof_ref",
			"proof_snapshot_digest", "proof_expires_at", "media_type", "size_bytes",
			"retention_policy_digest", "schema_version", "created_at",
		},
		pk: []string{"reference_id"},
		fks: []string{
			"object_id,object_version,ingest_reservation_id->orchestration_content_objects(object_id,object_version,ingest_reservation_id)/RESTRICT",
		},
		indexes: []string{"idx_orchestration_content_refs_object"},
		triggers: []string{
			"trg_orchestration_content_refs_exact_active_relation", "trg_orchestration_content_refs_no_delete",
			"trg_orchestration_content_refs_no_update",
		},
	}
}

func contentFenceContract() contentTableContract {
	return contentTableContract{
		name: "orchestration_command_content_deletion_fences",
		columns: []string{
			"command_id", "reference_id", "canonical_ref", "object_id", "object_version", "ref_version",
			"fence_kind", "fence_state", "command_profile_digest", "source_fence_ref", "target_fence_ref",
			"schema_version", "created_at",
		},
		pk: []string{"command_id"},
		fks: []string{
			"command_id->orchestration_command_intent_profiles(command_id)/RESTRICT",
			"reference_id,canonical_ref,object_id,object_version,ref_version->orchestration_content_refs(reference_id,canonical_ref,object_id,object_version,ref_version)/RESTRICT",
		},
		indexes: []string{"idx_orchestration_content_fences_object"},
		triggers: []string{
			"trg_orchestration_command_content_fences_exact_relation",
			"trg_orchestration_command_content_fences_no_delete",
			"trg_orchestration_command_content_fences_no_update",
		},
	}
}

func contentTableNames(contracts []contentTableContract) []string {
	names := make([]string, len(contracts))
	for index, contract := range contracts {
		names[index] = contract.name
	}
	sort.Strings(names)
	return names
}

func contentOwnedTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'orchestration_content_%' OR name='orchestration_command_content_deletion_fences') ORDER BY name`)
	if err != nil {
		t.Fatalf("list CONTENT-1 tables: %v", err)
	}
	defer rows.Close()
	return scanContentNames(t, rows)
}

func assertContentNames(t *testing.T, db *sql.DB, kind, table string, want []string) {
	t.Helper()
	query := `SELECT name FROM sqlite_master WHERE type=? AND tbl_name=?`
	if kind == "index" {
		query += ` AND name NOT LIKE 'sqlite_autoindex_%'`
	}
	query += ` ORDER BY name`
	rows, err := db.Query(query, kind, table)
	if err != nil {
		t.Fatalf("list %s names for %s: %v", kind, table, err)
	}
	defer rows.Close()
	got := scanContentNames(t, rows)
	sort.Strings(want)
	if !slicesEqual(got, want) {
		t.Fatalf("%s %s names = %v, want %v", table, kind, got, want)
	}
}

func scanContentNames(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan SQLite object name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite object names: %v", err)
	}
	return names
}

func assertContentForeignKeys(t *testing.T, db *sql.DB, table string, want []string) {
	t.Helper()
	got := contentForeignKeySignatures(t, db, table)
	sort.Strings(want)
	if !slicesEqual(got, want) {
		t.Fatalf("%s foreign keys = %v, want %v", table, got, want)
	}
}

func contentForeignKeySignatures(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
	if err != nil {
		t.Fatalf("foreign_key_list %s: %v", table, err)
	}
	defer rows.Close()
	type part struct {
		seq      int
		from, to string
	}
	parts, parents, deletes := map[int][]part{}, map[int]string{}, map[int]string{}
	for rows.Next() {
		var id, seq int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign_key_list %s: %v", table, err)
		}
		parts[id], parents[id], deletes[id] = append(parts[id], part{seq, from, to}), parent, onDelete
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_list %s: %v", table, err)
	}
	var signatures []string
	for id, group := range parts {
		sort.Slice(group, func(i, j int) bool { return group[i].seq < group[j].seq })
		from, to := make([]string, len(group)), make([]string, len(group))
		for index, item := range group {
			from[index], to[index] = item.from, item.to
		}
		signatures = append(signatures, strings.Join(from, ",")+"->"+parents[id]+"("+strings.Join(to, ",")+")/"+deletes[id])
	}
	sort.Strings(signatures)
	return signatures
}

func assertContentHasNoSecretColumns(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	banned := map[string]bool{"plaintext": true, "ciphertext": true, "body": true, "tag": true, "dek": true, "kek": true}
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		if banned[strings.ToLower(name)] || strings.EqualFold(kind, "BLOB") {
			t.Fatalf("%s has forbidden secret-bearing column %s %s", table, name, kind)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
}

func assertContentProfileTriggers(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range []string{
		"trg_orchestration_command_profiles_content_relation",
		"trg_orchestration_command_profiles_create_content_fence",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND tbl_name='orchestration_command_intent_profiles' AND name=?`, name).Scan(&count); err != nil {
			t.Fatalf("lookup profile trigger %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("profile trigger %s count = %d, want 1", name, count)
		}
	}
}

func assertContentHasNoTerminalSecretStates(t *testing.T, db *sql.DB, contracts []contentTableContract) {
	t.Helper()
	for _, contract := range contracts {
		var sqlText string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, contract.name).Scan(&sqlText); err != nil {
			t.Fatalf("read table SQL for %s: %v", contract.name, err)
		}
		lower := strings.ToLower(sqlText)
		for _, state := range []string{"'delete'", "'deleted'", "'destroy'", "'destroyed'", "'decrypt'", "'decrypted'"} {
			if strings.Contains(lower, state) {
				t.Fatalf("%s exposes forbidden lifecycle state %s", contract.name, state)
			}
		}
	}
}

func seedContentSourceIdentity(t *testing.T, db *sql.DB) {
	t.Helper()
	seedProfileIdentity(t, db, "src")
	insertProfileBinding(t, db, "src", 1, "active", "2035-01-01T00:00:00Z")
}

func validContentReservation(size int64, suffix string) map[string]any {
	digest := strings.Repeat
	return map[string]any{
		"ingest_reservation_id": "content-reservation" + suffix, "ingest_ticket_id": "content-ticket" + suffix,
		"object_id": "content-object" + suffix, "object_version": int64(1), "owner_id": "content-owner" + suffix,
		"owner_generation": int64(1), "writer_fence_ref": "content-writer-fence" + suffix,
		"security_time_generation": int64(1), "workspace_ref": "content-workspace", "caller_principal_ref": "principal",
		"scope_ref": "scope", "source_agent_id": "src", "source_identity_kind": "native_subagent",
		"source_identity_reservation_id": "profile-reservation-src", "source_reservation_generation": int64(1),
		"source_incarnation_id": "profile-incarnation-src", "source_incarnation_generation": int64(1),
		"source_external_identity_ref": "external-src", "source_binding_version": int64(1),
		"source_binding_digest": profileBindingDigest("src"), "source_fence_ref": "source-fence-src",
		"source_active_process_receipt_ref": "active-process-src", "source_binding_updated_at": "2035-01-01T00:00:00Z",
		"idempotency_namespace": "content/ingest" + suffix, "idempotency_key_digest": digest("4", 64),
		"proof_request_digest": digest("5", 64), "candidate_ref_digest": digest("6", 64),
		"content_intent_digest": digest("7", 64), "digest_key_id": "content-digest-key", "digest_key_version": int64(1),
		"ingest_kind": "content.single", "size_bytes": size, "media_type": "text/plain",
		"permitted_sink_role": "command_request", "aead_algorithm": "aes-256-gcm", "key_wrap_profile": "kms-handle-v1",
		"aad_profile": "orch.content.single.aad/v1", "quota_snapshot_ref": "quota-snapshot" + suffix,
		"quota_snapshot_digest": digest("8", 64), "retention_policy_id": "retention-policy",
		"retention_policy_version": int64(1), "retention_policy_digest": digest("9", 64),
		"assembly_deadline_at": "2036-01-01T00:00:00Z", "revision": int64(1), "state": "reserved", "schema_version": int64(1),
	}
}

func validContentObject(reservation map[string]any, suffix string) map[string]any {
	digest := strings.Repeat
	size := reservation["size_bytes"].(int64)
	return map[string]any{
		"object_id": reservation["object_id"], "object_version": int64(1),
		"ingest_reservation_id": reservation["ingest_reservation_id"], "owner_id": reservation["owner_id"],
		"owner_generation": reservation["owner_generation"], "writer_fence_ref": reservation["writer_fence_ref"],
		"security_time_generation": reservation["security_time_generation"], "workspace_ref": reservation["workspace_ref"],
		"source_agent_id": "src", "source_fence_ref": "source-fence-src", "aead_algorithm": "aes-256-gcm",
		"key_wrap_profile": "kms-handle-v1", "key_handle_ref": "key-handle" + suffix,
		"key_receipt_ref": "key-receipt" + suffix, "key_receipt_digest": digest("a", 64),
		"nonce_hex": fmt.Sprintf("%024x", len(suffix)+1), "aad_profile": "orch.content.single.aad/v1",
		"aad_digest": digest("b", 64), "media_type": "text/plain", "size_bytes": size,
		"ciphertext_sha256": digest("c", 64), "sealed_payload_size_bytes": size + 16,
		"retention_policy_digest": digest("9", 64), "content_state": "prepared", "key_state": "active",
		"ref_version": int64(0), "revision": int64(1), "promoted_at": "", "schema_version": int64(1),
	}
}

func startContentPreparation(t *testing.T, db *sql.DB, reservation map[string]any, suffix string) {
	t.Helper()
	contentExecOK(t, db, `UPDATE orchestration_content_ingest_reservations
		SET prepare_operation_id=?, prepare_started_at=?, updated_at=?
		WHERE ingest_reservation_id=?`, "content-prepare-operation"+suffix, "2035-01-15T00:00:00Z",
		"2035-01-15T00:00:00Z", reservation["ingest_reservation_id"])
}

func validContentReceipt(object map[string]any) map[string]any {
	return map[string]any{
		"object_id": object["object_id"], "object_version": int64(1),
		"ingest_reservation_id": object["ingest_reservation_id"], "backend_kind": "private_state_object_v1",
		"object_store_id": "private-content-store", "payload_format": "aes256gcm-ciphertext-tag-v1", "private_payload_ref": "private-payload-ref",
		"sealed_payload_size_bytes": object["sealed_payload_size_bytes"], "ciphertext_sha256": object["ciphertext_sha256"],
		"store_operation_id": "private-store-operation", "store_request_digest": strings.Repeat("d", 64),
		"store_receipt_ref": "private-store-receipt", "store_receipt_digest": strings.Repeat("e", 64),
		"stored_at": "2035-02-01T00:00:00Z", "schema_version": int64(1),
	}
}

func validContentRef(reservation map[string]any) map[string]any {
	return map[string]any{
		"reference_id": "content-reference", "canonical_ref": "content-ref-1", "object_id": reservation["object_id"],
		"object_version": int64(1), "ingest_reservation_id": reservation["ingest_reservation_id"],
		"ref_version": int64(1), "reference_kind": "command_request", "reference_state": "live",
		"caller_principal_ref": "principal", "scope_ref": "scope", "source_agent_id": "src",
		"source_fence_ref": "source-fence-src", "proof_request_digest": strings.Repeat("5", 64),
		"proof_id": "content-proof", "proof_revision": int64(1), "owner_id": reservation["owner_id"],
		"owner_generation": reservation["owner_generation"], "writer_fence_ref": reservation["writer_fence_ref"],
		"security_time_generation": reservation["security_time_generation"], "owner_proof_ref": "content-owner-proof",
		"proof_snapshot_digest": strings.Repeat("f", 64), "proof_expires_at": "2035-06-01T00:00:00Z",
		"media_type": "text/plain", "size_bytes": reservation["size_bytes"],
		"retention_policy_digest": strings.Repeat("9", 64), "schema_version": int64(1),
	}
}

func insertContentMap(execer interface {
	Exec(string, ...any) (sql.Result, error)
}, table string, row map[string]any) error {
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
	_, err := execer.Exec(query, values...)
	return err
}

func insertContentMapOK(t *testing.T, execer interface {
	Exec(string, ...any) (sql.Result, error)
}, table string, row map[string]any) {
	t.Helper()
	if err := insertContentMap(execer, table, row); err != nil {
		t.Fatalf("insert %s: %v", table, err)
	}
}

func contentExecOK(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execute CONTENT lifecycle statement: %v", err)
	}
}

func assertContentState(t *testing.T, db *sql.DB, table, column, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("SELECT " + column + " FROM " + table + " LIMIT 1").Scan(&got); err != nil {
		t.Fatalf("read %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("%s.%s = %q, want %q", table, column, got, want)
	}
}

func assertContentFenceHeld(t *testing.T, db *sql.DB) {
	t.Helper()
	var state, kind, reference string
	if err := db.QueryRow(`SELECT fence_state, fence_kind, canonical_ref FROM orchestration_command_content_deletion_fences WHERE command_id='profile-command'`).Scan(&state, &kind, &reference); err != nil {
		t.Fatalf("read generated content deletion fence: %v", err)
	}
	if state != "held" || kind != "non_terminal_command" || reference != "content-ref-1" {
		t.Fatalf("generated fence = state:%q kind:%q ref:%q", state, kind, reference)
	}
}

func assertAllContentTablesImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, test := range []struct{ name, update, delete string }{
		{"reservation", `UPDATE orchestration_content_ingest_reservations SET updated_at='2035-04-01T00:00:00Z'`, `DELETE FROM orchestration_content_ingest_reservations`},
		{"object", `UPDATE orchestration_content_objects SET updated_at='2035-04-01T00:00:00Z'`, `DELETE FROM orchestration_content_objects`},
		{"receipt", `UPDATE orchestration_content_private_payload_receipts SET stored_at='2035-04-01T00:00:00Z'`, `DELETE FROM orchestration_content_private_payload_receipts`},
		{"ref", `UPDATE orchestration_content_refs SET owner_proof_ref='forged'`, `DELETE FROM orchestration_content_refs`},
		{"fence", `UPDATE orchestration_command_content_deletion_fences SET source_fence_ref='forged'`, `DELETE FROM orchestration_command_content_deletion_fences`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec(test.update); err == nil {
				t.Fatal("immutable row accepted UPDATE")
			}
			if _, err := db.Exec(test.delete); err == nil {
				t.Fatal("immutable row accepted DELETE")
			}
		})
	}
}

func contentRollbackCommand() commandIntentFixture {
	fixture := validCommandIntentFixture()
	fixture.CommandID = "profile-command-rollback"
	fixture.CommandKind = "send_message"
	fixture.CallerPrincipalRef, fixture.ScopeRef = "principal", "scope"
	fixture.IdempotencyNamespace = "profile/command/rollback"
	fixture.IdempotencyKeyDigest, fixture.RequestDigest = strings.Repeat("f", 64), strings.Repeat("a", 64)
	fixture.DigestKeyID, fixture.RequestContentRef = "profile-key", "missing-content-ref"
	fixture.SourceFenceRef, fixture.TargetFenceRef = "source-fence-src", "source-fence-dst"
	return fixture
}

func insertContentCommand(execer interface {
	Exec(string, ...any) (sql.Result, error)
}, fixture commandIntentFixture) error {
	_, err := execer.Exec(`INSERT INTO orchestration_command_intents (
		command_id, command_kind, caller_principal_ref, scope_ref, idempotency_namespace,
		idempotency_key_digest, request_digest, digest_key_id, request_content_ref,
		source_fence_ref, target_fence_ref, schema_version, command_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, fixture.CommandID, fixture.CommandKind,
		fixture.CallerPrincipalRef, fixture.ScopeRef, fixture.IdempotencyNamespace, fixture.IdempotencyKeyDigest,
		fixture.RequestDigest, fixture.DigestKeyID, fixture.RequestContentRef, fixture.SourceFenceRef,
		fixture.TargetFenceRef, fixture.SchemaVersion, fixture.CommandVersion)
	return err
}

func assertContentCommandRows(t *testing.T, db *sql.DB, table, commandID string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE command_id=?", commandID).Scan(&got); err != nil {
		t.Fatalf("count %s rows for %s: %v", table, commandID, err)
	}
	if got != want {
		t.Fatalf("%s rows for %s = %d, want %d", table, commandID, got, want)
	}
}
