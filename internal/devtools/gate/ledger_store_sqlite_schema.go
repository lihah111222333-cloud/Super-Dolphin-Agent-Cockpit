package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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

-- remote baseline refresh lease 将跨进程刷新抢占和 accepted baseline 绑定到同一 SQLite authority。
CREATE TABLE IF NOT EXISTS ci_remote_baseline_refresh_lease (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	schema_version INTEGER NOT NULL CHECK (schema_version = 1),
	attempt_generation TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	accepted_state_sha256 TEXT NOT NULL,
	target_generation TEXT NOT NULL,
	token TEXT NOT NULL,
	builder_job_id TEXT NOT NULL DEFAULT '',
	target_tree_sha TEXT NOT NULL DEFAULT '',
	phase TEXT NOT NULL CHECK (phase IN ('idle', 'claimed', 'building', 'cache_preparing', 'ready_validated', 'promoted', 'retiring', 'cleanup_pending', 'unchanged', 'failed')),
	lease_expires_at_unix_ms INTEGER NOT NULL,
	last_started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL DEFAULT 0,
	image_cache_name TEXT NOT NULL DEFAULT '',
	image_cache_id TEXT NOT NULL DEFAULT '',
	successor_image TEXT NOT NULL DEFAULT '',
	successor_generation TEXT NOT NULL DEFAULT '',
	successor_state_sha256 TEXT NOT NULL DEFAULT '',
	retiring_image_cache_id TEXT NOT NULL DEFAULT '',
	failure_text TEXT NOT NULL DEFAULT '',
	CHECK (attempt_generation <> '' AND accepted_generation <> '' AND accepted_state_sha256 <> '' AND
		target_generation <> '' AND token <> '' AND lease_expires_at_unix_ms > 0 AND last_started_at_unix_ms > 0),
	CHECK ((phase IN ('claimed', 'building', 'cache_preparing', 'ready_validated') AND completed_at_unix_ms = 0) OR
		(phase IN ('promoted', 'retiring', 'cleanup_pending', 'unchanged', 'failed', 'idle') AND completed_at_unix_ms >= 0))
);

-- remote calibration checkpoint 与 duration samples 共用同一 SQLite authority。
CREATE TABLE IF NOT EXISTS remote_ci_calibration_checkpoints (
	identity TEXT PRIMARY KEY,
	schema_version INTEGER NOT NULL CHECK (schema_version = 1),
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
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
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
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
		accepted_generation, id DESC
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
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	observed_at_unix_ms INTEGER NOT NULL,
	PRIMARY KEY (catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation)
);

