package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

const eventStoreMigration = "129_orchestration_event_store.sql"

// TestEventStoreMigrationContract 验证 canonical dense event owner 的五表结构与边界。
func TestEventStoreMigrationContract(t *testing.T) {
	db := openEventStoreMigrationDB(t)
	assertEventStoreSchema(t, db)
	seedEventStoreIdentity(t, db)
	t.Run("source_state_watermarks", func(t *testing.T) { assertEventSourceStateContract(t, db) })
	t.Run("ingress_identity_and_replay", func(t *testing.T) { assertEventIngressContract(t, db) })
	t.Run("canonical_event_projection", func(t *testing.T) { assertCanonicalEventContract(t, db) })
	t.Run("gap_tombstone_cursor", func(t *testing.T) { assertEventGapTombstoneCursorContract(t, db) })
	t.Run("immutable_rows", func(t *testing.T) { assertEventImmutableRows(t, db) })
}

func openEventStoreMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, eventStoreMigration, 1)
	return db
}

func assertEventStoreSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	contracts := []eventTableContract{
		{
			name:       "orchestration_event_source_states",
			columns:    []string{"source_id", "source_epoch", "lane", "next_sequence", "materialized_sequence", "dedup_floor", "revision", "status", "gap_reason", "writer_epoch", "created_at", "updated_at"},
			primaryKey: []string{"source_id", "source_epoch", "lane"},
			notNull:    []string{"source_id", "source_epoch", "lane", "next_sequence", "materialized_sequence", "dedup_floor", "revision", "status", "gap_reason", "writer_epoch", "created_at", "updated_at"},
			checks:     []string{"STRICT", "next_sequence >= 1", "materialized_sequence >= 0", "dedup_floor >= 0", "revision > 0", "materialized_sequence < next_sequence", "dedup_floor <= materialized_sequence", "status IN ('active', 'gap', 'quarantined', 'closed')"},
		},
		{
			name:        "orchestration_event_ingress_receipts",
			columns:     []string{"source_id", "source_epoch", "lane", "source_sequence", "event_id", "envelope_hash", "status", "event_type", "schema_version", "root_agent_id", "subject_agent_id", "reservation_id", "incarnation_id", "external_epoch", "incarnation_generation", "identity_digest", "binding_version", "binding_digest", "source_fence_ref", "provenance", "trust_class", "content_role", "content_ref", "content_size", "media_type", "provider_at", "observed_at", "writer_epoch", "canonical_cursor", "error_ref"},
			primaryKey:  []string{"source_id", "source_epoch", "lane", "source_sequence"},
			notNull:     []string{"source_id", "source_epoch", "lane", "source_sequence", "event_id", "envelope_hash", "status", "event_type", "schema_version", "root_agent_id", "subject_agent_id", "reservation_id", "external_epoch", "incarnation_generation", "identity_digest", "binding_version", "binding_digest", "source_fence_ref", "provenance", "trust_class", "content_role", "content_ref", "content_size", "media_type", "observed_at", "writer_epoch", "canonical_cursor", "error_ref"},
			checks:      []string{"STRICT", "source_sequence >= 1", "status IN ('staged', 'materialized', 'quarantined', 'gap')", "schema_version = 1", "incarnation_id IS NULL", "content_role = 'none'"},
			foreignKeys: []foreignKeyContract{{Column: "reservation_id", Table: "orchestration_identity_reservations"}, {Column: "root_agent_id", Table: "orchestration_identity_reservations"}, {Column: "incarnation_id", Table: "orchestration_execution_incarnations"}},
			indexes:     []eventIndexContract{{name: "idx_orchestration_event_receipts_status"}},
		},
		{
			name:        "orchestration_agent_events",
			columns:     []string{"event_id", "source_id", "source_epoch", "lane", "source_sequence", "canonical_cursor", "envelope_hash", "status", "event_type", "schema_version", "root_agent_id", "subject_agent_id", "reservation_id", "incarnation_id", "external_epoch", "incarnation_generation", "identity_digest", "binding_version", "binding_digest", "source_fence_ref", "provenance", "trust_class", "content_role", "content_ref", "content_size", "media_type", "provider_at", "observed_at", "writer_epoch"},
			primaryKey:  []string{"event_id"},
			notNull:     []string{"event_id", "source_id", "source_epoch", "lane", "source_sequence", "canonical_cursor", "envelope_hash", "status", "event_type", "schema_version", "root_agent_id", "subject_agent_id", "reservation_id", "external_epoch", "incarnation_generation", "identity_digest", "binding_version", "binding_digest", "source_fence_ref", "provenance", "trust_class", "content_role", "content_ref", "content_size", "media_type", "observed_at", "writer_epoch"},
			checks:      []string{"STRICT", "status = 'materialized'", "source_sequence >= 1", "schema_version = 1", "content_role = 'none'"},
			foreignKeys: []foreignKeyContract{{Column: "source_id", Table: "orchestration_event_ingress_receipts"}, {Column: "source_epoch", Table: "orchestration_event_ingress_receipts"}, {Column: "lane", Table: "orchestration_event_ingress_receipts"}, {Column: "source_sequence", Table: "orchestration_event_ingress_receipts"}, {Column: "reservation_id", Table: "orchestration_identity_reservations"}, {Column: "root_agent_id", Table: "orchestration_identity_reservations"}, {Column: "incarnation_id", Table: "orchestration_execution_incarnations"}},
			indexes:     []eventIndexContract{{name: "idx_orchestration_agent_events_subject"}},
		},
		{
			name:       "orchestration_event_tombstones",
			columns:    []string{"source_id", "source_epoch", "lane", "source_sequence", "envelope_hash", "event_id", "canonical_cursor", "dedup_floor", "status", "created_at"},
			primaryKey: []string{"source_id", "source_epoch", "lane", "source_sequence"},
			notNull:    []string{"source_id", "source_epoch", "lane", "source_sequence", "envelope_hash", "event_id", "canonical_cursor", "dedup_floor", "status", "created_at"},
			checks:     []string{"STRICT", "source_sequence >= 1", "dedup_floor >= 0", "status IN ('materialized', 'expired', 'quarantined')"},
			indexes:    []eventIndexContract{{name: "idx_orchestration_event_tombstones_floor"}},
		},
		{
			name:        "orchestration_event_materialization_cursors",
			columns:     []string{"projection_id", "source_id", "source_epoch", "lane", "materialized_sequence", "materialized_hash", "revision", "status", "updated_at"},
			primaryKey:  []string{"projection_id", "source_id", "source_epoch", "lane"},
			notNull:     []string{"projection_id", "source_id", "source_epoch", "lane", "materialized_sequence", "materialized_hash", "revision", "status", "updated_at"},
			checks:      []string{"STRICT", "materialized_sequence >= 0", "revision > 0", "status IN ('active', 'gap', 'quarantined', 'closed')", "materialized_sequence = 0"},
			foreignKeys: []foreignKeyContract{{Column: "source_id", Table: "orchestration_event_source_states"}, {Column: "source_epoch", Table: "orchestration_event_source_states"}, {Column: "lane", Table: "orchestration_event_source_states"}},
		},
	}
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			assertPrimaryKey(t, db, contract.name, contract.primaryKey)
			if columns := tableColumns(t, db, contract.name); !slicesEqual(columns, contract.columns) {
				t.Fatalf("%s columns = %v, want %v", contract.name, columns, contract.columns)
			}
			assertNotNullColumns(t, db, contract.name, contract.notNull)
			assertTableSQLContains(t, db, contract.name, contract.checks)
			assertForeignKeys(t, db, contract.name, contract.foreignKeys)
			for _, index := range contract.indexes {
				assertIndex(t, db, contract.name, index.name, index.unique, index.where)
			}
		})
	}
	assertEventStateColumnsHaveNoDefaults(t, db)
}

