package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"

	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const durationLedgerSQLiteSchema = `
CREATE TABLE IF NOT EXISTS duration_ledger_meta (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	authority_id TEXT NOT NULL,
	schema_version INTEGER NOT NULL CHECK (schema_version = 1),
	generation TEXT NOT NULL,
	ledger_version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS duration_calibrations (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	schema_version INTEGER NOT NULL,
	commit_sha TEXT NOT NULL,
	tree_sha TEXT NOT NULL,
	platform TEXT NOT NULL,
	runner TEXT NOT NULL,
	toolchain TEXT NOT NULL,
	commit_entrypoint TEXT NOT NULL,
	push_entrypoint TEXT NOT NULL,
	release_entrypoint TEXT NOT NULL,
	commit_catalog_digest TEXT NOT NULL,
	push_catalog_digest TEXT NOT NULL,
	release_catalog_digest TEXT NOT NULL,
	calibration_resource_class_id TEXT NOT NULL CHECK (length(trim(calibration_resource_class_id)) > 0 AND calibration_resource_class_id <> 'medium'),
	calibration_resource_cpu REAL NOT NULL CHECK (calibration_resource_cpu = 4),
	calibration_resource_memory_gib REAL NOT NULL CHECK (calibration_resource_memory_gib = 8),
	workload_count INTEGER NOT NULL CHECK (workload_count > 0),
	race_package_count INTEGER NOT NULL CHECK (race_package_count > 0),
	accepted_snapshot_id TEXT NOT NULL CHECK (length(trim(accepted_snapshot_id)) > 0),
	completed_at_unix_ms INTEGER NOT NULL
);

-- remote baseline 的接受状态共用 duration ledger SQLite authority。
-- v3 只接受 ECI ImageCache state；历史形状不得参与该 authority。
CREATE TABLE IF NOT EXISTS ci_remote_baseline_state (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	schema_version INTEGER NOT NULL CHECK (schema_version = 3),
	generation TEXT NOT NULL,
	state_json TEXT NOT NULL DEFAULT '',
	state_sha256 TEXT NOT NULL DEFAULT '',
	updated_at_unix_ms INTEGER NOT NULL,
	CHECK (state_json <> '' AND state_sha256 <> '')
);

-- remote calibration checkpoint 与 duration samples 共用同一 SQLite authority。
CREATE TABLE IF NOT EXISTS remote_ci_calibration_checkpoints (
	identity TEXT PRIMARY KEY,
	schema_version INTEGER NOT NULL CHECK (schema_version = 3),
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	agent_token_digest TEXT NOT NULL,
	updated_at_unix_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_remote_ci_calibration_checkpoints_retention
	ON remote_ci_calibration_checkpoints (accepted_generation, identity);

CREATE TABLE IF NOT EXISTS remote_ci_calibration_checkpoint_scenarios (
	identity TEXT NOT NULL REFERENCES remote_ci_calibration_checkpoints(identity) ON DELETE CASCADE,
	scenario TEXT NOT NULL CHECK (length(trim(scenario)) > 0),
	started INTEGER NOT NULL CHECK (started IN (0, 1)),
	completed INTEGER NOT NULL CHECK (completed IN (0, 1)),
	input_json TEXT NOT NULL DEFAULT '',
	result_json TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (identity, scenario),
	CHECK ((completed = 0 AND input_json = '' AND result_json = '') OR
		(completed = 1 AND input_json <> '' AND result_json <> ''))
);

CREATE TABLE IF NOT EXISTS duration_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	workload_id TEXT NOT NULL,
	command_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL CHECK (length(trim(input_digest)) > 0),
	platform TEXT NOT NULL,
	runner TEXT NOT NULL,
	toolchain TEXT NOT NULL,
	execution_mode TEXT NOT NULL CHECK (execution_mode IN ('normal', 'calibration')),
	resource_class_id TEXT NOT NULL CHECK (length(trim(resource_class_id)) > 0),
	resource_cpu REAL NOT NULL CHECK (resource_cpu > 0),
	resource_memory_gib REAL NOT NULL CHECK (resource_memory_gib > 0),
	succeeded INTEGER NOT NULL CHECK (succeeded IN (0, 1)),
	duration_ms INTEGER NOT NULL CHECK (duration_ms > 0),
	target_kind TEXT NOT NULL DEFAULT '',
	parent_workload_id TEXT NOT NULL DEFAULT '',
	parent_command_digest TEXT NOT NULL DEFAULT '',
	target_name TEXT NOT NULL DEFAULT '',
	target_status TEXT NOT NULL DEFAULT '',
	CHECK (
		(execution_mode = 'calibration' AND resource_cpu = 4 AND resource_memory_gib = 8) OR
		(execution_mode = 'normal' AND ((resource_cpu = 2 AND resource_memory_gib = 4) OR
			(resource_cpu = 4 AND resource_memory_gib = 8) OR
			(resource_cpu = 8 AND resource_memory_gib = 16))))
);

CREATE INDEX IF NOT EXISTS idx_duration_samples_planning
	ON duration_samples (
		execution_mode, platform, runner, toolchain, resource_cpu, resource_memory_gib,
		resource_class_id, workload_id, command_digest, input_digest, succeeded, id DESC
	);

CREATE INDEX IF NOT EXISTS idx_duration_samples_retention
	ON duration_samples (
		accepted_generation, id DESC
	);

CREATE TABLE IF NOT EXISTS duration_shard_overheads (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	schema_version INTEGER NOT NULL CHECK (schema_version = 2),
	policy_version TEXT NOT NULL CHECK (policy_version = 'accounted-interval-union-nearest-rank-p95-v2'),
	platform TEXT NOT NULL,
	runner TEXT NOT NULL,
	toolchain TEXT NOT NULL,
	calibration_resource_class_id TEXT NOT NULL,
	calibration_resource_cpu REAL NOT NULL CHECK (calibration_resource_cpu = 4),
	calibration_resource_memory_gib REAL NOT NULL CHECK (calibration_resource_memory_gib = 8),
	p95_ms INTEGER NOT NULL CHECK (p95_ms >= 0),
	sample_count INTEGER NOT NULL CHECK (sample_count > 0),
	provenance_digest TEXT NOT NULL,
	accepted_snapshot_id TEXT NOT NULL CHECK (length(trim(accepted_snapshot_id)) > 0),
	UNIQUE (accepted_generation, platform, runner, toolchain, calibration_resource_class_id, provenance_digest, accepted_snapshot_id)
);

CREATE INDEX IF NOT EXISTS idx_duration_shard_overheads_planning
	ON duration_shard_overheads (
		platform, runner, toolchain, accepted_generation DESC, id DESC
	);

CREATE INDEX IF NOT EXISTS idx_duration_shard_overheads_retention
	ON duration_shard_overheads (
		accepted_generation, id DESC
	);

CREATE TABLE IF NOT EXISTS duration_shard_overhead_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	provenance_digest TEXT NOT NULL,
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	shard_identity TEXT NOT NULL,
	total_started_at_unix_ms INTEGER NOT NULL,
	total_completed_at_unix_ms INTEGER NOT NULL,
	workload_envelope_start_unix_ms INTEGER NOT NULL,
	workload_envelope_end_unix_ms INTEGER NOT NULL,
	accounted_duration_ms INTEGER NOT NULL CHECK (accounted_duration_ms > 0),
	accounted_interval_count INTEGER NOT NULL CHECK (accounted_interval_count > 0),
	overhead_ms INTEGER NOT NULL CHECK (overhead_ms >= 0),
	UNIQUE (accepted_generation, provenance_digest, job_id, shard_identity)
);

CREATE INDEX IF NOT EXISTS idx_duration_shard_overhead_samples_retention
	ON duration_shard_overhead_samples (
		accepted_generation, id DESC
	);

CREATE INDEX IF NOT EXISTS idx_duration_shard_overhead_samples_planning
	ON duration_shard_overhead_samples (
		accepted_generation, provenance_digest, job_id, shard_identity
	);

CREATE INDEX IF NOT EXISTS idx_duration_shard_overhead_samples_shard_fk
	ON duration_shard_overhead_samples (job_id, shard_identity);

CREATE TABLE IF NOT EXISTS ci_query_meta (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	revision TEXT NOT NULL,
	updated_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ci_workload_catalogs (
	catalog_digest TEXT PRIMARY KEY,
	catalog_version INTEGER NOT NULL,
	authoritative INTEGER NOT NULL CHECK (authoritative IN (0, 1)),
	workload_count INTEGER NOT NULL CHECK (workload_count > 0),
	created_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ci_catalog_observations (
	catalog_digest TEXT NOT NULL
		REFERENCES ci_workload_catalogs(catalog_digest) ON DELETE CASCADE,
	source_tree_sha TEXT NOT NULL,
	entrypoint TEXT NOT NULL,
	profile TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	observed_at_unix_ms INTEGER NOT NULL,
	PRIMARY KEY (catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation)
);

CREATE INDEX IF NOT EXISTS idx_ci_catalog_observations_catalog_order
	ON ci_catalog_observations (
		catalog_digest, observed_at_unix_ms DESC, source_tree_sha, entrypoint, profile, accepted_generation
	);

-- workload catalog authority lookup 按完整 observation identity 建立前缀。
-- 覆盖 catalog_digest 后，DISTINCT 回读不退化为全表扫描。
CREATE INDEX IF NOT EXISTS idx_ci_catalog_observations_identity_catalog
	ON ci_catalog_observations (
		source_tree_sha, entrypoint, profile, accepted_generation, catalog_digest
	);

CREATE INDEX IF NOT EXISTS idx_ci_catalog_observations_retention
	ON ci_catalog_observations (accepted_generation, catalog_digest);

CREATE TABLE IF NOT EXISTS ci_catalog_workloads (
	catalog_digest TEXT NOT NULL
		REFERENCES ci_workload_catalogs(catalog_digest) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	workload_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	command_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL DEFAULT '',
	bootstrap_estimate_ms INTEGER NOT NULL CHECK (bootstrap_estimate_ms > 0),
	shardable INTEGER NOT NULL CHECK (shardable IN (0, 1)),
	gate_id TEXT NOT NULL,
	target_kind TEXT NOT NULL DEFAULT '',
	target_value TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (catalog_digest, workload_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ci_catalog_workloads_order
	ON ci_catalog_workloads (catalog_digest, ordinal);

CREATE TABLE IF NOT EXISTS ci_runs (
	job_id TEXT PRIMARY KEY,
	force INTEGER NOT NULL DEFAULT 0 CHECK (force IN (0, 1)),
	entrypoint TEXT NOT NULL,
	profile TEXT NOT NULL,
	plan_digest TEXT NOT NULL,
	catalog_digest TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	image_cache_snapshot_id TEXT NOT NULL CHECK (length(trim(image_cache_snapshot_id)) > 0),
	source_tree_sha TEXT NOT NULL,
	candidate_gate_source_sha256 TEXT NOT NULL DEFAULT '',
	candidate_gate_toolchain_sha256 TEXT NOT NULL DEFAULT '',
	runner_image TEXT NOT NULL,
	status TEXT NOT NULL,
	authoritative INTEGER NOT NULL CHECK (authoritative IN (0, 1)),
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL,
	cleanup_complete INTEGER NOT NULL CHECK (cleanup_complete IN (0, 1)),
	error_text TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ci_runs_tree_status
	ON ci_runs (source_tree_sha, status, completed_at_unix_ms DESC);

CREATE INDEX IF NOT EXISTS idx_ci_runs_catalog_status
	ON ci_runs (catalog_digest, status, completed_at_unix_ms DESC);

CREATE INDEX IF NOT EXISTS idx_ci_runs_accepted_generation
	ON ci_runs (accepted_generation, completed_at_unix_ms DESC, job_id DESC);

CREATE TABLE IF NOT EXISTS ci_run_agent_identities (
	job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	agent_token_digest TEXT NOT NULL,
	started_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ci_shards (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	shard_identity TEXT NOT NULL,
	container_group_id TEXT NOT NULL,
	container_status TEXT NOT NULL,
	materialization_timing_json TEXT NOT NULL DEFAULT '',
	resources_json TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (job_id, shard_identity)
);

CREATE TABLE IF NOT EXISTS ci_shard_workloads (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	workload_id TEXT NOT NULL,
	PRIMARY KEY (job_id, shard_identity, workload_id),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ci_gate_executions (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	workload_id TEXT NOT NULL,
	status TEXT NOT NULL,
	exit_code INTEGER NOT NULL,
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL,
	argv_digest TEXT NOT NULL,
	log_digest TEXT NOT NULL,
	test_timings_json TEXT NOT NULL DEFAULT '[]',
	execution_profile_json TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (job_id, workload_id)
);

CREATE TABLE IF NOT EXISTS ci_workload_executions (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	workload_id TEXT NOT NULL,
	status TEXT NOT NULL,
	exit_code INTEGER NOT NULL,
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL,
	argv_digest TEXT NOT NULL,
	log_digest TEXT NOT NULL,
	test_timings_json TEXT NOT NULL DEFAULT '[]',
	execution_profile_json TEXT NOT NULL,
	PRIMARY KEY (job_id, workload_id),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_executions_shard_fk
	ON ci_workload_executions (job_id, shard_identity);

CREATE TABLE IF NOT EXISTS ci_timing_observations (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	scope TEXT NOT NULL CHECK (scope IN ('run', 'shard', 'workload', 'compile_group')),
	shard_identity TEXT NOT NULL DEFAULT '',
	workload_id TEXT NOT NULL DEFAULT '',
	phase TEXT NOT NULL,
	started_at_unix_ms INTEGER NOT NULL DEFAULT 0,
	completed_at_unix_ms INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
	measurement TEXT NOT NULL CHECK (measurement IN ('measured', 'not_applicable')),
	reason TEXT NOT NULL DEFAULT '',
	aggregation TEXT NOT NULL CHECK (aggregation IN ('raw', 'interval_union', 'critical_path')),
	cache_evidence_json TEXT NOT NULL,
	compile_group_id TEXT NOT NULL DEFAULT '',
	compile_artifact_key TEXT NOT NULL DEFAULT '',
	compile_package_target TEXT NOT NULL DEFAULT '',
	compile_workload_ids_json TEXT NOT NULL DEFAULT '[]',
	compile_artifact_sha256 TEXT NOT NULL DEFAULT '',
	compile_artifact_size INTEGER NOT NULL DEFAULT 0 CHECK (compile_artifact_size >= 0),
	compile_cache_hits INTEGER NOT NULL DEFAULT 0 CHECK (compile_cache_hits >= 0),
	compile_cache_misses INTEGER NOT NULL DEFAULT 0 CHECK (compile_cache_misses >= 0),
	compile_cache_puts INTEGER NOT NULL DEFAULT 0 CHECK (compile_cache_puts >= 0),
	compile_cache_status TEXT NOT NULL DEFAULT '',
	compile_status TEXT NOT NULL DEFAULT '',
	compile_exit_code INTEGER NOT NULL DEFAULT 0,
	compile_error_text TEXT NOT NULL DEFAULT '',
	compile_command_digest TEXT NOT NULL DEFAULT '',
	compile_profile_digest TEXT NOT NULL DEFAULT '',
	compile_resource_class_id TEXT NOT NULL DEFAULT '',
	compile_resource_cpu REAL NOT NULL DEFAULT 0,
	compile_resource_memory_gib REAL NOT NULL DEFAULT 0,
	compile_execution_mode TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (job_id, scope, shard_identity, workload_id, phase, compile_group_id, compile_artifact_key)
);

CREATE INDEX IF NOT EXISTS idx_ci_timing_observations_compile_group
	ON ci_timing_observations (job_id, scope, shard_identity, compile_group_id, compile_artifact_key, phase);

CREATE TABLE IF NOT EXISTS ci_run_warnings (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	warning_text TEXT NOT NULL,
	PRIMARY KEY (job_id, ordinal)
);
`

func durationLedgerSQLiteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", "auto_vacuum=FULL")
	query.Add("_pragma", fmt.Sprintf("busy_timeout=%d", durationLedgerSQLiteBusyTimeoutMS))
	query.Add("_pragma", "foreign_keys=ON")
	query.Add("_pragma", "journal_mode=WAL")
	query.Add("_pragma", "synchronous=FULL")
	query.Add("_pragma", "wal_autocheckpoint=1000")
	query.Add("_txlock", "immediate")
	return path + "?" + query.Encode()
}

// ensureDurationLedgerSQLiteSchemaWithValidator 在唯一 current DDL 写入器上执行严格 schema 协调。
func ensureDurationLedgerSQLiteSchemaWithValidator(
	database *sql.DB,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
) error {
	if now == nil {
		return errors.New("duration ledger schema clock is required")
	}
	if validator == nil || validator.expectedSchema == nil || validator.initializeAuthority == nil {
		return errors.New("duration ledger schema validator is required")
	}
	schemaVersion, err := readDurationLedgerSQLiteSchemaVersion(database)
	if err != nil {
		return err
	}
	if err := coordinateDurationLedgerSQLiteSchemaVersion(database, now, validator, schemaVersion); err != nil {
		return err
	}
	if err := verifyDurationLedgerSQLiteAutoVacuum(database); err != nil {
		return err
	}
	return verifyDurationLedgerSQLiteCurrentAuthority(database)
}