CREATE INDEX IF NOT EXISTS idx_ci_catalog_observations_tree_entrypoint
	ON ci_catalog_observations (
		entrypoint, profile, accepted_generation, observed_at_unix_ms DESC, catalog_digest
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

CREATE TABLE IF NOT EXISTS ci_runs (
	job_id TEXT PRIMARY KEY,
	entrypoint TEXT NOT NULL,
	profile TEXT NOT NULL,
	plan_digest TEXT NOT NULL,
	catalog_digest TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
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

CREATE TABLE IF NOT EXISTS ci_run_requesters (
	job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	requester_fingerprint TEXT NOT NULL,
	started_at_unix_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ci_run_requesters_lookup
	ON ci_run_requesters (
		requester_fingerprint, started_at_unix_ms DESC, job_id DESC
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

CREATE INDEX IF NOT EXISTS idx_ci_workload_executions_lookup
	ON ci_workload_executions (workload_id, status, completed_at_unix_ms DESC);

CREATE TABLE IF NOT EXISTS ci_timing_observations (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	scope TEXT NOT NULL CHECK (scope IN ('run', 'shard', 'workload')),
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
	PRIMARY KEY (job_id, scope, shard_identity, workload_id, phase)
);

CREATE TABLE IF NOT EXISTS ci_run_warnings (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	warning_text TEXT NOT NULL,
	PRIMARY KEY (job_id, ordinal)
);

PRAGMA user_version = 2;
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
func ensureDurationLedgerSQLiteSchema(database *sql.DB, now func() time.Time) error {
	if now == nil {
		return errors.New("duration ledger schema clock is required")
	}
	var schemaVersion int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return mapDurationLedgerSQLiteError("read duration ledger SQLite schema version", err)
	}
	switch schemaVersion {
	case 0:
		if _, err := database.Exec(durationLedgerSQLiteSchema); err != nil {
			return mapDurationLedgerSQLiteError("initialize duration ledger SQLite schema", err)
		}
	case 1:
		fallthrough
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
		if err := ensureDurationLedgerShardResourcesColumn(database); err != nil {
			return err
		}
		if err := ensureDurationLedgerCandidateGateCompileIdentityColumns(database); err != nil {
			return err
		}
		if err := ensureDurationLedgerWorkloadShardBinding(database); err != nil {
			return err
		}
		if err := ensureDurationLedgerTimingObservationDurationColumn(database); err != nil {
			return err
		}
		if err := ensureRemoteBaselineStateSQLiteSchema(database); err != nil {
			return err
		}
		if err := ensureRemoteBaselineRefreshLeaseSQLiteSchema(database); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"duration ledger SQLite schema version %d is unsupported",
			schemaVersion,
		)
	}
	if err := ensureAcceptedGenerationRetentionSchema(database); err != nil {
		return err
	}
	if err := rejectLegacyWorkloadReuseSchema(database, now); err != nil {
		return err
	}
	if err := rejectRetiredRunPhaseTimingSchema(database); err != nil {
		return err
	}
	if err := ensureDurationLedgerMetadataAuthority(database); err != nil {
		return err
	}
	if err := ensureRemoteCIAuthorityBindingSQLiteSchema(database); err != nil {
		return err
	}
	return verifyDurationLedgerSQLAuthorityBindings(database)
}

func ensureDurationLedgerTimingObservationDurationColumn(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "migrate timing observation duration", func(transaction *sql.Tx) error {
		columns, err := durationLedgerSQLiteTableColumns(transaction, "ci_timing_observations")
		if err != nil {
			return err
		}
		if columns["duration_ms"] {
			return nil
		}
		var rows int
		if err := transaction.QueryRow(`SELECT COUNT(*) FROM ci_timing_observations`).Scan(&rows); err != nil {
			return mapDurationLedgerSQLiteError("count legacy timing observations", err)
		}
		if rows != 0 {
			return errors.New("legacy ci_timing_observations rows lack duration_ms; refuse to infer interval unions from envelopes")
		}
		if _, err := transaction.Exec(`ALTER TABLE ci_timing_observations ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0)`); err != nil {
			return mapDurationLedgerSQLiteError("add timing observation duration", err)
		}
		return nil
	})
}

// ensureAcceptedGenerationRetentionSchema 在同一事务内收敛历史根的代际来源。
// 旧表中的非空行无法证明所属 accepted baseline，必须拒绝而不能猜测当前代。
func ensureAcceptedGenerationRetentionSchema(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "migrate accepted generation retention roots", func(transaction *sql.Tx) error {
		missing := false
		for _, binding := range cicontract.RetentionRootBindings() {
			columns, err := durationLedgerSQLiteTableColumns(transaction, binding.Table)
			if err != nil {
				return err
			}
			if !columns[binding.GenerationColumn] {
				missing = true
			}
		}
		if !missing {
			return nil
		}
		for _, binding := range cicontract.RetentionRootBindings() {
			var rows int
			if err := transaction.QueryRow(`SELECT COUNT(*) FROM ` + binding.Table).Scan(&rows); err != nil {
				return mapDurationLedgerSQLiteError("count legacy accepted generation rows", err)
			}
			if rows != 0 {
				return fmt.Errorf("%w: mixed retention schema cannot rebuild nonempty root %s with %d rows", ErrMigrationRequired, binding.Table, rows)
			}
		}
		for _, binding := range cicontract.RetentionRootBindings() {
			if _, err := transaction.Exec(`DROP TABLE ` + binding.Table); err != nil {
				return mapDurationLedgerSQLiteError("drop empty legacy retention root", err)
			}
		}
		if _, err := transaction.Exec(durationLedgerSQLiteSchema); err != nil {
			return mapDurationLedgerSQLiteError("rebuild empty accepted generation retention roots", err)
		}
		if _, err := transaction.Exec(`PRAGMA user_version = ` + strconv.Itoa(durationLedgerSQLiteSchemaVersion)); err != nil {
			return mapDurationLedgerSQLiteError("advance duration ledger SQLite schema version", err)
		}
		return nil
	})
}

// rejectRetiredRunPhaseTimingSchema refuses authorities containing the retired
// second timing source. It never reads, writes, or migrates its records.
func rejectRetiredRunPhaseTimingSchema(database *sql.DB) error {
	const retiredTable = "ci_run_phase_timings"
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, retiredTable).Scan(&count); err != nil {
		return mapDurationLedgerSQLiteError("inspect retired remote CI phase timing table", err)
	}
	if count != 0 {
		return fmt.Errorf("retired remote CI phase timing table %q is present; refuse incompatible authority", retiredTable)
	}
	return nil
}

