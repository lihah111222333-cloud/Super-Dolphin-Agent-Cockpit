package gate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

type durationLedgerSQLiteLegacyShapeFixture struct {
	name, table, definition, insert, dataQuery, wantData string
}

func TestDurationLedgerSQLiteCurrentSchemaOpenIsReadOnly(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "current-read-only.sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatal(err)
	}
	validator := newDurationLedgerSQLiteSchemaValidator()
	initializerCalled := false
	validator.initializeAuthority = func(*sql.DB, *durationLedgerSQLiteSchemaValidator) error {
		initializerCalled = true
		return nil
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA query_only = ON`); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, validator); err != nil {
		t.Fatalf("open exact current schema on query-only connection: %v", err)
	}
	if initializerCalled {
		t.Fatal("current schema open invoked the DDL initializer")
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("current read-only open changed sqlite_master:\nwant %s\n got %s", beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != beforeVersion {
		t.Fatalf("current read-only open changed user_version to %d, want %d", got, beforeVersion)
	}
}

func TestDurationLedgerSQLiteInitializationFailureRollsBackEverything(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "rollback.sqlite")
	defer database.Close()
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	if err := initializeDurationLedgerSQLiteCurrentSchemaWithStatements(database, newDurationLedgerSQLiteSchemaValidator(), []string{
		`CREATE TABLE partial_authority (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE broken_authority (`,
	}); err == nil {
		t.Fatal("injected schema failure unexpectedly succeeded")
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("failed initializer left partial schema:\nwant %s\n got %s", beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 0 {
		t.Fatalf("failed initializer user_version = %d, want 0", got)
	}
}

func TestDurationLedgerSQLiteConcurrentEmptyInitializationConverges(t *testing.T) {
	path := t.TempDir() + "/concurrent.sqlite"
	databases := []*sql.DB{
		openStrictSchemaDatabaseAtPath(t, path),
		openStrictSchemaDatabaseAtPath(t, path),
	}
	defer databases[0].Close()
	defer databases[1].Close()
	for _, database := range databases {
		if err := database.Ping(); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errorsByInitializer := make([]error, len(databases))
	var group errgroup.Group
	for index, database := range databases {
		group.Go(func() error {
			<-start
			errorsByInitializer[index] = ensureDurationLedgerSQLiteSchema(database, time.Now)
			return errorsByInitializer[index]
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent initializer: %v", err)
	}
	for index, err := range errorsByInitializer {
		if err != nil {
			t.Fatalf("initializer %d: %v", index, err)
		}
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, databases[0]); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("concurrent initializer user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	if err := preflightDurationLedgerSQLiteExactSchema(databases[0], durationLedgerSQLiteSchemaVersion); err != nil {
		t.Fatalf("concurrent initializer final shape: %v", err)
	}
}

func TestDurationLedgerSQLiteRejectsRogueViewAndTriggerWithoutMutation(t *testing.T) {
	for name, rogueDDL := range map[string]string{
		"view": `CREATE VIEW rogue_duration_ledger_view AS
			SELECT name FROM ci_schema_migrations`,
		"trigger": `CREATE TRIGGER rogue_duration_ledger_trigger
			AFTER INSERT ON ci_schema_migrations BEGIN SELECT 1; END`,
	} {
		t.Run(name, func(t *testing.T) {
			assertRogueSchemaRefusal(t, name, rogueDDL)
		})
	}
}

// assertRogueSchemaRefusal 证明额外 view/trigger 不会绕过严格 schema preflight。
func assertRogueSchemaRefusal(t *testing.T, name, rogueDDL string) {
	t.Helper()
	database := openStrictSchemaTestDatabase(t, name+".sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_schema_migrations(name, applied_at_unix_ms) VALUES ('preserve-me', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(rogueDDL); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("rogue %s schema error = %v, want exact-schema refusal", name, err)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("rogue %s refusal changed sqlite_master:\nwant %s\n got %s", name, beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != beforeVersion {
		t.Fatalf("rogue %s refusal changed user_version to %d, want %d", name, got, beforeVersion)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_schema_migrations WHERE name = 'preserve-me'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rogue %s refusal changed authority data: rows=%d err=%v", name, rows, err)
	}
}

func TestDurationLedgerSQLiteRejectsRetiredSchemaVersionsBeforeMutation(t *testing.T) {
	for _, retiredVersion := range []int{1, 2, 3, 9} {
		t.Run(fmt.Sprintf("v%d", retiredVersion), func(t *testing.T) {
			assertRetiredSchemaVersionRefusal(t, retiredVersion)
		})
	}
}

// assertRetiredSchemaVersionRefusal 证明历史 user_version 只读拒绝且不改数据。
func assertRetiredSchemaVersionRefusal(t *testing.T, retiredVersion int) {
	t.Helper()
	database := openStrictSchemaTestDatabase(t, fmt.Sprintf("v%d.sqlite", retiredVersion))
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_schema_migrations(name, applied_at_unix_ms) VALUES ('preserve-me', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, retiredVersion)); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("retired schema version %d error = %v", retiredVersion, err)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("retired schema v%d refusal changed sqlite_master:\nwant %s\n got %s", retiredVersion, beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != retiredVersion {
		t.Fatalf("retired schema refusal changed user_version to %d, want %d", got, retiredVersion)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_schema_migrations WHERE name = 'preserve-me'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("retired schema refusal changed data: rows=%d err=%v", rows, err)
	}
}
func TestDurationLedgerSQLiteRejectsRetiredCompatibilityShapesBeforeMutation(t *testing.T) {
	fixtures := []durationLedgerSQLiteLegacyShapeFixture{
		{
			name: "executed only check receipt", table: "ci_check_receipts",
			definition: `CREATE TABLE ci_check_receipts (
				run_id TEXT NOT NULL, job_id TEXT NOT NULL, required_check TEXT NOT NULL,
				executed INTEGER NOT NULL CHECK (executed = 1), passed INTEGER NOT NULL,
				PRIMARY KEY(job_id, required_check))`,
			insert:    `INSERT INTO ci_check_receipts VALUES ('run-1','job-1','go-test',1,1)`,
			dataQuery: `SELECT required_check FROM ci_check_receipts WHERE job_id='job-1'`, wantData: "go-test",
		},
		{
			name: "nonempty run missing identity", table: "ci_runs",
			definition: `CREATE TABLE ci_runs (
				job_id TEXT PRIMARY KEY, source_tree_sha TEXT NOT NULL,
				status TEXT NOT NULL, catalog_digest TEXT NOT NULL)`,
			insert:    `INSERT INTO ci_runs VALUES ('job-1','tree-1','passed','catalog-1')`,
			dataQuery: `SELECT job_id FROM ci_runs`, wantData: "job-1",
		},
		{
			name: "legacy shard overhead aggregate policy", table: "duration_shard_overheads",
			definition: `CREATE TABLE duration_shard_overheads (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				accepted_generation TEXT NOT NULL,
				schema_version INTEGER NOT NULL CHECK (schema_version = 1),
				policy_version TEXT NOT NULL CHECK (policy_version = 'nearest-rank-p95-v1'),
				platform TEXT NOT NULL, runner TEXT NOT NULL, toolchain TEXT NOT NULL,
				calibration_resource_class_id TEXT NOT NULL,
				calibration_resource_cpu REAL NOT NULL, calibration_resource_memory_gib REAL NOT NULL,
				p95_ms INTEGER NOT NULL, sample_count INTEGER NOT NULL,
				provenance_digest TEXT NOT NULL, accepted_snapshot_id TEXT NOT NULL)`,
			insert:    `INSERT INTO duration_shard_overheads VALUES (1,'1',1,'nearest-rank-p95-v1','linux/amd64','runner','toolchain','calibration',4,8,1,1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','snapshot')`,
			dataQuery: `SELECT policy_version FROM duration_shard_overheads`, wantData: "nearest-rank-p95-v1",
		},
		{
			name: "nonempty shard missing timing and resources", table: "ci_shards",
			definition: `CREATE TABLE ci_shards (
				job_id TEXT NOT NULL, shard_identity TEXT NOT NULL, status TEXT NOT NULL,
				PRIMARY KEY(job_id, shard_identity))`,
			insert:    `INSERT INTO ci_shards VALUES ('job-1','shard-1','passed')`,
			dataQuery: `SELECT shard_identity FROM ci_shards`, wantData: "shard-1",
		},
		{
			name: "nonempty gate execution missing profile and timings", table: "ci_gate_executions",
			definition: `CREATE TABLE ci_gate_executions (
				job_id TEXT PRIMARY KEY, command_json TEXT NOT NULL)`,
			insert:    `INSERT INTO ci_gate_executions VALUES ('job-1','[]')`,
			dataQuery: `SELECT job_id FROM ci_gate_executions`, wantData: "job-1",
		},
		{
			name: "nonempty workload execution missing shard and timings", table: "ci_workload_executions",
			definition: `CREATE TABLE ci_workload_executions (
				job_id TEXT NOT NULL, workload_id TEXT NOT NULL, command_json TEXT NOT NULL,
				PRIMARY KEY(job_id, workload_id))`,
			insert:    `INSERT INTO ci_workload_executions VALUES ('job-1','workload-1','[]')`,
			dataQuery: `SELECT workload_id FROM ci_workload_executions`, wantData: "workload-1",
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			assertDurationLedgerSQLiteRejectsLegacyShape(t, fixture)
		})
	}
}

