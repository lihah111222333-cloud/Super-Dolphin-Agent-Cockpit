package sqlite

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"
)

const commandIntentProfileMigration = "131_orchestration_command_intent_profiles.sql"

// TestCommandIntentProfileMigrationContract locks the v131 schema and exact
// parent/grant/current-binding relation enforced at the insert boundary.
func TestCommandIntentProfileMigrationContract(t *testing.T) {
	db := openCommandIntentProfileDB(t)
	assertCommandIntentProfileSchema(t, db)
	seedCommandIntentProfileRelations(t, db)
	row := validCommandIntentProfileRow()
	if err := insertCommandIntentProfile(db, row); err != nil {
		t.Fatalf("insert valid command intent profile: %v", err)
	}
	assertCommandIntentProfileImmutable(t, db)
}

func TestCommandIntentProfileMigrationRejectsRelationDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB, map[string]any)
	}{
		{"parent", func(_ *testing.T, _ *sql.DB, row map[string]any) {
			row["complete_intent_digest"] = strings.Repeat("f", 64)
		}},
		{"grant", func(_ *testing.T, _ *sql.DB, row map[string]any) {
			row["authorization_grant_revision"] = int64(2)
		}},
		{"stale_source", func(t *testing.T, db *sql.DB, _ map[string]any) {
			insertProfileBinding(t, db, "src", 2, "revoked", "2035-01-03T00:00:00Z")
		}},
		{"stale_target", func(t *testing.T, db *sql.DB, _ map[string]any) {
			insertProfileBinding(t, db, "dst", 2, "revoked", "2035-01-03T00:00:00Z")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openCommandIntentProfileDB(t)
			seedCommandIntentProfileRelations(t, db)
			row := validCommandIntentProfileRow()
			test.mutate(t, db, row)
			if err := insertCommandIntentProfile(db, row); err == nil {
				t.Fatal("relation drift was accepted")
			}
		})
	}
}

func TestCommandIntentProfileMigrationUpgradesV130WithEmptyTable(t *testing.T) {
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := RunMigrations(context.Background(), db, migrationsThrough130Dir(t)); err != nil {
		t.Fatalf("apply migrations through v130: %v", err)
	}
	assertMaxMigrationVersion(t, db, 130)
	assertMigrationTableMissing(t, db, "orchestration_command_intent_profiles")

	dir131 := t.TempDir()
	writeMigrationTestFile(t, dir131, commandIntentProfileMigration, readMigrationTestFile(t, commandIntentProfileMigration))
	if err := RunMigrations(context.Background(), db, dir131); err != nil {
		t.Fatalf("upgrade v130 to v131: %v", err)
	}
	assertMaxMigrationVersion(t, db, 131)
	assertMigrationMarkerCount(t, db, commandIntentProfileMigration, 1)
	assertProfileRowCount(t, db, 0)
	if err := RunMigrations(context.Background(), db, dir131); err != nil {
		t.Fatalf("repeat v131 migration: %v", err)
	}
	assertMigrationMarkerCount(t, db, commandIntentProfileMigration, 1)
}

func openCommandIntentProfileDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := RunMigrations(context.Background(), db, contentMigrationsThrough131Dir(t)); err != nil {
		t.Fatalf("run migrations through v131: %v", err)
	}
	assertMaxMigrationVersion(t, db, 131)
	assertMigrationMarkerCount(t, db, commandIntentProfileMigration, 1)
	return db
}

func assertCommandIntentProfileSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	const table = "orchestration_command_intent_profiles"
	if got, want := tableColumns(t, db, table), commandIntentProfileColumns(); !slicesEqual(got, want) {
		t.Fatalf("%s columns = %v, want %v", table, got, want)
	}
	assertPrimaryKey(t, db, table, []string{"command_id"})
	assertIndex(t, db, table, "idx_orchestration_command_intent_profiles_grant", false, "")
	assertNotNullColumns(t, db, table, commandIntentProfileColumns())
	assertTableSQLContains(t, db, table, []string{"STRICT", "profile_kind = 'send_message_v1'", "authorization_capability = 'send'"})
	assertForeignKeys(t, db, table, []foreignKeyContract{
		{Column: "command_id", Table: "orchestration_command_intents"},
		{Column: "authorization_grant_id", Table: "orchestration_access_grants"},
		{Column: "source_agent_id", Table: "orchestration_current_bindings"},
		{Column: "target_agent_id", Table: "orchestration_current_bindings"},
	})
}