// verifyDurationLedgerSQLiteAutoVacuum 保证历史淘汰提交时同步归还空页。
func verifyDurationLedgerSQLiteAutoVacuum(database *sql.DB) error {
	var mode int
	if err := database.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return mapDurationLedgerSQLiteError("read duration ledger SQLite auto vacuum mode", err)
	}
	if mode != 1 {
		return fmt.Errorf("duration ledger SQLite auto_vacuum mode %d must equal FULL(1); explicit VACUUM migration is required", mode)
	}
	return nil
}

// coordinateDurationLedgerSQLiteSchemaVersion 只接受空库初始化或当前版本；退役版本必须显式清理。
func coordinateDurationLedgerSQLiteSchemaVersion(database *sql.DB, now func() time.Time, validator *durationLedgerSQLiteSchemaValidator, schemaVersion int) error {
	switch schemaVersion {
	case 0:
		return validator.initializeAuthority(database, now, validator)
	case legacyDurationLedgerSQLiteSchemaVersion:
		if err := migrateDurationLedgerSQLiteSchema13To14(database, now); err != nil {
			return err
		}
		if err := migrateDurationLedgerSQLiteSchema14To15(database); err != nil {
			return err
		}
		return migrateDurationLedgerSQLiteSchema15To16(database)
	case localDurationLedgerSQLiteSchemaVersion:
		if err := migrateDurationLedgerSQLiteSchema14To15(database); err != nil {
			return err
		}
		return migrateDurationLedgerSQLiteSchema15To16(database)
	case executionScopeDurationLedgerSQLiteSchemaVersion:
		return migrateDurationLedgerSQLiteSchema15To16(database)
	case durationLedgerSQLiteSchemaVersion:
		return validator.preflight(database, schemaVersion)
	default:
		return fmt.Errorf("duration ledger SQLite schema version %d is unsupported", schemaVersion)
	}
}

type durationLedgerSQLiteSchemaVersionReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readDurationLedgerSQLiteSchemaVersion(reader durationLedgerSQLiteSchemaVersionReader) (int, error) {
	var schemaVersion int
	if err := reader.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return 0, mapDurationLedgerSQLiteError("read duration ledger SQLite schema version", err)
	}
	return schemaVersion, nil
}

// initializeDurationLedgerSQLiteCurrentSchema initializes a truly empty
// authority atomically. Concurrent initializers serialize on BEGIN IMMEDIATE
// and re-read the complete shape while holding that lock.
func initializeDurationLedgerSQLiteCurrentSchema(
	database *sql.DB,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
) error {
	return initializeDurationLedgerSQLiteCurrentSchemaWithStatements(
		database,
		now,
		validator,
		durationLedgerSQLiteCurrentSchemaStatements(),
	)
}

func initializeDurationLedgerSQLiteCurrentSchemaWithStatements(
	database *sql.DB,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
	statements []string,
) error {
	if _, err := validator.expectedSchema(); err != nil {
		return err
	}
	for attempt := range 16 {
		err := initializeDurationLedgerSQLiteCurrentSchemaOnce(database, now, validator, statements)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrDurationLedgerBusy) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return fmt.Errorf("%w: initialize duration ledger SQLite schema exceeded busy retry limit", ErrDurationLedgerBusy)
}

