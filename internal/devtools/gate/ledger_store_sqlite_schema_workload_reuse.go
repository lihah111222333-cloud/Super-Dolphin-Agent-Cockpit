package gate

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

const strictCheckReceiptReuseSQLiteSchema = `CREATE TABLE IF NOT EXISTS ci_check_receipts (
	run_id TEXT NOT NULL,
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	candidate_tree_sha TEXT NOT NULL,
	agent_token_digest TEXT NOT NULL,
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
