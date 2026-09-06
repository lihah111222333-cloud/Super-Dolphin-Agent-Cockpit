package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const commandJournalMigration = "127_orchestration_command_journal.sql"

func TestCommandJournalMigrationContract(t *testing.T) {
	t.Run("journal_schema", TestCommandJournalMigrationReaches133AndConstrainsSchema)
	t.Run("journal_invalid_states", TestCommandJournalMigrationRejectsInvalidStateCombinations)
	t.Run("valid_version_two", TestCommandJournalMigrationAcceptsValidVersionTwoControl)
	t.Run("attempt_reuse", TestCommandJournalMigrationAllowsMultipleObservationsPerAttempt)
	t.Run("repeated_new_attempt", TestCommandJournalMigrationRejectsRepeatedNewAttemptTriggers)
	t.Run("operation_unique", TestCommandJournalMigrationRejectsDuplicateOperationWithinCommand)
	t.Run("cross_command_attempt", TestCommandJournalMigrationAllowsSameAttemptAcrossCommands)
	t.Run("explicit_version", TestCommandJournalMigrationRequiresExplicitVersion)
	t.Run("command_intent_schema", TestCommandIntentMigrationReaches133AndConstrainsImmutableSchema)
	t.Run("upgrade_matrix", TestCombinedMigrationUpgradeMatrixReaches133)
	t.Run("embedded_fresh_idempotent", TestRunEmbeddedMigrationsFreshDatabaseIsIdempotent)
	t.Run("embedded_legacy_markers", TestRunEmbeddedMigrationsSupportsLegacy120Markers)
	t.Run("embedded_matches_disk", TestEmbeddedMigrationsExactlyMatchDiskSetAndContents)
}

func TestCommandJournalMigrationReaches133AndConstrainsSchema(t *testing.T) {
	db := openCommandJournalMigrationDB(t)
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, commandJournalMigration, 1)
	assertCommandJournalSchema(t, db)
	insertCommandIntent(t, db, validCommandIntentFixture())
	insertCommandJournalGenesis(t, db, "command-1", "operation-genesis")
	assertCommandJournalGenesis(t, db)
	assertCommandJournalImmutableAndContiguous(t, db)
}

func openCommandJournalMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func assertCommandJournalSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	assertTableSQLContains(t, db, "orchestration_command_journal", []string{"STRICT", "PRIMARY KEY", "UNIQUE", "prepare_accept"})
	want := []string{
		"command_id", "command_version", "version", "operation_id", "operation_digest", "phase",
		"accept_resolution", "attempt_id", "trigger", "evidence_ref", "uncertain_phase",
		"schema_version", "recorded_at",
	}
	if columns := tableColumns(t, db, "orchestration_command_journal"); !slicesEqual(columns, want) {
		t.Fatalf("command journal columns = %v, want %v", columns, want)
	}
	assertNotNullColumns(t, db, "orchestration_command_journal", want)
	assertPrimaryKey(t, db, "orchestration_command_journal", []string{"command_id", "version"})
	assertForeignKeys(t, db, "orchestration_command_journal", []foreignKeyContract{{Column: "command_id", Table: "orchestration_command_intents"}})
	assertIndex(t, db, "orchestration_command_journal", "idx_orchestration_command_journal_attempt", false, "")
	assertIndex(t, db, "orchestration_command_journal", "idx_orchestration_command_journal_new_attempt", true, "trigger IN ('begin_dispatch', 'reclaim_unsent')")
}