func initializeDurationLedgerSQLiteCurrentSchemaOnce(
	database *sql.DB,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
	statements []string,
) error {
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite initializer connection", err)
	}
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			mapDurationLedgerSQLiteError("begin duration ledger SQLite initializer transaction", err))
	}
	if err := initializeDurationLedgerSQLiteCurrentSchemaOnConnection(connection, now, validator, statements); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection, err))
	}
	_, err = connection.ExecContext(context.Background(), `COMMIT`)
	if err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection,
				mapDurationLedgerSQLiteError("commit duration ledger SQLite initializer transaction", err)))
	}
	return closeDurationLedgerSQLiteInitializerConnection(connection, nil)
}

func rollbackDurationLedgerSQLiteInitializer(connection *sql.Conn, cause error) error {
	if _, err := connection.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		return fmt.Errorf("initializer failed: %v; %w", cause,
			mapDurationLedgerSQLiteError("rollback duration ledger SQLite initializer transaction", err))
	}
	return cause
}

func closeDurationLedgerSQLiteInitializerConnection(connection *sql.Conn, cause error) error {
	if err := connection.Close(); err != nil {
		return fmt.Errorf("initializer result: %v; close initializer connection: %w", cause, err)
	}
	return cause
}

// initializeDurationLedgerSQLiteCurrentSchemaOnConnection 在单个连接事务内完成空库初始化。
func initializeDurationLedgerSQLiteCurrentSchemaOnConnection(
	connection *sql.Conn,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
	statements []string,
) error {
	schemaVersion, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if schemaVersion == durationLedgerSQLiteSchemaVersion {
		return validator.preflight(connection, schemaVersion)
	}
	if schemaVersion != 0 {
		return fmt.Errorf("duration ledger SQLite schema version %d is unsupported", schemaVersion)
	}
	return initializeDurationLedgerSQLiteEmptySchemaOnConnection(connection, now, validator, schemaVersion, statements)
}