func commandIntentProfileColumns() []string {
	return []string{
		"command_id", "profile_kind", "profile_version", "caller_profile", "caller_auth_context",
		"caller_auth_expires_at", "authorization_kind", "authorization_decision",
		"authorization_request_version", "authorization_request_id", "authorization_request_digest",
		"authorization_grant_id", "authorization_grant_revision", "authorization_capability",
		"authorization_requested_at", "authorization_decided_at", "authorization_expires_at",
		"grant_request_digest", "grant_issued_at", "grant_updated_at", "source_selector_version",
		"source_selector_kind", "source_agent_id", "source_identity_kind", "source_reservation_id",
		"source_reservation_generation", "source_incarnation_id", "source_incarnation_generation",
		"source_external_identity_ref", "source_binding_version", "source_binding_digest", "source_fence_ref",
		"source_active_process_receipt_ref", "source_binding_state", "source_binding_schema_version",
		"source_binding_updated_at", "target_selector_version", "target_selector_kind", "target_agent_id",
		"target_identity_kind", "target_reservation_id", "target_reservation_generation", "target_incarnation_id",
		"target_incarnation_generation", "target_external_identity_ref", "target_binding_version",
		"target_binding_digest", "target_fence_ref", "target_active_process_receipt_ref", "target_binding_state",
		"target_binding_schema_version", "target_binding_updated_at", "complete_intent_digest", "digest_domain",
		"digest_algorithm", "digest_key_id", "digest_key_version", "schema_version", "recorded_at",
	}
}

func seedCommandIntentProfileRelations(t *testing.T, db *sql.DB) {
	t.Helper()
	intent := validCommandIntentFixture()
	intent.CommandID = "profile-command"
	intent.CommandKind = "send_message"
	intent.CallerPrincipalRef = "principal"
	intent.ScopeRef = "scope"
	intent.IdempotencyNamespace = "profile/command"
	intent.IdempotencyKeyDigest = strings.Repeat("1", 64)
	intent.RequestDigest = strings.Repeat("a", 64)
	intent.DigestKeyID = "profile-key"
	intent.SourceFenceRef = "source-fence-src"
	intent.TargetFenceRef = "source-fence-dst"
	insertCommandIntent(t, db, intent)
	seedProfileIdentity(t, db, "src")
	seedProfileIdentity(t, db, "dst")
	insertProfileBinding(t, db, "src", 1, "active", "2035-01-01T00:00:00Z")
	insertProfileBinding(t, db, "dst", 1, "active", "2035-01-01T00:00:00Z")
	insertProfileGrant(t, db)
}

func seedProfileIdentity(t *testing.T, db *sql.DB, agent string) {
	t.Helper()
	digest := strings.Repeat("2", 64)
	if err := identityExec(t, db, identityReservationInsert,
		"profile-reservation-"+agent, nil, agent, "native_subagent", "provider_discovery",
		"principal", "scope", "", "profile/reservation/"+agent, digest, digest,
		"source-fence-"+agent, "target-fence-"+agent, 0, 1, "activated", 1); err != nil {
		t.Fatalf("insert profile reservation %s: %v", agent, err)
	}
	if err := identityExec(t, db, identityIncarnationInsert,
		"profile-incarnation-"+agent, "profile-reservation-"+agent, agent, "native_subagent",
		"external-"+agent, "epoch-"+agent, "source-fence-"+agent, digest, 1, 1); err != nil {
		t.Fatalf("insert profile incarnation %s: %v", agent, err)
	}
}

func insertProfileBinding(t *testing.T, db *sql.DB, agent string, version int64, state, updatedAt string) {
	t.Helper()
	digest := profileBindingDigest(agent)
	_, err := db.Exec(`INSERT INTO orchestration_current_bindings (
		agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
		reservation_generation, incarnation_generation, external_identity_ref,
		source_fence_ref, active_process_receipt_ref, binding_digest, state,
		schema_version, updated_at
	) VALUES (?, ?, ?, ?, 'native_subagent', 1, 1, ?, ?, ?, ?, ?, 1, ?)`,
		agent, version, "profile-reservation-"+agent, "profile-incarnation-"+agent,
		"external-"+agent, "source-fence-"+agent, "active-process-"+agent, digest, state, updatedAt)
	if err != nil {
		t.Fatalf("insert profile binding %s v%d: %v", agent, version, err)
	}
}