func insertCommandJournalGenesis(t *testing.T, db *sql.DB, commandID, operationID string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO orchestration_command_journal (
			command_id, command_version, version, operation_id, operation_digest, phase,
			accept_resolution, attempt_id, trigger, evidence_ref, uncertain_phase, schema_version
		) VALUES (?, 1, 1, ?, ?, 'recovery_custody_pending', 'pending', '', 'prepare_accept', '', '', 1)`,
		commandID, operationID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("insert command journal genesis: %v", err)
	}
}

type commandJournalGenesisSnapshot struct {
	phase, resolution, attempt, trigger, evidence, uncertain, recordedAt string
	version, commandVersion, schemaVersion                               int64
}

func assertCommandJournalGenesis(t *testing.T, db *sql.DB) {
	t.Helper()
	snapshot := readCommandJournalGenesis(t, db)
	assertCommandJournalGenesisFields(t, snapshot)
	assertCommandJournalGenesisRecordedAt(t, snapshot.recordedAt)
}

func readCommandJournalGenesis(t *testing.T, db *sql.DB) commandJournalGenesisSnapshot {
	t.Helper()
	var snapshot commandJournalGenesisSnapshot
	err := db.QueryRow(`SELECT command_version, version, phase, accept_resolution, attempt_id,
		trigger, evidence_ref, uncertain_phase, schema_version, recorded_at
		FROM orchestration_command_journal WHERE command_id = 'command-1'`).Scan(
		&snapshot.commandVersion, &snapshot.version, &snapshot.phase, &snapshot.resolution, &snapshot.attempt,
		&snapshot.trigger, &snapshot.evidence, &snapshot.uncertain, &snapshot.schemaVersion, &snapshot.recordedAt)
	if err != nil {
		t.Fatalf("read command journal genesis: %v", err)
	}
	return snapshot
}

func assertCommandJournalGenesisFields(t *testing.T, snapshot commandJournalGenesisSnapshot) {
	t.Helper()
	got := snapshot
	got.recordedAt = ""
	want := commandJournalGenesisSnapshot{phase: "recovery_custody_pending", resolution: "pending", trigger: "prepare_accept", version: 1, commandVersion: 1, schemaVersion: 1}
	if got != want {
		t.Fatalf("command journal genesis = %#v, want v1 pending genesis", snapshot)
	}
}

func assertCommandJournalGenesisRecordedAt(t *testing.T, recordedAt string) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, recordedAt)
	if err != nil || parsed.Location() != time.UTC {
		t.Fatalf("command journal recorded_at = %q, want UTC RFC3339 timestamp", recordedAt)
	}
}

func assertCommandJournalImmutableAndContiguous(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE orchestration_command_journal SET phase = 'accepted' WHERE command_id = 'command-1'`); err == nil {
		t.Fatal("command journal accepted UPDATE")
	}
	if _, err := db.Exec(`DELETE FROM orchestration_command_journal WHERE command_id = 'command-1'`); err == nil {
		t.Fatal("command journal accepted DELETE")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_command_journal (
		command_id, command_version, version, operation_id, operation_digest, phase,
		accept_resolution, attempt_id, trigger, evidence_ref, uncertain_phase, schema_version
	) VALUES ('command-1', 1, 3, 'operation-gap', ?, 'accepted', 'accepted', '', 'accept', 'evidence', '', 1)`, strings.Repeat("b", 64)); err == nil {
		t.Fatal("command journal accepted a version gap")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestration_command_journal WHERE command_id = 'command-1'`).Scan(&count); err != nil {
		t.Fatalf("count command journal rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("command journal row count = %d, want 1 after rejected mutations", count)
	}
}

type commandJournalRowFixture struct {
	CommandID, OperationID, OperationDigest, Phase, AcceptResolution string
	AttemptID, Trigger, EvidenceRef, UncertainPhase                  string
	CommandVersion, Version, SchemaVersion                           int64
}

func validCommandJournalRow(commandID string) commandJournalRowFixture {
	return commandJournalRowFixture{
		CommandID: commandID, OperationID: "operation", OperationDigest: strings.Repeat("a", 64),
		Phase: "recovery_custody_pending", AcceptResolution: "pending", Trigger: "prepare_accept",
		CommandVersion: 1, Version: 1, SchemaVersion: 1,
	}
}

func commandJournalRowWithVersion(commandID string, version int64, operationID, phase, resolution, attemptID, trigger, evidenceRef string) commandJournalRowFixture {
	row := validCommandJournalRow(commandID)
	row.Version, row.OperationID, row.Phase = version, operationID, phase
	row.AcceptResolution, row.AttemptID = resolution, attemptID
	row.Trigger, row.EvidenceRef = trigger, evidenceRef
	return row
}

func insertCommandJournalIntent(t *testing.T, db *sql.DB, commandID string) {
	t.Helper()
	intent := validCommandIntentFixture()
	intent.CommandID, intent.IdempotencyNamespace = commandID, commandID
	insertCommandIntent(t, db, intent)
}

func insertCommandJournalRowOrFatal(t *testing.T, db *sql.DB, row commandJournalRowFixture) {
	t.Helper()
	if _, err := insertCommandJournalRow(db, row); err != nil {
		t.Fatalf("insert command journal row version %d: %v", row.Version, err)
	}
}