type eventTableContract struct {
	name                                 string
	columns, primaryKey, notNull, checks []string
	foreignKeys                          []foreignKeyContract
	indexes                              []eventIndexContract
}

type eventIndexContract struct {
	name, where string
	unique      bool
}

func assertEventStateColumnsHaveNoDefaults(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, column := range []string{"source_id", "source_epoch", "lane", "next_sequence", "materialized_sequence", "dedup_floor", "revision", "status"} {
		if eventColumnDefault(t, db, "orchestration_event_source_states", column) != nil {
			t.Fatalf("source state %s has implicit default", column)
		}
	}
}

func eventColumnDefault(t *testing.T, db *sql.DB, table, column string) any {
	t.Helper()
	var value any
	if err := db.QueryRow("SELECT dflt_value FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&value); err != nil {
		t.Fatalf("read default for %s.%s: %v", table, column, err)
	}
	return value
}

func seedEventStoreIdentity(t *testing.T, db *sql.DB) {
	t.Helper()
	intent := validCommandIntentFixture()
	intent.CommandID = "event-command"
	intent.IdempotencyNamespace = "event/identity"
	intent.IdempotencyKeyDigest = strings.Repeat("c", 64)
	intent.RequestDigest = strings.Repeat("d", 64)
	insertCommandIntent(t, db, intent)
	if _, err := db.Exec(`INSERT INTO orchestration_identity_reservations (
		reservation_id, command_id, agent_id, identity_kind, source_kind,
		caller_principal_ref, scope_ref, parent_agent_id, idempotency_namespace,
		idempotency_key_digest, request_digest, source_fence_ref, target_fence_ref,
		expected_binding_version, reservation_generation, state, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"event-reservation", intent.CommandID, "event-agent", "provider_session", "first_launch",
		"event-principal", "event-scope", "", "event/reservation", strings.Repeat("e", 64), strings.Repeat("f", 64),
		"event-source-fence", "event-target-fence", 0, 1, "reserved", 1); err != nil {
		t.Fatalf("insert event reservation: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO orchestration_execution_incarnations (
		incarnation_id, reservation_id, agent_id, identity_kind, external_identity_ref,
		external_epoch, source_fence_ref, identity_digest, incarnation_generation, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"event-incarnation", "event-reservation", "event-agent", "provider_session", "event-external",
		"event-epoch", "event-source-fence", strings.Repeat("a", 64), 1, 1); err != nil {
		t.Fatalf("insert event incarnation: %v", err)
	}
}

func assertEventSourceStateContract(t *testing.T, db *sql.DB) {
	t.Helper()
	insertEventSourceState(t, db, "state-source", "epoch-1", "control", 1, 0, 0, 1, "active", "")
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_source_states (source_id,source_epoch,lane,next_sequence,materialized_sequence,dedup_floor,revision,status,gap_reason,writer_epoch) VALUES (?,?,?,?,?,?,?,?,?,?)`, "bad", "epoch", "control", 1, 1, 0, 1, "active", "", "writer")
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_source_states (source_id,source_epoch,lane,next_sequence,materialized_sequence,dedup_floor,revision,status,gap_reason,writer_epoch) VALUES (?,?,?,?,?,?,?,?,?,?)`, "bad", "epoch", "control", 1, 0, 1, 1, "active", "", "writer")
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_source_states (source_id,source_epoch,lane,next_sequence,materialized_sequence,dedup_floor,revision,status,gap_reason,writer_epoch) VALUES (?,?,?,?,?,?,?,?,?,?)`, "bad", "epoch", "control", 1, 0, 0, 1, "gap", "", "writer")
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_source_states (source_id,source_epoch,lane,next_sequence,materialized_sequence,dedup_floor,revision,status,gap_reason,writer_epoch) VALUES (?,?,?,?,?,?,?,?,?,?)`, "bad", "epoch", "control", 1, 0, 0, 1, "unknown", "", "writer")
	assertEventExecFails(t, db, `UPDATE orchestration_event_source_states SET revision = 3 WHERE source_id = 'state-source'`)
	assertEventExecFails(t, db, `UPDATE orchestration_event_source_states SET materialized_sequence = -1, revision = 2 WHERE source_id = 'state-source'`)
	if _, err := db.Exec(`UPDATE orchestration_event_source_states SET next_sequence = 2, revision = 2 WHERE source_id = 'state-source'`); err != nil {
		t.Fatalf("advance source state: %v", err)
	}
	assertEventExecFails(t, db, `DELETE FROM orchestration_event_source_states WHERE source_id = 'state-source'`)
}

func insertEventSourceState(t *testing.T, db *sql.DB, sourceID, epoch, lane string, next, materialized, floor, revision int64, status, reason string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO orchestration_event_source_states (source_id,source_epoch,lane,next_sequence,materialized_sequence,dedup_floor,revision,status,gap_reason,writer_epoch) VALUES (?,?,?,?,?,?,?,?,?,?)`, sourceID, epoch, lane, next, materialized, floor, revision, status, reason, "writer-1"); err != nil {
		t.Fatalf("insert event source state: %v", err)
	}
}

