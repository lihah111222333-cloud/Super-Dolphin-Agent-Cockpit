package gate

// durationLedgerRemoteCIExecutionScopeSchemaStatements is the additive v15
// execution-scope side table. Existing remote rows and ci_runs are deliberately
// not altered or rewritten.
func durationLedgerRemoteCIExecutionScopeSchemaStatements() []string {
	return []string{`
CREATE TABLE IF NOT EXISTS ci_remote_run_execution_scopes (
	job_id TEXT NOT NULL,
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	scope_json TEXT NOT NULL CHECK (length(trim(scope_json)) > 0),
	scope_digest TEXT NOT NULL CHECK (length(trim(scope_digest)) > 0),
	scope_count INTEGER NOT NULL CHECK (scope_count > 0),
	PRIMARY KEY (job_id, accepted_generation),
	FOREIGN KEY (job_id) REFERENCES ci_runs(job_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ci_remote_run_execution_scopes_job
	ON ci_remote_run_execution_scopes (job_id);

CREATE INDEX IF NOT EXISTS idx_ci_remote_run_execution_scopes_generation
	ON ci_remote_run_execution_scopes (accepted_generation, job_id);
`}
}

// durationLedgerRetainedWorkloadPassProofSchemaStatements is the additive v16
// projection. Each row belongs to a retained consumer run, not to its stale
// direct origin, so strict three-generation compaction can delete the origin.
func durationLedgerRetainedWorkloadPassProofSchemaStatements() []string {
	return []string{`
CREATE TABLE IF NOT EXISTS ci_retained_workload_pass_proofs (
	consumer_job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	workload_id TEXT NOT NULL,
	identity_digest TEXT NOT NULL,
	origin_job_id TEXT NOT NULL,
	origin_accepted_generation TEXT NOT NULL CHECK (origin_accepted_generation <> '' AND origin_accepted_generation NOT GLOB '0*' AND origin_accepted_generation NOT GLOB '*[^0-9]*'),
	origin_source_tree_sha TEXT NOT NULL,
	origin_receipt_set_sha256 TEXT NOT NULL,
	origin_execution_json TEXT NOT NULL,
	evidence_sha256 TEXT NOT NULL,
	PRIMARY KEY (consumer_job_id, workload_id)
);

CREATE INDEX IF NOT EXISTS idx_ci_retained_workload_pass_proofs_lookup
	ON ci_retained_workload_pass_proofs (identity_digest, consumer_job_id);

CREATE INDEX IF NOT EXISTS idx_ci_run_workload_results_retention
	ON ci_run_workload_results (disposition, job_id, origin_job_id, origin_accepted_generation, identity_digest, evidence_sha256);
`}
}
