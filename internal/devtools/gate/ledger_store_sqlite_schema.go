package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const durationLedgerSQLiteSchema = `
CREATE TABLE IF NOT EXISTS duration_ledger_meta (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	schema_version INTEGER NOT NULL CHECK (schema_version = 1),
	generation TEXT NOT NULL,
	ledger_version INTEGER NOT NULL,
	legacy_source_sha256 TEXT NOT NULL DEFAULT ''
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
	workload_count INTEGER NOT NULL CHECK (workload_count > 0),
	race_package_count INTEGER NOT NULL CHECK (race_package_count > 0),
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
	schema_version INTEGER NOT NULL CHECK (schema_version = 1),
	updated_at_unix_ms INTEGER NOT NULL
);

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

CREATE INDEX IF NOT EXISTS idx_duration_calibrations_environment
	ON duration_calibrations (
		platform, runner, toolchain, completed_at_unix_ms DESC
	);

CREATE TABLE IF NOT EXISTS duration_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	workload_id TEXT NOT NULL,
	command_digest TEXT NOT NULL,
	platform TEXT NOT NULL,
	runner TEXT NOT NULL,
	toolchain TEXT NOT NULL,
	succeeded INTEGER NOT NULL CHECK (succeeded IN (0, 1)),
	duration_ms INTEGER NOT NULL CHECK (duration_ms > 0),
	target_kind TEXT NOT NULL DEFAULT '',
	parent_workload_id TEXT NOT NULL DEFAULT '',
	parent_command_digest TEXT NOT NULL DEFAULT '',
	target_name TEXT NOT NULL DEFAULT '',
	target_status TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_duration_samples_planning
	ON duration_samples (
		platform, runner, toolchain, workload_id, command_digest, succeeded, id DESC
	);

CREATE INDEX IF NOT EXISTS idx_duration_samples_retention
	ON duration_samples (
		workload_id, platform, toolchain, command_digest, runner, succeeded, id DESC
	);

CREATE INDEX IF NOT EXISTS idx_duration_samples_target
	ON duration_samples (
		parent_workload_id, parent_command_digest, target_name,
		platform, runner, toolchain, target_status, id DESC
	);

CREATE TABLE IF NOT EXISTS ci_query_meta (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	revision TEXT NOT NULL,
	updated_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ci_schema_migrations (
	name TEXT PRIMARY KEY,
	applied_at_unix_ms INTEGER NOT NULL
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
	observed_at_unix_ms INTEGER NOT NULL,
	PRIMARY KEY (catalog_digest, source_tree_sha, entrypoint, profile)
);

CREATE INDEX IF NOT EXISTS idx_ci_catalog_observations_tree_entrypoint
	ON ci_catalog_observations (
		source_tree_sha, entrypoint, observed_at_unix_ms DESC, catalog_digest
	);

CREATE TABLE IF NOT EXISTS ci_catalog_workloads (
	catalog_digest TEXT NOT NULL
		REFERENCES ci_workload_catalogs(catalog_digest) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	workload_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	command_digest TEXT NOT NULL,
	bootstrap_estimate_ms INTEGER NOT NULL CHECK (bootstrap_estimate_ms > 0),
	shardable INTEGER NOT NULL CHECK (shardable IN (0, 1)),
	gate_id TEXT NOT NULL,
	target_kind TEXT NOT NULL DEFAULT '',
	target_value TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (catalog_digest, workload_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ci_catalog_workloads_order
	ON ci_catalog_workloads (catalog_digest, ordinal);

CREATE INDEX IF NOT EXISTS idx_ci_catalog_workloads_identity
	ON ci_catalog_workloads (workload_id, command_digest, catalog_digest);

CREATE INDEX IF NOT EXISTS idx_ci_catalog_workloads_target
	ON ci_catalog_workloads (target_kind, target_value, workload_id);

CREATE TABLE IF NOT EXISTS ci_workload_pass_proofs (
	identity_digest TEXT PRIMARY KEY,
	workload_id TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	object_key TEXT NOT NULL,
	observed_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ci_workload_fingerprints (
	identity_digest TEXT PRIMARY KEY,
	workload_id TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	source_tree_sha TEXT NOT NULL,
	observed_at_unix_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_fingerprint_lookup
	ON ci_workload_fingerprints (
		workload_id, input_digest, environment_digest, observed_at_unix_ms DESC
	);

CREATE TABLE IF NOT EXISTS ci_workload_identity_aliases (
	identity_digest TEXT NOT NULL,
	workload_id TEXT NOT NULL,
	observed_at_unix_ms INTEGER NOT NULL,
	PRIMARY KEY (identity_digest, workload_id)
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_identity_alias_lookup
	ON ci_workload_identity_aliases (
		workload_id, observed_at_unix_ms DESC, identity_digest
	);

INSERT OR IGNORE INTO ci_workload_identity_aliases (
	identity_digest, workload_id, observed_at_unix_ms
)
SELECT identity_digest, workload_id, observed_at_unix_ms
FROM ci_workload_fingerprints
WHERE NOT EXISTS (
	SELECT 1 FROM ci_schema_migrations
	WHERE name = 'workload-identity-aliases-v1'
);

INSERT OR IGNORE INTO ci_workload_identity_aliases (
	identity_digest, workload_id, observed_at_unix_ms
)
SELECT identity_digest, workload_id, observed_at_unix_ms
FROM ci_workload_pass_proofs
WHERE NOT EXISTS (
	SELECT 1 FROM ci_schema_migrations
	WHERE name = 'workload-identity-aliases-v1'
);

INSERT OR IGNORE INTO ci_schema_migrations (name, applied_at_unix_ms)
VALUES ('workload-identity-aliases-v1', CAST(strftime('%s', 'now') AS INTEGER) * 1000);

CREATE TABLE IF NOT EXISTS ci_workload_fingerprint_observations (
	identity_digest TEXT NOT NULL
		REFERENCES ci_workload_fingerprints(identity_digest) ON DELETE CASCADE,
	source_tree_sha TEXT NOT NULL,
	observed_at_unix_ms INTEGER NOT NULL,
	PRIMARY KEY (identity_digest, source_tree_sha)
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_fingerprint_observation_tree
	ON ci_workload_fingerprint_observations (
		source_tree_sha, observed_at_unix_ms DESC, identity_digest
	);

CREATE INDEX IF NOT EXISTS idx_ci_workload_fingerprint_observation_latest
	ON ci_workload_fingerprint_observations (
		identity_digest, observed_at_unix_ms DESC, source_tree_sha DESC
	);

INSERT OR IGNORE INTO ci_workload_fingerprint_observations (
	identity_digest, source_tree_sha, observed_at_unix_ms
)
SELECT identity_digest, source_tree_sha, observed_at_unix_ms
FROM ci_workload_fingerprints
WHERE NOT EXISTS (
	SELECT 1 FROM ci_schema_migrations
	WHERE name = 'fingerprint-observations-v1'
);

INSERT OR IGNORE INTO ci_schema_migrations (name, applied_at_unix_ms)
VALUES ('fingerprint-observations-v1', CAST(strftime('%s', 'now') AS INTEGER) * 1000);

CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_lookup
	ON ci_workload_pass_proofs (
		environment_digest, execution_digest, input_digest, workload_id
	);

CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_compatible
	ON ci_workload_pass_proofs (
		workload_id, execution_digest, environment_digest, observed_at_unix_ms DESC
	);

CREATE TABLE IF NOT EXISTS ci_runs (
	job_id TEXT PRIMARY KEY,
	entrypoint TEXT NOT NULL,
	profile TEXT NOT NULL,
	plan_digest TEXT NOT NULL,
	catalog_digest TEXT NOT NULL,
	source_tree_sha TEXT NOT NULL,
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

CREATE TABLE IF NOT EXISTS ci_run_requesters (
	job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	requester_fingerprint TEXT NOT NULL,
	started_at_unix_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ci_run_requesters_lookup
	ON ci_run_requesters (
		requester_fingerprint, started_at_unix_ms DESC, job_id DESC
	);

CREATE TABLE IF NOT EXISTS ci_run_workloads (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	workload_id TEXT NOT NULL,
	disposition TEXT NOT NULL CHECK (disposition IN ('reused', 'cache_miss')),
	PRIMARY KEY (job_id, workload_id, disposition)
);

CREATE INDEX IF NOT EXISTS idx_ci_run_workloads_lookup
	ON ci_run_workloads (workload_id, disposition, job_id);

CREATE TABLE IF NOT EXISTS ci_shards (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	shard_identity TEXT NOT NULL,
	container_group_id TEXT NOT NULL,
	container_status TEXT NOT NULL,
	materialization_timing_json TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (job_id, shard_identity)
);

CREATE INDEX IF NOT EXISTS idx_ci_shards_container
	ON ci_shards (container_group_id, container_status);

CREATE TABLE IF NOT EXISTS ci_shard_workloads (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	workload_id TEXT NOT NULL,
	PRIMARY KEY (job_id, shard_identity, workload_id),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ci_shard_workloads_lookup
	ON ci_shard_workloads (workload_id, job_id, shard_identity);

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

CREATE INDEX IF NOT EXISTS idx_ci_gate_executions_lookup
	ON ci_gate_executions (workload_id, status, completed_at_unix_ms DESC);

CREATE TABLE IF NOT EXISTS ci_workload_executions (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	workload_id TEXT NOT NULL,
	status TEXT NOT NULL,
	exit_code INTEGER NOT NULL,
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL,
	argv_digest TEXT NOT NULL,
	log_digest TEXT NOT NULL,
	test_timings_json TEXT NOT NULL DEFAULT '[]',
	execution_profile_json TEXT NOT NULL,
	PRIMARY KEY (job_id, workload_id)
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_executions_lookup
	ON ci_workload_executions (workload_id, status, completed_at_unix_ms DESC);

CREATE TABLE IF NOT EXISTS ci_run_phase_timings (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	phase TEXT NOT NULL,
	started_at_unix_ms INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
	outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed')),
	workload_count INTEGER NOT NULL CHECK (workload_count >= 0),
	shard_count INTEGER NOT NULL CHECK (shard_count >= 0),
	cache_hit_count INTEGER NOT NULL CHECK (cache_hit_count >= 0),
	cache_miss_count INTEGER NOT NULL CHECK (cache_miss_count >= 0),
	PRIMARY KEY (job_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_ci_run_phase_timings_hotspots
	ON ci_run_phase_timings (
		phase, duration_ms DESC, started_at_unix_ms DESC, job_id
	);

CREATE TABLE IF NOT EXISTS ci_run_warnings (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	warning_text TEXT NOT NULL,
	PRIMARY KEY (job_id, ordinal)
);

PRAGMA user_version = 1;
`

func durationLedgerSQLiteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", fmt.Sprintf("busy_timeout=%d", durationLedgerSQLiteBusyTimeoutMS))
	query.Add("_pragma", "foreign_keys=ON")
	query.Add("_pragma", "journal_mode=WAL")
	query.Add("_pragma", "synchronous=FULL")
	query.Add("_pragma", "wal_autocheckpoint=1000")
	query.Add("_txlock", "immediate")
	return path + "?" + query.Encode()
}

// ensureDurationLedgerSQLiteSchema 初始化或协调SQLite schema。
func ensureDurationLedgerSQLiteSchema(database *sql.DB) error {
	var schemaVersion int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return mapDurationLedgerSQLiteError("read duration ledger SQLite schema version", err)
	}
	switch schemaVersion {
	case 0:
		if _, err := database.Exec(durationLedgerSQLiteSchema); err != nil {
			return mapDurationLedgerSQLiteError("initialize duration ledger SQLite schema", err)
		}
	case durationLedgerSQLiteSchemaVersion:
		if _, err := database.Exec(durationLedgerSQLiteSchema); err != nil {
			return mapDurationLedgerSQLiteError("reconcile duration ledger SQLite v1 schema", err)
		}
		if err := ensureDurationLedgerExecutionProfileColumn(database); err != nil {
			return err
		}
		if err := ensureDurationLedgerTestTimingsColumns(database); err != nil {
			return err
		}
		if err := ensureDurationLedgerShardMaterializationTimingColumn(database); err != nil {
			return err
		}
		if err := ensureRemoteBaselineStateSQLiteSchema(database); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"duration ledger SQLite schema version %d is unsupported",
			schemaVersion,
		)
	}
	return nil
}