func assertEventIngressContract(t *testing.T, db *sql.DB) {
	t.Helper()
	pre := validEventIngress("ingress-source", 1, "ingress-pre", nil)
	insertEventIngress(t, db, pre)
	bound := validEventIngress("ingress-source", 2, "ingress-bound", "event-incarnation")
	insertEventIngress(t, db, bound)
	assertEventIngressReplay(t, db, pre)
	assertEventIngressFieldCombinations(t, db, pre, bound)
}

func assertEventIngressReplay(t *testing.T, db *sql.DB, fixture eventIngressFixture) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO orchestration_event_ingress_receipts (
		source_id,source_epoch,lane,source_sequence,event_id,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch,canonical_cursor,error_ref
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id,source_epoch,lane,source_sequence) DO UPDATE SET envelope_hash=excluded.envelope_hash`, eventIngressArgs(fixture)...); err != nil {
		t.Fatalf("same source sequence/hash replay: %v", err)
	}
	changed := fixture
	changed.EnvelopeHash = strings.Repeat("b", 64)
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_ingress_receipts (
		source_id,source_epoch,lane,source_sequence,event_id,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch,canonical_cursor,error_ref
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id,source_epoch,lane,source_sequence) DO UPDATE SET envelope_hash=excluded.envelope_hash`, eventIngressArgs(changed)...)
	if _, err := db.Exec(`INSERT INTO orchestration_event_ingress_receipts SELECT source_id,source_epoch,lane,source_sequence,'ingress-duplicate-id',envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch,canonical_cursor,error_ref FROM orchestration_event_ingress_receipts WHERE event_id = 'ingress-pre'`); err == nil {
		t.Fatal("duplicate ingress event_id accepted")
	}
	otherSource := fixture
	otherSource.SourceID, otherSource.SourceSequence = "ingress-other-source", 1
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_ingress_receipts (
		source_id,source_epoch,lane,source_sequence,event_id,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch,canonical_cursor,error_ref
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, eventIngressArgs(otherSource)...)
}

