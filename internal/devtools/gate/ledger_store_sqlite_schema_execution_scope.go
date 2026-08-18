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

// durationLedgerSourceReplayIndexSchemaStatements 是 additive v17 查询分区。
// 它只新增 source replay 复合索引，不改写或复制任何 authority/proof 行。
func durationLedgerSourceReplayIndexSchemaStatements() []string {
	return []string{`
CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_evidence_source_replay
	ON ci_workload_pass_evidence (workload_id, execution_digest, environment_digest, accepted_generation);

CREATE INDEX IF NOT EXISTS idx_ci_retained_workload_pass_proofs_source_replay
	ON ci_retained_workload_pass_proofs (workload_id, consumer_job_id);
`}
}

// durationLedgerWorkloadInputReplayCacheSchemaStatements 是 additive v18
// immutable source-tree 输入索引；它不构成 PASS evidence 或 retention root。
func durationLedgerWorkloadInputReplayCacheSchemaStatements() []string {
	return []string{`
CREATE TABLE IF NOT EXISTS ci_workload_input_replay_cache (
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	source_tree_sha TEXT NOT NULL CHECK ((length(source_tree_sha) = 40 OR length(source_tree_sha) = 64) AND source_tree_sha = lower(source_tree_sha) AND source_tree_sha NOT GLOB '*[^0-9a-f]*'),
	input_algorithm_digest TEXT NOT NULL CHECK (length(input_algorithm_digest) = 71 AND substr(input_algorithm_digest, 1, 7) = 'sha256:' AND substr(input_algorithm_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	workload_id TEXT NOT NULL CHECK (length(trim(workload_id)) > 0 AND workload_id = trim(workload_id)),
	input_digest TEXT NOT NULL CHECK (length(input_digest) = 71 AND substr(input_digest, 1, 7) = 'sha256:' AND substr(input_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	cache_sha256 TEXT NOT NULL CHECK (length(cache_sha256) = 71 AND substr(cache_sha256, 1, 7) = 'sha256:' AND substr(cache_sha256, 8) NOT GLOB '*[^0-9a-f]*'),
	PRIMARY KEY (accepted_generation, source_tree_sha, input_algorithm_digest, workload_id)
) WITHOUT ROWID;
`}
}