func assertSQLiteConstraintCode(t *testing.T, err error, wantCode int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected SQLite constraint code %d, got nil", wantCode)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("error %T = %v, want modernc SQLite constraint code %d", err, err, wantCode)
	}
	if got := sqliteErr.Code(); got != wantCode {
		t.Fatalf("SQLite constraint code = %d (%v), want %d", got, err, wantCode)
	}
}

func TestCommandJournalMigrationAcceptsValidVersionTwoControl(t *testing.T) {
	db := openCommandJournalMigrationDB(t)
	commandID := "journal-valid-v2"
	insertCommandJournalIntent(t, db, commandID)
	insertCommandJournalGenesis(t, db, commandID, "operation-genesis")
	row := commandJournalRowWithVersion(commandID, 2, "operation-accepted", "accepted", "accepted", "", "accept", "evidence")
	insertCommandJournalRowOrFatal(t, db, row)
	assertCommandJournalRowCount(t, db, commandID, 2)
}

func TestCommandJournalMigrationAllowsMultipleObservationsPerAttempt(t *testing.T) {
	db := openCommandJournalMigrationDB(t)
	commandID := "journal-attempt-reuse"
	insertCommandJournalIntent(t, db, commandID)
	insertCommandJournalGenesis(t, db, commandID, "operation-genesis")
	rows := []commandJournalRowFixture{
		commandJournalRowWithVersion(commandID, 2, "operation-begin", "dispatch_custody_pending", "accepted", "attempt-1", "begin_dispatch", "evidence"),
		commandJournalRowWithVersion(commandID, 3, "operation-dispatch", "dispatched", "accepted", "attempt-1", "dispatch", "evidence"),
		commandJournalRowWithVersion(commandID, 4, "operation-ack", "provider_acknowledged", "accepted", "attempt-1", "provider_ack", "evidence"),
		commandJournalRowWithVersion(commandID, 5, "operation-complete", "completed", "accepted", "attempt-1", "complete", "evidence"),
	}
	for _, row := range rows {
		insertCommandJournalRowOrFatal(t, db, row)
	}
	assertCommandJournalAttemptRowCount(t, db, commandID, "attempt-1", len(rows))
}

func TestCommandJournalMigrationRejectsRepeatedNewAttemptTriggers(t *testing.T) {
	cases := []struct {
		name            string
		firstTrigger    string
		repeatedTrigger string
	}{
		{name: "begin twice", firstTrigger: "begin_dispatch", repeatedTrigger: "begin_dispatch"},
		{name: "reclaim twice", firstTrigger: "reclaim_unsent", repeatedTrigger: "reclaim_unsent"},
		{name: "begin then reclaim", firstTrigger: "begin_dispatch", repeatedTrigger: "reclaim_unsent"},
		{name: "reclaim then begin", firstTrigger: "reclaim_unsent", repeatedTrigger: "begin_dispatch"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assertRepeatedNewAttemptTrigger(t, tc.firstTrigger, tc.repeatedTrigger)
		})
	}
}

func assertRepeatedNewAttemptTrigger(t *testing.T, firstTrigger, repeatedTrigger string) {
	t.Helper()
	db := openCommandJournalMigrationDB(t)
	commandID := "journal-repeated-attempt"
	insertCommandJournalIntent(t, db, commandID)
	insertCommandJournalGenesis(t, db, commandID, "operation-genesis")
	first := commandJournalRowWithVersion(commandID, 2, "operation-first", "dispatch_custody_pending", "accepted", "attempt-history", firstTrigger, "evidence")
	insertCommandJournalRowOrFatal(t, db, first)
	repeated := commandJournalRowWithVersion(commandID, 3, "operation-repeated", "dispatched", "accepted", "attempt-history", repeatedTrigger, "evidence")
	_, err := insertCommandJournalRow(db, repeated)
	assertSQLiteConstraintCode(t, err, sqlite3.SQLITE_CONSTRAINT_UNIQUE)
	assertCommandJournalRowCount(t, db, commandID, 2)
}

func TestCommandJournalMigrationRejectsDuplicateOperationWithinCommand(t *testing.T) {
	db := openCommandJournalMigrationDB(t)
	commandID := "journal-operation-unique"
	insertCommandJournalIntent(t, db, commandID)
	insertCommandJournalGenesis(t, db, commandID, "operation-reused")
	duplicate := commandJournalRowWithVersion(commandID, 2, "operation-reused", "accepted", "accepted", "", "accept", "evidence")
	_, err := insertCommandJournalRow(db, duplicate)
	assertSQLiteConstraintCode(t, err, sqlite3.SQLITE_CONSTRAINT_UNIQUE)
	assertCommandJournalRowCount(t, db, commandID, 1)
}

