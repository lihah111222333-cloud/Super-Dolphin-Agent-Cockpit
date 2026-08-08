package gate

import (
	"database/sql"
	"testing"
	"time"
)

func TestCalibrationCheckpointSchemaV1RejectsEmptyLegacyTablesWithoutMutation(t *testing.T) {
	database := openLegacyCalibrationCheckpointDatabase(t, "empty.sqlite", "")
	defer database.Close()
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil {
		t.Fatal("empty legacy calibration checkpoint schema was accepted")
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("empty legacy calibration schema changed:\nwant %s\n got %s", beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != beforeVersion {
		t.Fatalf("empty legacy calibration user_version = %d, want %d", got, beforeVersion)
	}
}

func TestCalibrationCheckpointSchemaV1RejectsNonemptyLegacyDataWithoutMutation(t *testing.T) {
	database := openLegacyCalibrationCheckpointDatabase(t, "nonempty.sqlite", `
		INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, accepted_generation, agent_token_digest, updated_at_unix_ms)
		VALUES ('sha256:legacy', 1, '7', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1);
	`)
	defer database.Close()
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil {
		t.Fatal("nonempty legacy calibration checkpoint schema was accepted")
	}
	var schemaVersion, rows int
	if err := database.QueryRow(`SELECT schema_version FROM remote_ci_calibration_checkpoints WHERE identity = 'sha256:legacy'`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM remote_ci_calibration_checkpoints`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 1 || rows != 1 {
		t.Fatalf("rejected schema mutated legacy checkpoint: schema_version=%d rows=%d", schemaVersion, rows)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("nonempty legacy calibration schema changed:\nwant %s\n got %s", beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != beforeVersion {
		t.Fatalf("nonempty legacy calibration user_version = %d, want %d", got, beforeVersion)
	}
}

func openLegacyCalibrationCheckpointDatabase(t *testing.T, name, rowsSQL string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(t.TempDir()+"/"+name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE remote_ci_calibration_checkpoints (
			identity TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version = 1),
			accepted_generation TEXT NOT NULL,
			agent_token_digest TEXT NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		);
		CREATE TABLE remote_ci_calibration_checkpoint_scenarios (
			identity TEXT NOT NULL REFERENCES remote_ci_calibration_checkpoints(identity) ON DELETE CASCADE,
			scenario TEXT NOT NULL,
			started INTEGER NOT NULL,
			completed INTEGER NOT NULL,
			input_json TEXT NOT NULL DEFAULT '',
			result_json TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (identity, scenario)
		);` + rowsSQL); err != nil {
		database.Close()
		t.Fatal(err)
	}
	return database
}
