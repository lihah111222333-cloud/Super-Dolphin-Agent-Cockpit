package gate

// frozenWorkloadPassReuseSQLiteSchemaV13 is the exact remote PASS DDL from the
// v13 HEAD authority. It is deliberately separate from the current schema:
// historical migration preflight must not derive its expectation from a
// candidate that may add a later-version object.
const frozenWorkloadPassReuseSQLiteSchemaV13 = `
CREATE TABLE IF NOT EXISTS ci_run_workload_results (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	workload_id TEXT NOT NULL,
	identity_digest TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	disposition TEXT NOT NULL CHECK (disposition IN ('executed', 'reused')),
	origin_job_id TEXT NOT NULL,
	origin_accepted_generation TEXT NOT NULL,
	evidence_sha256 TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (job_id, workload_id)
);

CREATE TABLE IF NOT EXISTS ci_workload_pass_evidence (
	identity_digest TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	workload_id TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	origin_job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	origin_source_tree_sha TEXT NOT NULL,
	origin_receipt_set_sha256 TEXT NOT NULL,
	origin_execution_json TEXT NOT NULL,
	evidence_sha256 TEXT NOT NULL,
	PRIMARY KEY (identity_digest, accepted_generation)
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_evidence_origin_job
	ON ci_workload_pass_evidence (origin_job_id);

CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_evidence_retention
	ON ci_workload_pass_evidence (accepted_generation, identity_digest);

-- Historical migration matches the requested workload execution/environment
-- tuple inside the retained generation window. Keep workload first so each
-- bounded request probes only its requested tuples instead of scanning the
-- evidence table or relying on the origin-job projection.
`

const strictWorkloadPassReuseSQLiteSchema = `
CREATE TABLE IF NOT EXISTS ci_run_workload_results (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	workload_id TEXT NOT NULL,
	identity_digest TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	disposition TEXT NOT NULL CHECK (disposition IN ('executed', 'reused')),
	origin_job_id TEXT NOT NULL,
	origin_accepted_generation TEXT NOT NULL,
	evidence_sha256 TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (job_id, workload_id)
);

CREATE TABLE IF NOT EXISTS ci_workload_pass_evidence (
	identity_digest TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	workload_id TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	origin_job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	origin_source_tree_sha TEXT NOT NULL,
	origin_receipt_set_sha256 TEXT NOT NULL,
	origin_execution_json TEXT NOT NULL,
	evidence_sha256 TEXT NOT NULL,
	PRIMARY KEY (identity_digest, accepted_generation)
);

CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_evidence_origin_job
	ON ci_workload_pass_evidence (origin_job_id);

CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_evidence_retention
	ON ci_workload_pass_evidence (accepted_generation, identity_digest);

`