func insertProfileGrant(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO orchestration_access_grants (
		grant_id, caller_principal_ref, scope_ref, source_agent_id, target_agent_id,
		source_reservation_id, source_reservation_generation, source_incarnation_id,
		source_incarnation_generation, source_binding_version, source_binding_digest, source_fence_ref,
		target_reservation_id, target_reservation_generation, target_incarnation_id,
		target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref,
		can_read, can_send, idempotency_namespace, idempotency_key_digest, request_digest,
		revision, status, issued_at, expires_at, updated_at, schema_version
	) VALUES (
		'profile-grant', 'principal', 'scope', 'src', 'dst',
		'profile-reservation-src', 1, 'profile-incarnation-src', 1, 1, ?, 'source-fence-src',
		'profile-reservation-dst', 1, 'profile-incarnation-dst', 1, 1, ?, 'source-fence-dst',
		1, 1, 'profile/grant', ?, ?, 1, 'active', ?, ?, ?, 1
	)`, profileBindingDigest("src"), profileBindingDigest("dst"), strings.Repeat("3", 64),
		strings.Repeat("d", 64), "2035-01-01T00:00:00Z", "2036-01-01T00:00:00Z", "2035-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert profile access grant: %v", err)
	}
}

func validCommandIntentProfileRow() map[string]any {
	row := map[string]any{
		"command_id": "profile-command", "profile_kind": "send_message_v1", "profile_version": int64(1),
		"caller_profile": "standalone_http_local_v1", "caller_auth_context": "orch-auth:test",
		"caller_auth_expires_at": "2036-01-01T00:00:00Z", "authorization_kind": "access_grant_allow_v1",
		"authorization_decision": "allowed", "authorization_request_version": int64(1),
		"authorization_request_id": "profile-request", "authorization_request_digest": strings.Repeat("e", 64),
		"authorization_grant_id": "profile-grant", "authorization_grant_revision": int64(1),
		"authorization_capability": "send", "authorization_requested_at": "2035-01-02T00:00:00Z",
		"authorization_decided_at": "2035-01-02T00:00:01Z", "authorization_expires_at": "2036-01-01T00:00:00Z",
		"grant_request_digest": strings.Repeat("d", 64), "grant_issued_at": "2035-01-01T00:00:00Z",
		"grant_updated_at": "2035-01-01T00:00:00Z", "complete_intent_digest": strings.Repeat("a", 64),
		"digest_domain": "orch.command.send-message-profile/v1", "digest_algorithm": "hmac-sha256",
		"digest_key_id": "profile-key", "digest_key_version": int64(1), "schema_version": int64(1),
	}
	addProfileBindingFields(row, "source", "src")
	addProfileBindingFields(row, "target", "dst")
	return row
}

func addProfileBindingFields(row map[string]any, prefix, agent string) {
	row[prefix+"_selector_version"] = int64(1)
	row[prefix+"_selector_kind"] = "active_binding"
	row[prefix+"_agent_id"] = agent
	row[prefix+"_identity_kind"] = "native_subagent"
	row[prefix+"_reservation_id"] = "profile-reservation-" + agent
	row[prefix+"_reservation_generation"] = int64(1)
	row[prefix+"_incarnation_id"] = "profile-incarnation-" + agent
	row[prefix+"_incarnation_generation"] = int64(1)
	row[prefix+"_external_identity_ref"] = "external-" + agent
	row[prefix+"_binding_version"] = int64(1)
	row[prefix+"_binding_digest"] = profileBindingDigest(agent)
	row[prefix+"_fence_ref"] = "source-fence-" + agent
	row[prefix+"_active_process_receipt_ref"] = "active-process-" + agent
	row[prefix+"_binding_state"] = "active"
	row[prefix+"_binding_schema_version"] = int64(1)
	row[prefix+"_binding_updated_at"] = "2035-01-01T00:00:00Z"
}

func insertCommandIntentProfile(db *sql.DB, row map[string]any) error {
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
	query := "INSERT INTO orchestration_command_intent_profiles (" + strings.Join(columns, ",") +
		") VALUES (" + strings.Join(marks, ",") + ")"
	_, err := db.Exec(query, values...)
	return err
}

func assertCommandIntentProfileImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		"UPDATE orchestration_command_intent_profiles SET caller_profile = 'changed' WHERE command_id = 'profile-command'",
		"DELETE FROM orchestration_command_intent_profiles WHERE command_id = 'profile-command'",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("immutable command intent profile mutation succeeded: %s", statement)
		}
	}
}

func migrationsThrough130Dir(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(sqliteMigrationsDir(t))
	if err != nil {
		t.Fatalf("read migration directory: %v", err)
	}
	dir := t.TempDir()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || name >= commandIntentProfileMigration {
			continue
		}
		writeMigrationTestFile(t, dir, name, readMigrationTestFile(t, name))
	}
	return dir
}

func assertProfileRowCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM orchestration_command_intent_profiles").Scan(&got); err != nil {
		t.Fatalf("count command intent profiles: %v", err)
	}
	if got != want {
		t.Fatalf("command intent profile rows = %d, want %d", got, want)
	}
}

func profileBindingDigest(agent string) string {
	if agent == "src" {
		return strings.Repeat("b", 64)
	}
	return strings.Repeat("c", 64)
}