func assertDurationLedgerSQLiteRejectsLegacyShape(t *testing.T, fixture durationLedgerSQLiteLegacyShapeFixture) {
	t.Helper()
	database := openStrictSchemaTestDatabase(t, strings.ReplaceAll(fixture.name, " ", "-")+".sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatal(err)
	}
	replaceDurationLedgerSQLiteTableForTest(t, database, fixture)
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
	var beforeData string
	if err := database.QueryRow(fixture.dataQuery).Scan(&beforeData); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
		t.Fatal("retired compatibility shape was accepted")
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("schema refusal changed sqlite_master:\nwant %s\n got %s", beforeMaster, got)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != beforeVersion {
		t.Fatalf("schema refusal changed user_version to %d, want %d", got, beforeVersion)
	}
	var afterData string
	if err := database.QueryRow(fixture.dataQuery).Scan(&afterData); err != nil || afterData != beforeData || afterData != fixture.wantData {
		t.Fatalf("schema refusal changed data: got %q err=%v, want %q", afterData, err, fixture.wantData)
	}
}

func replaceDurationLedgerSQLiteTableForTest(
	t *testing.T,
	database *sql.DB,
	fixture durationLedgerSQLiteLegacyShapeFixture,
) {
	t.Helper()
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`DROP TABLE ` + fixture.table,
		fixture.definition,
		fixture.insert,
		`PRAGMA foreign_keys = ON`,
	} {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("replace %s: %v", fixture.table, err)
		}
	}
}

func TestDurationLedgerSQLiteExpectedSchemaCacheIsConcurrentSafe(t *testing.T) {
	const readers = 32
	validator := newDurationLedgerSQLiteSchemaValidator()
	errorsByReader := make([]error, readers)
	objectCounts := make([]int, readers)
	var group errgroup.Group
	for index := range readers {
		group.Go(func() error {
			schema, err := validator.expectedSchema()
			errorsByReader[index] = err
			objectCounts[index] = len(schema)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("cached schema reader: %v", err)
	}
	for index, err := range errorsByReader {
		if err != nil {
			t.Fatalf("cached schema reader %d: %v", index, err)
		}
		if objectCounts[index] == 0 || objectCounts[index] != objectCounts[0] {
			t.Fatalf("cached schema reader %d object count = %d, want %d", index, objectCounts[index], objectCounts[0])
		}
	}
}

func openStrictSchemaTestDatabase(t *testing.T, name string) *sql.DB {
	t.Helper()
	return openStrictSchemaDatabaseAtPath(t, t.TempDir()+"/"+name)
}

func openStrictSchemaDatabaseAtPath(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	return database
}