func ensureDurationLedgerWorkloadShardBinding(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "migrate workload execution shard binding", func(transaction *sql.Tx) error {
		columns, err := durationLedgerSQLiteTableColumns(transaction, "ci_workload_executions")
		if err != nil {
			return err
		}
		if columns["shard_identity"] {
			return nil
		}
		var rows int
		if err := transaction.QueryRow(`SELECT COUNT(*) FROM ci_workload_executions`).Scan(&rows); err != nil {
			return mapDurationLedgerSQLiteError("count legacy workload executions", err)
		}
		if rows != 0 {
			return errors.New("legacy ci_workload_executions rows lack shard_identity; refuse incompatible authority read")
		}
		if _, err := transaction.Exec(`ALTER TABLE ci_workload_executions ADD COLUMN shard_identity TEXT NOT NULL DEFAULT ''`); err != nil {
			return mapDurationLedgerSQLiteError("add workload execution shard binding", err)
		}
		return nil
	})
}

// rejectLegacyWorkloadReuseSchema 只清理无事实的旧表；发现任何历史记录立即拒绝继续。
func rejectLegacyWorkloadReuseSchema(database *sql.DB, now func() time.Time) error {
	const migrationName = "retire-workload-result-reuse-v1"
	retiredTables := []string{
		"ci_run_workloads",
		"ci_workload_pass_proofs",
		"ci_workload_fingerprints",
		"ci_workload_identity_aliases",
		"ci_workload_fingerprint_observations",
	}
	return withSQLiteWriteTransaction(database, "retire legacy workload reuse tables", func(transaction *sql.Tx) error {
		for _, table := range retiredTables {
			var count int
			err := transaction.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
			if err != nil {
				return mapDurationLedgerSQLiteError("inspect retired workload reuse table", err)
			}
			if count == 0 {
				continue
			}
			var rows int
			if err := transaction.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&rows); err != nil {
				return mapDurationLedgerSQLiteError("count retired workload reuse rows", err)
			}
			if rows != 0 {
				return fmt.Errorf("retired workload reuse table %q contains %d records; refuse to discard historical facts", table, rows)
			}
			if _, err := transaction.Exec(`DROP TABLE ` + table); err != nil {
				return mapDurationLedgerSQLiteError("drop retired workload reuse table", err)
			}
		}
		if _, err := transaction.Exec(`INSERT OR IGNORE INTO ci_schema_migrations(name, applied_at_unix_ms) VALUES(?, ?)`, migrationName, now().UTC().UnixMilli()); err != nil {
			return mapDurationLedgerSQLiteError("record retired workload reuse migration", err)
		}
		return nil
	})
}

// ensureDurationLedgerMetadataAuthority 删除旧 JSON 迁移残留并固定 SQLite authority 身份。
func ensureDurationLedgerMetadataAuthority(database *sql.DB) error {
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger authority metadata migration", err)
	}
	defer transaction.Rollback()
	columns, err := durationLedgerSQLiteTableColumns(transaction, "duration_ledger_meta")
	if err != nil {
		return err
	}
	if !columns["authority_id"] || columns["legacy_source_sha256"] {
		if _, err := transaction.Exec(`
			CREATE TABLE duration_ledger_meta_next (
				singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
				authority_id TEXT NOT NULL,
				schema_version INTEGER NOT NULL CHECK (schema_version = 1),
				generation TEXT NOT NULL,
				ledger_version INTEGER NOT NULL
			)
		`); err != nil {
			return mapDurationLedgerSQLiteError("create duration ledger authority metadata table", err)
		}
		if _, err := transaction.Exec(`
			INSERT INTO duration_ledger_meta_next (
				singleton, authority_id, schema_version, generation, ledger_version
			)
			SELECT singleton, ?, schema_version, generation, ledger_version
			FROM duration_ledger_meta
		`, cicontract.SQLAuthorityID); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger authority metadata", err)
		}
		if _, err := transaction.Exec(`DROP TABLE duration_ledger_meta`); err != nil {
			return mapDurationLedgerSQLiteError("drop legacy duration ledger metadata", err)
		}
		if _, err := transaction.Exec(`ALTER TABLE duration_ledger_meta_next RENAME TO duration_ledger_meta`); err != nil {
			return mapDurationLedgerSQLiteError("rename duration ledger authority metadata", err)
		}
	}
	var authorityID string
	err = transaction.QueryRow(`
		SELECT authority_id FROM duration_ledger_meta WHERE singleton = 1
	`).Scan(&authorityID)
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return mapDurationLedgerSQLiteError("load duration ledger authority metadata", err)
		}
		if authorityID != cicontract.SQLAuthorityID {
			return fmt.Errorf(
				"duration ledger SQLite authority ID %q must equal %q",
				authorityID,
				cicontract.SQLAuthorityID,
			)
		}
	}
	if err := transaction.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger authority metadata migration", err)
	}
	return nil
}