// ensureRemoteBaselineStateSQLiteSchema 拒绝 v1 或 legacy 状态；调用方必须显式迁移。
func ensureRemoteBaselineStateSQLiteSchema(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "inspect remote baseline state schema", func(transaction *sql.Tx) error {
		columns, err := durationLedgerSQLiteTableColumns(transaction, "ci_remote_baseline_state")
		if err != nil {
			return err
		}
		if columns["legacy_json"] {
			return ErrRemoteBaselineStateMigrationRequired
		}
		return nil
	})
}

func durationLedgerSQLiteTableColumns(transaction *sql.Tx, table string) (map[string]bool, error) {
	rows, err := transaction.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite table %s: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var (
			columnID, notNull, primaryKey int
			name, dataType                string
			defaultValue                  any
		)
		if err := rows.Scan(&columnID, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan SQLite table %s column: %w", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite table %s columns: %w", table, err)
	}
	return columns, nil
}

func ensureDurationLedgerShardMaterializationTimingColumn(database *sql.DB) error {
	rows, err := database.Query(`PRAGMA table_info(ci_shards)`)
	if err != nil {
		return mapDurationLedgerSQLiteError("inspect remote CI shard schema", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return mapDurationLedgerSQLiteError("scan remote CI shard schema", err)
		}
		if name == "materialization_timing_json" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate remote CI shard schema", err)
	}
	if _, err := database.Exec(`ALTER TABLE ci_shards ADD COLUMN materialization_timing_json TEXT NOT NULL DEFAULT ''`); err != nil {
		return mapDurationLedgerSQLiteError("migrate remote CI shard materialization timing", err)
	}
	return nil
}

