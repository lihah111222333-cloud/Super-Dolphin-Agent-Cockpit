package gate

const durationLedgerCompileTimingTableSchema = `
CREATE TABLE IF NOT EXISTS ci_compile_timing_observations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	package_target TEXT NOT NULL CHECK (length(trim(package_target)) > 0),
	semantic_key TEXT NOT NULL CHECK (length(trim(semantic_key)) > 0),
	platform TEXT NOT NULL CHECK (length(trim(platform)) > 0),
	runner_identity_digest TEXT NOT NULL CHECK (length(trim(runner_identity_digest)) > 0),
	toolchain_digest TEXT NOT NULL CHECK (length(trim(toolchain_digest)) > 0),
	execution_mode TEXT NOT NULL CHECK (execution_mode IN ('normal', 'calibration')),
	resource_class_id TEXT NOT NULL CHECK (length(trim(resource_class_id)) > 0),
	resource_cpu REAL NOT NULL CHECK (resource_cpu > 0),
	resource_memory_gib REAL NOT NULL CHECK (resource_memory_gib > 0),
	duration_ms INTEGER NOT NULL CHECK (duration_ms > 0),
	started_at_unix_ms INTEGER NOT NULL CHECK (started_at_unix_ms > 0),
	completed_at_unix_ms INTEGER NOT NULL CHECK (completed_at_unix_ms > started_at_unix_ms),
	measurement TEXT NOT NULL CHECK (measurement = 'measured'),
	aggregation TEXT NOT NULL CHECK (aggregation = 'raw'),
	UNIQUE (job_id, package_target, semantic_key, platform, runner_identity_digest, toolchain_digest,
		execution_mode, resource_class_id, resource_cpu, resource_memory_gib, started_at_unix_ms, completed_at_unix_ms)
);`

const durationLedgerCompileTimingLookupIndexSchema = `
CREATE INDEX IF NOT EXISTS idx_ci_compile_timing_lookup
	ON ci_compile_timing_observations (
		package_target, semantic_key, platform, runner_identity_digest,
		toolchain_digest, execution_mode, resource_class_id, resource_cpu,
		resource_memory_gib, job_id
	);`

const durationLedgerCompileTimingJobIndexSchema = `
CREATE INDEX IF NOT EXISTS idx_ci_compile_timing_job
	ON ci_compile_timing_observations (job_id, id);`

func durationLedgerCompileTimingSchemaStatements() []string {
	return []string{
		durationLedgerCompileTimingTableSchema,
		durationLedgerCompileTimingLookupIndexSchema,
		durationLedgerCompileTimingJobIndexSchema,
	}
}