func TestCommandJournalMigrationAllowsSameAttemptAcrossCommands(t *testing.T) {
	db := openCommandJournalMigrationDB(t)
	for _, commandID := range []string{"journal-command-a", "journal-command-b"} {
		insertCommandJournalIntent(t, db, commandID)
		insertCommandJournalGenesis(t, db, commandID, "operation-genesis")
		row := commandJournalRowWithVersion(commandID, 2, "operation-attempt", "dispatch_custody_pending", "accepted", "attempt-shared", "begin_dispatch", "evidence")
		insertCommandJournalRowOrFatal(t, db, row)
	}
	assertCommandJournalAttemptCount(t, db, "attempt-shared", 2)
}

func TestCommandJournalMigrationRequiresExplicitVersion(t *testing.T) {
	db := openCommandJournalMigrationDB(t)
	commandID := "journal-explicit-version"
	insertCommandJournalIntent(t, db, commandID)
	assertCommandJournalVersionHasNoDefault(t, db)
	_, err := insertCommandJournalRowWithoutVersion(db, commandID)
	assertSQLiteConstraintCode(t, err, sqlite3.SQLITE_CONSTRAINT_NOTNULL)
}

func assertCommandJournalRowCount(t *testing.T, db *sql.DB, commandID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestration_command_journal WHERE command_id = ?`, commandID).Scan(&count); err != nil {
		t.Fatalf("count command journal rows for %q: %v", commandID, err)
	}
	if count != want {
		t.Fatalf("command journal rows for %q = %d, want %d", commandID, count, want)
	}
}

func assertCommandJournalAttemptRowCount(t *testing.T, db *sql.DB, commandID, attemptID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestration_command_journal WHERE command_id = ? AND attempt_id = ?`, commandID, attemptID).Scan(&count); err != nil {
		t.Fatalf("count command journal attempt rows for %q/%q: %v", commandID, attemptID, err)
	}
	if count != want {
		t.Fatalf("command journal attempt rows for %q/%q = %d, want %d", commandID, attemptID, count, want)
	}
}