func ensureDurationLedgerTestTimingsColumns(database *sql.DB) error {
	for _, table := range []string{"ci_gate_executions", "ci_workload_executions"} {
		rows, err := database.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			return mapDurationLedgerSQLiteError("inspect remote CI test timing schema", err)
		}
		var found bool
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return mapDurationLedgerSQLiteError("scan remote CI test timing schema", err)
			}
			found = found || name == "test_timings_json"
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return mapDurationLedgerSQLiteError("iterate remote CI test timing schema", err)
		}
		if err := rows.Close(); err != nil {
			return mapDurationLedgerSQLiteError("close remote CI test timing schema", err)
		}
		if found {
			continue
		}
		if _, err := database.Exec(`ALTER TABLE ` + table + ` ADD COLUMN test_timings_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return mapDurationLedgerSQLiteError("migrate remote CI test timings", err)
		}
	}
	return nil
}

func ensureDurationLedgerExecutionProfileColumn(database *sql.DB) error {
	rows, err := database.Query(`PRAGMA table_info(ci_gate_executions)`)
	if err != nil {
		return mapDurationLedgerSQLiteError("inspect remote CI execution schema", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return mapDurationLedgerSQLiteError("scan remote CI execution schema", err)
		}
		if name == "execution_profile_json" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate remote CI execution schema", err)
	}
	if _, err := database.Exec(`ALTER TABLE ci_gate_executions ADD COLUMN execution_profile_json TEXT NOT NULL DEFAULT ''`); err != nil {
		return mapDurationLedgerSQLiteError("migrate remote CI execution profile", err)
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
	return fmt.Errorf("%s transaction exceeded SQLite busy retry limit", operation)
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