func assertEventIngressFieldCombinations(t *testing.T, db *sql.DB, pre, bound eventIngressFixture) {
	t.Helper()
	cases := []struct {
		name string
		row  eventIngressFixture
	}{
		{name: "pre_with_generation", row: mutateIngress(pre, func(v *eventIngressFixture) {
			v.SourceSequence = 3
			v.EventID = "bad-pre-generation"
			v.IncarnationGeneration = 1
		})},
		{name: "pre_with_external_epoch", row: mutateIngress(pre, func(v *eventIngressFixture) {
			v.SourceSequence = 4
			v.EventID = "bad-pre-epoch"
			v.ExternalEpoch = "unexpected"
		})},
		{name: "bound_without_binding_digest", row: mutateIngress(bound, func(v *eventIngressFixture) {
			v.SourceSequence = 3
			v.EventID = "bad-bound-digest"
			v.BindingDigest = ""
		})},
		{name: "invalid_identity_digest", row: mutateIngress(bound, func(v *eventIngressFixture) {
			v.SourceSequence = 4
			v.EventID = "bad-identity-digest"
			v.IdentityDigest = "not-hex"
		})},
		{name: "content_ref_without_role", row: mutateIngress(pre, func(v *eventIngressFixture) {
			v.SourceSequence = 5
			v.EventID = "bad-content"
			v.ContentRef = "content-ref"
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertEventIngressRejected(t, db, tc.row) })
	}
}

func assertEventIngressRejected(t *testing.T, db *sql.DB, fixture eventIngressFixture) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO orchestration_event_ingress_receipts (
		source_id,source_epoch,lane,source_sequence,event_id,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch,canonical_cursor,error_ref
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, eventIngressArgs(fixture)...)
	if err == nil {
		t.Fatalf("invalid ingress fixture %q accepted", fixture.EventID)
	}
}