// strictLocalWorkloadPassSQLiteSchema is additive to the frozen remote PASS
// table.  Local evidence has its own namespace column and origin FK, so the
// historical remote table, composite key and ECI origin FK stay byte-for-byte
// compatible across the v13 to v14 migration.
const strictLocalWorkloadPassSQLiteSchema = `
CREATE TABLE IF NOT EXISTS ci_local_authority_state (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	schema_version INTEGER NOT NULL CHECK (schema_version = 1),
	generation TEXT NOT NULL CHECK (generation <> '' AND generation NOT GLOB '0*' AND generation NOT GLOB '*[^0-9]*' AND (length(generation) < 20 OR (length(generation) = 20 AND generation <= '18446744073709551615'))),
	state_json TEXT NOT NULL,
	state_sha256 TEXT NOT NULL,
	updated_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ci_local_workload_origins (
	run_id TEXT PRIMARY KEY,
	authority_kind TEXT NOT NULL CHECK (authority_kind = 'local-canonical'),
	local_generation TEXT NOT NULL CHECK (local_generation <> '' AND local_generation NOT GLOB '0*' AND local_generation NOT GLOB '*[^0-9]*' AND (length(local_generation) < 20 OR (length(local_generation) = 20 AND local_generation <= '18446744073709551615'))),
	source_tree_sha TEXT NOT NULL,
	catalog_digest TEXT NOT NULL,
	host_context_digest TEXT NOT NULL,
	toolchain_closure_digest TEXT NOT NULL,
	runner_semantic_policy TEXT NOT NULL,
	runner_semantic_digest TEXT NOT NULL,
	cpu_window_start_unix_ms INTEGER NOT NULL,
	cpu_window_end_unix_ms INTEGER NOT NULL CHECK (cpu_window_end_unix_ms >= cpu_window_start_unix_ms),
	cpu_sample_count INTEGER NOT NULL CHECK (cpu_sample_count >= 7),
	cpu_busy_average_percent REAL NOT NULL CHECK (cpu_busy_average_percent >= 0 AND cpu_busy_average_percent <= 100),
	available_cpu REAL NOT NULL CHECK (available_cpu > 0),
	available_memory_gib REAL NOT NULL CHECK (available_memory_gib > 0),
	status TEXT NOT NULL CHECK (status = 'passed'),
	cleanup_complete INTEGER NOT NULL CHECK (cleanup_complete = 1),
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL CHECK (completed_at_unix_ms >= started_at_unix_ms),
	projection_digest TEXT NOT NULL,
	UNIQUE (run_id, local_generation)
);

CREATE TABLE IF NOT EXISTS ci_local_workload_executions (
	run_id TEXT NOT NULL,
	workload_id TEXT NOT NULL,
	local_generation TEXT NOT NULL,
	identity_digest TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status = 'passed'),
	exit_code INTEGER NOT NULL CHECK (exit_code = 0),
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL CHECK (completed_at_unix_ms >= started_at_unix_ms),
	environment_json TEXT NOT NULL,
	execution_json TEXT NOT NULL,
	PRIMARY KEY (run_id, workload_id),
	FOREIGN KEY (run_id, local_generation) REFERENCES ci_local_workload_origins(run_id, local_generation) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ci_local_workload_executions_origin
	ON ci_local_workload_executions (run_id, local_generation);

CREATE INDEX IF NOT EXISTS idx_ci_local_workload_executions_identity
	ON ci_local_workload_executions (identity_digest, local_generation);

CREATE TABLE IF NOT EXISTS ci_local_workload_pass_evidence (
	namespace TEXT NOT NULL CHECK (namespace = 'local'),
	identity_digest TEXT NOT NULL,
	local_generation TEXT NOT NULL CHECK (local_generation <> '' AND local_generation NOT GLOB '0*' AND local_generation NOT GLOB '*[^0-9]*' AND (length(local_generation) < 20 OR (length(local_generation) = 20 AND local_generation <= '18446744073709551615'))),
	workload_id TEXT NOT NULL,
	execution_digest TEXT NOT NULL,
	input_digest TEXT NOT NULL,
	environment_digest TEXT NOT NULL,
	origin_local_run_id TEXT NOT NULL,
	origin_source_tree_sha TEXT NOT NULL,
	origin_receipt_set_sha256 TEXT NOT NULL,
	origin_execution_json TEXT NOT NULL,
	evidence_sha256 TEXT NOT NULL,
	PRIMARY KEY (identity_digest, local_generation),
	FOREIGN KEY (origin_local_run_id, local_generation) REFERENCES ci_local_workload_origins(run_id, local_generation) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ci_local_workload_pass_evidence_origin
	ON ci_local_workload_pass_evidence (origin_local_run_id, local_generation);

CREATE INDEX IF NOT EXISTS idx_ci_local_workload_pass_evidence_retention
	ON ci_local_workload_pass_evidence (namespace, local_generation, identity_digest);

CREATE INDEX IF NOT EXISTS idx_ci_local_workload_pass_evidence_replay
	ON ci_local_workload_pass_evidence (namespace, workload_id, execution_digest, local_generation);
`

const strictCheckReceiptReuseSQLiteSchema = `CREATE TABLE IF NOT EXISTS ci_check_receipts (
	run_id TEXT NOT NULL,
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	candidate_tree_sha TEXT NOT NULL,
	agent_token_digest TEXT NOT NULL,
	force INTEGER NOT NULL DEFAULT 0 CHECK (force IN (0, 1)),
	accepted_generation TEXT NOT NULL,
	accepted_snapshot_id TEXT NOT NULL,
	required_check TEXT NOT NULL,
	executed INTEGER NOT NULL CHECK (executed IN (0, 1)),
	reused INTEGER NOT NULL CHECK (reused IN (0, 1)),
	reuse_proof_sha256 TEXT NOT NULL DEFAULT '',
	passed INTEGER NOT NULL CHECK (passed IN (0, 1)),
	started_at_unix_ms INTEGER NOT NULL,
	completed_at_unix_ms INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL CHECK (duration_ms > 0),
	receipt_sha256 TEXT NOT NULL,
	PRIMARY KEY (job_id, required_check),
	UNIQUE (run_id, required_check),
	CHECK (passed = 0 OR executed = 1 OR reused = 1),
	CHECK ((reused = 0 AND reuse_proof_sha256 = '') OR (reused = 1 AND length(reuse_proof_sha256) = 71 AND reuse_proof_sha256 GLOB 'sha256:[0-9a-f]*')),
	CHECK (completed_at_unix_ms >= started_at_unix_ms),
	CHECK (completed_at_unix_ms - started_at_unix_ms = duration_ms)
)`