// ensureRemoteCIAuthorityBindingSQLiteSchema 只新增 cicontract 指定的 authority 表，不读取旧表。
func ensureRemoteCIAuthorityBindingSQLiteSchema(database *sql.DB) error {
	const refreshDDL = `CREATE TABLE IF NOT EXISTS ci_remote_refresh_deltas (
		job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
		attempt_generation TEXT NOT NULL,
		accepted_generation TEXT NOT NULL,
		accepted_state_sha256 TEXT NOT NULL,
		accepted_snapshot_id TEXT NOT NULL,
		delta_identity TEXT NOT NULL,
		delta_sha256 TEXT NOT NULL,
		delta_size_bytes INTEGER NOT NULL CHECK (delta_size_bytes > 0),
		target_tree_sha TEXT NOT NULL,
		target_closure_sha256 TEXT NOT NULL,
		transfer_mode TEXT NOT NULL CHECK (transfer_mode = 'accepted_snapshot_delta'),
		recorded_at_unix_ms INTEGER NOT NULL,
		lease_singleton INTEGER NOT NULL DEFAULT 1 REFERENCES ci_remote_baseline_refresh_lease(singleton) ON DELETE RESTRICT,
		PRIMARY KEY (job_id, attempt_generation, delta_identity),
		UNIQUE (job_id, attempt_generation, delta_sha256)
	)`
	const checkDDL = `CREATE TABLE IF NOT EXISTS ci_check_receipts (
		run_id TEXT NOT NULL,
		job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
		candidate_tree_sha TEXT NOT NULL,
		accepted_generation TEXT NOT NULL,
		accepted_snapshot_id TEXT NOT NULL,
		required_check TEXT NOT NULL,
		executed INTEGER NOT NULL CHECK (executed = 1),
		passed INTEGER NOT NULL CHECK (passed IN (0, 1)),
		started_at_unix_ms INTEGER NOT NULL,
		completed_at_unix_ms INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL CHECK (duration_ms > 0),
		receipt_sha256 TEXT NOT NULL,
		PRIMARY KEY (job_id, required_check),
		UNIQUE (run_id, required_check),
		CHECK (completed_at_unix_ms >= started_at_unix_ms),
		CHECK (completed_at_unix_ms - started_at_unix_ms = duration_ms)
	)`
	if _, err := database.Exec(refreshDDL); err != nil {
		return mapDurationLedgerSQLiteError("add remote refresh delta authority table", err)
	}
	if _, err := database.Exec(checkDDL); err != nil {
		return mapDurationLedgerSQLiteError("add check receipt authority table", err)
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

func ensureRemoteBaselineRefreshLeaseSQLiteSchema(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "migrate remote baseline refresh lease schema", func(transaction *sql.Tx) error {
		columns, err := durationLedgerSQLiteTableColumns(transaction, "ci_remote_baseline_refresh_lease")
		if err != nil {
			return err
		}
		if columns["phase"] {
			for _, column := range []string{"builder_job_id", "target_tree_sha", "successor_image", "successor_generation", "successor_state_sha256", "retiring_image_cache_id"} {
				if columns[column] {
					continue
				}
				if _, err := transaction.Exec(`ALTER TABLE ci_remote_baseline_refresh_lease ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
					return err
				}
			}
			return nil
		}
		if !columns["candidate_state"] || !columns["status"] {
			return errors.New("remote baseline refresh lease schema migration required")
		}
		if _, err := transaction.Exec(`ALTER TABLE ci_remote_baseline_refresh_lease ADD COLUMN phase TEXT NOT NULL DEFAULT 'claimed'`); err != nil {
			return err
		}
		_, err = transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET phase = CASE status WHEN 'succeeded' THEN 'promoted' WHEN 'failed' THEN 'failed' ELSE candidate_state END`)
		return err
	})
}

func ensureDurationLedgerShardResourcesColumn(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "inspect remote CI shard resource schema", func(transaction *sql.Tx) error {
		columns, err := durationLedgerSQLiteTableColumns(transaction, "ci_shards")
		if err != nil {
			return err
		}
		if columns["resources_json"] {
			return nil
		}
		if _, err := transaction.Exec(`ALTER TABLE ci_shards ADD COLUMN resources_json TEXT NOT NULL DEFAULT ''`); err != nil {
			return mapDurationLedgerSQLiteError("migrate remote CI shard resources", err)
		}
		return ensureRemoteBaselineRefreshLeasePhaseCheck(transaction)
	})
}

func ensureRemoteBaselineRefreshLeasePhaseCheck(transaction *sql.Tx) error {
	var ddl string
	if err := transaction.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='ci_remote_baseline_refresh_lease'`).Scan(&ddl); err != nil {
		return err
	}
	if strings.Contains(ddl, "'unchanged'") {
		return nil
	}
	if _, err := transaction.Exec(`CREATE TABLE ci_remote_baseline_refresh_lease_next (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1), schema_version INTEGER NOT NULL CHECK (schema_version = 1),
		attempt_generation TEXT NOT NULL, accepted_generation TEXT NOT NULL, accepted_state_sha256 TEXT NOT NULL, target_generation TEXT NOT NULL,
		token TEXT NOT NULL, builder_job_id TEXT NOT NULL DEFAULT '', target_tree_sha TEXT NOT NULL DEFAULT '', phase TEXT NOT NULL CHECK (phase IN ('idle','claimed','building','cache_preparing','ready_validated','promoted','retiring','cleanup_pending','unchanged','failed')),
		lease_expires_at_unix_ms INTEGER NOT NULL, last_started_at_unix_ms INTEGER NOT NULL, completed_at_unix_ms INTEGER NOT NULL DEFAULT 0,
		image_cache_name TEXT NOT NULL DEFAULT '', image_cache_id TEXT NOT NULL DEFAULT '', successor_image TEXT NOT NULL DEFAULT '',
		successor_generation TEXT NOT NULL DEFAULT '', successor_state_sha256 TEXT NOT NULL DEFAULT '', retiring_image_cache_id TEXT NOT NULL DEFAULT '', failure_text TEXT NOT NULL DEFAULT '',
		CHECK (attempt_generation <> '' AND accepted_generation <> '' AND accepted_state_sha256 <> '' AND target_generation <> '' AND token <> '' AND lease_expires_at_unix_ms > 0 AND last_started_at_unix_ms > 0),
		CHECK ((phase IN ('claimed','building','cache_preparing','ready_validated') AND completed_at_unix_ms = 0) OR (phase IN ('promoted','retiring','cleanup_pending','unchanged','failed','idle') AND completed_at_unix_ms >= 0))
	)`); err != nil {
		return err
	}
	if _, err := transaction.Exec(`INSERT INTO ci_remote_baseline_refresh_lease_next SELECT singleton,schema_version,attempt_generation,accepted_generation,accepted_state_sha256,target_generation,token,'','',phase,lease_expires_at_unix_ms,last_started_at_unix_ms,completed_at_unix_ms,image_cache_name,image_cache_id,successor_image,successor_generation,successor_state_sha256,retiring_image_cache_id,failure_text FROM ci_remote_baseline_refresh_lease`); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DROP TABLE ci_remote_baseline_refresh_lease`); err != nil {
		return err
	}
	_, err := transaction.Exec(`ALTER TABLE ci_remote_baseline_refresh_lease_next RENAME TO ci_remote_baseline_refresh_lease`)
	return err
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

// ensureDurationLedgerCandidateGateCompileIdentityColumns keeps legacy records explicitly non-comparable.
func ensureDurationLedgerCandidateGateCompileIdentityColumns(database *sql.DB) error {
	return withSQLiteWriteTransaction(database, "inspect remote CI candidate gate compile identity schema", func(transaction *sql.Tx) error {
		columns, err := durationLedgerSQLiteTableColumns(transaction, "ci_runs")
		if err != nil {
			return err
		}
		for _, column := range []string{"candidate_gate_source_sha256", "candidate_gate_toolchain_sha256"} {
			if columns[column] {
				continue
			}
			if _, err := transaction.Exec(`ALTER TABLE ci_runs ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				return mapDurationLedgerSQLiteError("migrate remote CI candidate gate compile identity", err)
			}
		}
		return nil
	})
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