// initializeDurationLedgerSQLiteEmptySchemaOnConnection 在已验证的空 authority 事务内执行当前 schema DDL、版本写入和最终预检。
func initializeDurationLedgerSQLiteEmptySchemaOnConnection(
	connection *sql.Conn,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
	schemaVersion int,
	statements []string,
) error {
	if err := validator.preflight(connection, schemaVersion); err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("initialize duration ledger SQLite current schema", err)
		}
	}
	if err := initializeLocalAuthorityStateOnConnection(connection, now); err != nil {
		return err
	}
	if _, err := connection.ExecContext(
		context.Background(),
		fmt.Sprintf(`PRAGMA user_version = %d`, durationLedgerSQLiteSchemaVersion),
	); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite current schema version", err)
	}
	return validator.preflight(connection, durationLedgerSQLiteSchemaVersion)
}

func verifyDurationLedgerSQLiteCurrentAuthority(database *sql.DB) error {
	if err := verifyDurationLedgerMetadataAuthorityDatabase(database); err != nil {
		return err
	}
	if err := verifyLocalAuthorityStateDatabase(database); err != nil {
		return err
	}
	return verifyDurationLedgerSQLAuthorityBindings(database)
}

// verifyDurationLedgerMetadataAuthorityDatabase 只读校验已有 metadata 不会指向另一个持久化 authority。
func verifyDurationLedgerMetadataAuthorityDatabase(database *sql.DB) error {
	var authorityID string
	err := database.QueryRow(`SELECT authority_id FROM duration_ledger_meta WHERE singleton = 1`).Scan(&authorityID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load duration ledger authority metadata", err)
	}
	if authorityID != cicontract.SQLAuthorityID {
		return fmt.Errorf("duration ledger SQLite authority ID %q must equal %q", authorityID, cicontract.SQLAuthorityID)
	}
	return nil
}