func assertCommandJournalAttemptCount(t *testing.T, db *sql.DB, attemptID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestration_command_journal WHERE attempt_id = ?`, attemptID).Scan(&count); err != nil {
		t.Fatalf("count command journal rows for attempt %q: %v", attemptID, err)
	}
	if count != want {
		t.Fatalf("command journal rows for attempt %q = %d, want %d", attemptID, count, want)
	}
}

func assertCommandJournalVersionHasNoDefault(t *testing.T, db *sql.DB) {
	t.Helper()
	var defaultValue sql.NullString
	err := db.QueryRow(`SELECT dflt_value FROM pragma_table_info('orchestration_command_journal') WHERE name = 'version'`).Scan(&defaultValue)
	if err != nil {
		t.Fatalf("read command journal version default: %v", err)
	}
	if defaultValue.Valid {
		t.Fatalf("command journal version default = %q, want no default", defaultValue.String)
	}
}

func insertCommandJournalRowWithoutVersion(db *sql.DB, commandID string) (sql.Result, error) {
	return db.Exec(`INSERT INTO orchestration_command_journal (
		command_id, command_version, operation_id, operation_digest, phase,
		accept_resolution, attempt_id, trigger, evidence_ref, uncertain_phase, schema_version
	) VALUES (?, 1, 'operation-genesis', ?, 'recovery_custody_pending', 'pending', '', 'prepare_accept', '', '', 1)`, commandID, strings.Repeat("a", 64))
}

func TestCommandJournalMigrationRejectsInvalidStateCombinations(t *testing.T) {
	cases := commandJournalInvalidCases()
	for index, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assertInvalidCommandJournalCase(t, index, tc)
		})
	}
}

type commandJournalInvalidCase struct {
	name          string
	mutate        func(*commandJournalRowFixture)
	seedGenesis   bool
	wantErrorCode int
}

func assertInvalidCommandJournalCase(t *testing.T, index int, tc commandJournalInvalidCase) {
	t.Helper()
	db := openCommandJournalMigrationDB(t)
	commandID := fmt.Sprintf("journal-invalid-%d", index)
	insertCommandJournalIntent(t, db, commandID)
	if tc.seedGenesis {
		insertCommandJournalGenesis(t, db, commandID, "operation-genesis")
	}
	row := validCommandJournalRow(commandID)
	tc.mutate(&row)
	_, err := insertCommandJournalRow(db, row)
	assertSQLiteConstraintCode(t, err, tc.wantErrorCode)
}

func commandJournalInvalidCases() []commandJournalInvalidCase {
	check := sqlite3.SQLITE_CONSTRAINT_CHECK
	trigger := sqlite3.SQLITE_CONSTRAINT_TRIGGER
	return []commandJournalInvalidCase{
		{name: "command version", mutate: func(v *commandJournalRowFixture) { v.CommandVersion = 2 }, wantErrorCode: check},
		{name: "version zero", mutate: func(v *commandJournalRowFixture) { v.Version = 0 }, wantErrorCode: trigger},
		{name: "empty operation id", mutate: func(v *commandJournalRowFixture) { v.OperationID = "" }, wantErrorCode: check},
		{name: "uppercase operation digest", mutate: func(v *commandJournalRowFixture) { v.OperationDigest = strings.Repeat("A", 64) }, wantErrorCode: check},
		{name: "unknown phase", mutate: func(v *commandJournalRowFixture) { v.Phase = "dispatch" }, wantErrorCode: check},
		{name: "unknown resolution", mutate: func(v *commandJournalRowFixture) { v.AcceptResolution = "unknown" }, wantErrorCode: check},
		{name: "long attempt id", mutate: func(v *commandJournalRowFixture) { v.AttemptID = strings.Repeat("a", 129) }, wantErrorCode: check},
		{name: "empty trigger", mutate: func(v *commandJournalRowFixture) { v.Trigger = "" }, wantErrorCode: check},
		{name: "genesis trigger", mutate: func(v *commandJournalRowFixture) { v.Trigger = "accept" }, wantErrorCode: check},
		{name: "genesis evidence", mutate: func(v *commandJournalRowFixture) { v.EvidenceRef = "evidence" }, wantErrorCode: check},
		{name: "genesis uncertain phase", mutate: func(v *commandJournalRowFixture) { v.UncertainPhase = "effect_token_issued" }, wantErrorCode: check},
		{name: "schema version", mutate: func(v *commandJournalRowFixture) { v.SchemaVersion = 2 }, wantErrorCode: check},
		{name: "version two empty evidence", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger = 2, "accepted", "accepted", "accept"
		}},
		{name: "version two prepare trigger", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef = 2, "accepted", "accepted", "prepare_accept", "evidence"
		}},
		{name: "pending non-custody", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef = 2, "accepted", "pending", "accept", "evidence"
		}},
		{name: "aborted non-rejected", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef = 2, "accepted", "aborted", "abort_accept", "evidence"
		}},
		{name: "recovery attempt", mutate: func(v *commandJournalRowFixture) { v.AttemptID = "attempt" }, wantErrorCode: check},
		{name: "accepted attempt", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef, v.AttemptID = 2, "accepted", "accepted", "accept", "evidence", "attempt"
		}},
		{name: "dispatch custody empty attempt", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef = 2, "dispatch_custody_pending", "accepted", "begin_dispatch", "evidence"
		}},
		{name: "completed empty attempt", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef = 2, "completed", "accepted", "complete", "evidence"
		}},
		{name: "unknown invalid uncertain phase", seedGenesis: true, wantErrorCode: check, mutate: func(v *commandJournalRowFixture) {
			v.Version, v.Phase, v.AcceptResolution, v.Trigger, v.EvidenceRef, v.UncertainPhase, v.AttemptID = 2, "outcome_unknown", "accepted", "mark_unknown", "evidence", "accepted", "attempt"
		}},
	}
}

func insertCommandJournalRow(db *sql.DB, row commandJournalRowFixture) (sql.Result, error) {
	return db.Exec(`INSERT INTO orchestration_command_journal (
		command_id, command_version, version, operation_id, operation_digest, phase,
		accept_resolution, attempt_id, trigger, evidence_ref, uncertain_phase, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, row.CommandID, row.CommandVersion, row.Version,
		row.OperationID, row.OperationDigest, row.Phase, row.AcceptResolution, row.AttemptID,
		row.Trigger, row.EvidenceRef, row.UncertainPhase, row.SchemaVersion)
}