type eventIngressFixture struct {
	SourceID, SourceEpoch, Lane string
	SourceSequence              int64
	EventID, EnvelopeHash       string
	Status, EventType           string
	SchemaVersion               int64
	RootAgentID, SubjectAgentID string
	ReservationID               string
	IncarnationID               any
	ExternalEpoch               string
	IncarnationGeneration       int64
	IdentityDigest              string
	BindingVersion              int64
	BindingDigest               string
	SourceFenceRef              string
	Provenance, TrustClass      string
	ContentRole, ContentRef     string
	ContentSize                 int64
	MediaType, ProviderAt       any
	ObservedAt, WriterEpoch     string
	CanonicalCursor, ErrorRef   string
}

func validEventIngress(source string, sequence int64, eventID string, incarnation any) eventIngressFixture {
	row := eventIngressFixture{
		SourceID: source, SourceEpoch: "epoch-1", Lane: "control", SourceSequence: sequence,
		EventID: eventID, EnvelopeHash: strings.Repeat("a", 64), Status: "staged", EventType: "turn.started", SchemaVersion: 1,
		RootAgentID: "event-agent", SubjectAgentID: "event-agent", ReservationID: "event-reservation", IncarnationID: incarnation,
		SourceFenceRef: "event-source-fence", Provenance: "provider", TrustClass: "trusted", ContentRole: "none",
		ContentRef: "", ContentSize: 0, MediaType: "", ProviderAt: nil, ObservedAt: "2026-09-01T00:00:00Z", WriterEpoch: "writer-1",
		CanonicalCursor: "cursor-" + eventID, ErrorRef: "",
	}
	if incarnation != nil {
		row.ExternalEpoch, row.IncarnationGeneration = "event-epoch", 1
		row.IdentityDigest, row.BindingVersion, row.BindingDigest = strings.Repeat("b", 64), 1, strings.Repeat("c", 64)
	}
	return row
}

func mutateIngress(row eventIngressFixture, mutate func(*eventIngressFixture)) eventIngressFixture {
	mutated := row
	mutate(&mutated)
	return mutated
}

func eventIngressArgs(row eventIngressFixture) []any {
	return []any{row.SourceID, row.SourceEpoch, row.Lane, row.SourceSequence, row.EventID, row.EnvelopeHash, row.Status, row.EventType, row.SchemaVersion, row.RootAgentID, row.SubjectAgentID, row.ReservationID, row.IncarnationID, row.ExternalEpoch, row.IncarnationGeneration, row.IdentityDigest, row.BindingVersion, row.BindingDigest, row.SourceFenceRef, row.Provenance, row.TrustClass, row.ContentRole, row.ContentRef, row.ContentSize, row.MediaType, row.ProviderAt, row.ObservedAt, row.WriterEpoch, row.CanonicalCursor, row.ErrorRef}
}