// verifyDurationLedgerSQLAuthorityBindings 拒绝缺失任一契约指定的 SQLite 事实表。
func verifyDurationLedgerSQLAuthorityBindings(database *sql.DB) error {
	for _, binding := range cicontract.SQLAuthorityBindings() {
		var count int
		if err := database.QueryRow(`
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`, binding.Table).Scan(&count); err != nil {
			return mapDurationLedgerSQLiteError("verify duration ledger SQL authority binding", err)
		}
		if count != 1 {
			return fmt.Errorf(
				"duration ledger SQLite authority is missing canonical table %q for domain %q",
				binding.Table,
				binding.Domain,
			)
		}
	}
	return nil
}

// withSQLiteWriteTransaction 在SQLite繁忙时重试写事务。
func withSQLiteWriteTransaction(
	database *sql.DB,
	operation string,
	write func(*sql.Tx) error,
) error {
	for attempt := range 16 {
		transaction, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			mapped := mapDurationLedgerSQLiteError("begin "+operation+" transaction", err)
			if errors.Is(mapped, ErrDurationLedgerBusy) {
				time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
				continue
			}
			return mapped
		}
		if err := write(transaction); err != nil {
			rollbackErr := rollbackSQLiteTransaction(transaction, operation)
			combined := errors.Join(err, rollbackErr)
			if rollbackErr == nil && errors.Is(err, ErrDurationLedgerBusy) {
				time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
				continue
			}
			return combined
		}
		if err := transaction.Commit(); err != nil {
			mapped := mapDurationLedgerSQLiteError("commit "+operation+" transaction", err)
			rollbackErr := rollbackSQLiteTransaction(transaction, operation)
			if rollbackErr == nil && errors.Is(mapped, ErrDurationLedgerBusy) {
				time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
				continue
			}
			return errors.Join(mapped, rollbackErr)
		}
		return nil
	}
	return fmt.Errorf("%w: %s transaction exceeded SQLite busy retry limit", ErrDurationLedgerBusy, operation)
}

func rollbackSQLiteTransaction(transaction *sql.Tx, operation string) error {
	err := transaction.Rollback()
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return mapDurationLedgerSQLiteError("rollback "+operation+" transaction", err)
}

func mapDurationLedgerSQLiteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if sqliteError, ok := errors.AsType[*sqlitedriver.Error](err); ok {
		primary := sqliteError.Code() & 0xff
		if primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED {
			return fmt.Errorf("%w: %s: %v", ErrDurationLedgerBusy, operation, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