func insertEventIngress(t *testing.T, db *sql.DB, row eventIngressFixture) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO orchestration_event_ingress_receipts (
		source_id,source_epoch,lane,source_sequence,event_id,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch,canonical_cursor,error_ref
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, eventIngressArgs(row)...); err != nil {
		t.Fatalf("insert ingress %s: %v", row.EventID, err)
	}
}

func assertCanonicalEventContract(t *testing.T, db *sql.DB) {
	t.Helper()
	row := validEventIngress("canonical-source", 1, "canonical-pre", nil)
	insertEventIngress(t, db, row)
	insertCanonicalEvent(t, db, row)
	bound := validEventIngress("canonical-source", 2, "canonical-bound", "event-incarnation")
	insertEventIngress(t, db, bound)
	insertCanonicalEvent(t, db, bound)
	assertEventExecFails(t, db, `UPDATE orchestration_agent_events SET event_type = 'tampered' WHERE event_id = 'canonical-pre'`)
	assertEventExecFails(t, db, `DELETE FROM orchestration_agent_events WHERE event_id = 'canonical-pre'`)
	assertEventExecFails(t, db, `INSERT INTO orchestration_agent_events SELECT 'canonical-duplicate',source_id,source_epoch,lane,source_sequence,canonical_cursor,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch FROM orchestration_agent_events WHERE event_id = 'canonical-pre'`)
	assertEventExecFails(t, db, `INSERT INTO orchestration_agent_events SELECT 'canonical-new-event',source_id,source_epoch,lane,source_sequence,canonical_cursor,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch FROM orchestration_agent_events WHERE event_id = 'canonical-pre'`)
}

func insertCanonicalEvent(t *testing.T, db *sql.DB, row eventIngressFixture) {
	t.Helper()
	args := []any{row.EventID, row.SourceID, row.SourceEpoch, row.Lane, row.SourceSequence, row.CanonicalCursor, row.EnvelopeHash, "materialized", row.EventType, row.SchemaVersion, row.RootAgentID, row.SubjectAgentID, row.ReservationID, row.IncarnationID, row.ExternalEpoch, row.IncarnationGeneration, row.IdentityDigest, row.BindingVersion, row.BindingDigest, row.SourceFenceRef, row.Provenance, row.TrustClass, row.ContentRole, row.ContentRef, row.ContentSize, row.MediaType, row.ProviderAt, row.ObservedAt, row.WriterEpoch}
	if _, err := db.Exec(`INSERT INTO orchestration_agent_events (
		event_id,source_id,source_epoch,lane,source_sequence,canonical_cursor,envelope_hash,status,event_type,schema_version,root_agent_id,subject_agent_id,reservation_id,incarnation_id,external_epoch,incarnation_generation,identity_digest,binding_version,binding_digest,source_fence_ref,provenance,trust_class,content_role,content_ref,content_size,media_type,provider_at,observed_at,writer_epoch
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
		t.Fatalf("insert canonical event %s: %v", row.EventID, err)
	}
}

func assertEventGapTombstoneCursorContract(t *testing.T, db *sql.DB) {
	t.Helper()
	insertEventSourceState(t, db, "gap-source", "epoch-1", "control", 1, 0, 0, 1, "active", "")
	gap := validEventIngress("gap-source", 3, "gap-event", nil)
	gap.Status = "gap"
	insertEventIngress(t, db, gap)
	if _, err := db.Exec(`INSERT INTO orchestration_event_tombstones (source_id,source_epoch,lane,source_sequence,envelope_hash,event_id,canonical_cursor,dedup_floor,status) VALUES (?,?,?,?,?,?,?,?,?)`, "gap-source", "epoch-1", "control", 2, strings.Repeat("d", 64), "gap-tombstone", "cursor-gap-2", 1, "quarantined"); err != nil {
		t.Fatalf("insert event tombstone: %v", err)
	}
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_tombstones (source_id,source_epoch,lane,source_sequence,envelope_hash,event_id,canonical_cursor,dedup_floor,status) VALUES (?,?,?,?,?,?,?,?,?)`, "gap-source", "epoch-1", "control", 2, strings.Repeat("d", 64), "duplicate", "cursor-gap-2", 1, "quarantined")
	assertEventExecFails(t, db, `INSERT INTO orchestration_event_tombstones (source_id,source_epoch,lane,source_sequence,envelope_hash,event_id,canonical_cursor,dedup_floor,status) VALUES (?,?,?,?,?,?,?,?,?)`, "gap-source", "epoch-1", "control", 4, strings.Repeat("d", 64), "bad-floor", "cursor-gap-4", -1, "quarantined")
	if _, err := db.Exec(`INSERT INTO orchestration_event_materialization_cursors (projection_id,source_id,source_epoch,lane,materialized_sequence,materialized_hash,revision,status) VALUES (?,?,?,?,?,?,?,?)`, "projection-1", "gap-source", "epoch-1", "control", 0, "", 1, "active"); err != nil {
		t.Fatalf("insert initial cursor: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_event_materialization_cursors SET materialized_sequence = 1, materialized_hash = ?, revision = 2 WHERE projection_id = 'projection-1'`, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("advance materialization cursor: %v", err)
	}
	assertEventExecFails(t, db, `UPDATE orchestration_event_materialization_cursors SET revision = 4 WHERE projection_id = 'projection-1'`)
	assertEventExecFails(t, db, `UPDATE orchestration_event_materialization_cursors SET materialized_sequence = 0, revision = 3 WHERE projection_id = 'projection-1'`)
	assertEventExecFails(t, db, `UPDATE orchestration_event_materialization_cursors SET materialized_hash = '', revision = 3 WHERE projection_id = 'projection-1'`)
	assertEventExecFails(t, db, `UPDATE orchestration_event_materialization_cursors SET status = 'unknown', revision = 3 WHERE projection_id = 'projection-1'`)
	assertEventExecFails(t, db, `DELETE FROM orchestration_event_materialization_cursors WHERE projection_id = 'projection-1'`)
}

func assertEventImmutableRows(t *testing.T, db *sql.DB) {
	t.Helper()
	row := validEventIngress("immutable-source", 1, "immutable-ingress", nil)
	insertEventIngress(t, db, row)
	assertEventExecFails(t, db, `UPDATE orchestration_event_ingress_receipts SET event_id = 'changed' WHERE event_id = 'immutable-ingress'`)
	assertEventExecFails(t, db, `DELETE FROM orchestration_event_ingress_receipts WHERE event_id = 'immutable-ingress'`)
	insertCanonicalEvent(t, db, rowWithEventID(row, "immutable-canonical"))
	assertEventExecFails(t, db, `UPDATE orchestration_agent_events SET event_type = 'changed' WHERE event_id = 'immutable-canonical'`)
	assertEventExecFails(t, db, `DELETE FROM orchestration_agent_events WHERE event_id = 'immutable-canonical'`)
	if _, err := db.Exec(`INSERT INTO orchestration_event_tombstones (source_id,source_epoch,lane,source_sequence,envelope_hash,event_id,canonical_cursor,dedup_floor,status) VALUES (?,?,?,?,?,?,?,?,?)`, "immutable-source", "epoch-1", "control", 2, strings.Repeat("e", 64), "immutable-tombstone", "cursor-immutable", 1, "expired"); err != nil {
		t.Fatalf("insert immutable tombstone: %v", err)
	}
	assertEventExecFails(t, db, `UPDATE orchestration_event_tombstones SET event_id = 'changed' WHERE event_id = 'immutable-tombstone'`)
	assertEventExecFails(t, db, `DELETE FROM orchestration_event_tombstones WHERE event_id = 'immutable-tombstone'`)
}

func rowWithEventID(row eventIngressFixture, eventID string) eventIngressFixture {
	row.EventID = eventID
	row.CanonicalCursor = "cursor-" + eventID
	return row
}

func assertEventExecFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("expected SQL rejection, query=%s", query)
	}
}
