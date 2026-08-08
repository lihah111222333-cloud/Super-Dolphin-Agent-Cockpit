package gate

import (
	"database/sql"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type generationOneHistoryFixture func(*testing.T, *sql.DB)

// insertGenerationOneAuthorityHistoryForTest 通过表驱动 fixture 污染每个首代权威表。
func insertGenerationOneAuthorityHistoryForTest(t *testing.T, database *sql.DB, table string) {
	t.Helper()
	fixture, ok := generationOneAuthorityHistoryFixtures()[table]
	if !ok {
		t.Fatalf("test does not know generation-one authority table %q", table)
	}
	fixture(t, database)
}

func generationOneAuthorityHistoryFixtures() map[string]generationOneHistoryFixture {
	return map[string]generationOneHistoryFixture{
		cicontract.DurationCalibrationsTable:         insertGenerationOneDurationCalibration,
		cicontract.DurationSamplesTable:              insertGenerationOneDurationSample,
		cicontract.DurationShardOverheadsTable:       insertGenerationOneShardOverhead,
		cicontract.DurationShardOverheadSamplesTable: insertGenerationOneShardOverheadSample,
		cicontract.CatalogObservationsTable:          insertGenerationOneCatalogObservation,
		cicontract.RemoteRunsTable:                   insertGenerationOneRun,
		cicontract.WorkloadPassEvidenceTable:         insertGenerationOnePassEvidence,
		cicontract.CalibrationCheckpointsTable:       insertGenerationOneCalibrationCheckpoint,
		cicontract.WorkloadCatalogsTable:             insertGenerationOneCatalog,
		cicontract.LiveTimingWarningsTable:           insertGenerationOneLiveWarning,
	}
}

func insertGenerationOneDurationCalibration(t *testing.T, database *sql.DB) {
	execGenerationOneHistoryFixture(t, database, `INSERT INTO duration_calibrations (singleton, schema_version, commit_sha, tree_sha, platform, runner, toolchain, commit_entrypoint, push_entrypoint, release_entrypoint, commit_catalog_digest, push_catalog_digest, release_catalog_digest, calibration_resource_class_id, calibration_resource_cpu, calibration_resource_memory_gib, workload_count, race_package_count, accepted_snapshot_id, completed_at_unix_ms) VALUES (1, 1, 'commit', 'tree', 'linux/amd64', 'eci', 'go', 'commit', 'push', 'release', 'catalog-commit', 'catalog-push', 'catalog-release', 'calibration', 4, 8, 1, 1, 'snapshot', 1)`)
}

func insertGenerationOneDurationSample(t *testing.T, database *sql.DB) {
	execGenerationOneHistoryFixture(t, database, `INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms) VALUES ('1', 'orphan-sample', 'orphan-command', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'linux/amd64', 'eci', 'go', 'normal', 'small', 2, 4, 1, 1)`)
}

func insertGenerationOneShardOverhead(t *testing.T, database *sql.DB) {
	execGenerationOneHistoryFixture(t, database, `INSERT INTO duration_shard_overheads (accepted_generation, schema_version, policy_version, platform, runner, toolchain, calibration_resource_class_id, calibration_resource_cpu, calibration_resource_memory_gib, p95_ms, sample_count, provenance_digest, accepted_snapshot_id) VALUES ('1', 2, 'accounted-interval-union-nearest-rank-p95-v2', 'linux/amd64', 'eci', 'go', 'calibration', 4, 8, 1, 1, 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'snapshot')`)
}

func insertGenerationOneShardOverheadSample(t *testing.T, database *sql.DB) {
	insertGenerationOneRunForTest(t, database, "orphan-overhead-run")
	execGenerationOneHistoryFixture(t, database, `INSERT INTO duration_shard_overhead_samples (accepted_generation, provenance_digest, job_id, shard_identity, total_started_at_unix_ms, total_completed_at_unix_ms, workload_envelope_start_unix_ms, workload_envelope_end_unix_ms, accounted_duration_ms, accounted_interval_count, overhead_ms) VALUES ('1', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'orphan-overhead-run', 'orphan-shard', 1, 3, 1, 2, 1, 1, 1)`)
}

func insertGenerationOneCatalogObservation(t *testing.T, database *sql.DB) {
	insertGenerationOneCatalog(t, database)
	execGenerationOneHistoryFixture(t, database, `INSERT INTO ci_catalog_observations (catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms) VALUES ('orphan-catalog', 'tree', 'commit', 'default', '1', 1)`)
}

func insertGenerationOneRun(t *testing.T, database *sql.DB) {
	insertGenerationOneRunForTest(t, database, "orphan-run")
}

func insertGenerationOnePassEvidence(t *testing.T, database *sql.DB) {
	insertGenerationOneRunForTest(t, database, "orphan-pass-run")
	execGenerationOneHistoryFixture(t, database, `INSERT INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES ('identity', '1', 'workload', 'execution', 'input', 'environment', 'orphan-pass-run', 'tree', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '{}', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`)
}

func insertGenerationOneCalibrationCheckpoint(t *testing.T, database *sql.DB) {
	execGenerationOneHistoryFixture(t, database, `INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, agent_token_digest, accepted_generation, updated_at_unix_ms) VALUES ('orphan-checkpoint', 3, 'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', '1', 1)`)
}

func insertGenerationOneCatalog(t *testing.T, database *sql.DB) {
	execGenerationOneHistoryFixture(t, database, `INSERT INTO ci_workload_catalogs (catalog_digest, catalog_version, authoritative, workload_count, created_at_unix_ms) VALUES ('orphan-catalog', 1, 1, 1, 1)`)
}

func insertGenerationOneLiveWarning(t *testing.T, database *sql.DB) {
	execGenerationOneHistoryFixture(t, database, `INSERT INTO ci_live_timing_warnings (job_id, agent_token_digest, accepted_generation, scope, shard_identity, workload_id, evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms, evidence_duration_ms, target_ms, warning_text) VALUES ('orphan-live-warning', 'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', '1', 'shard', 'orphan-shard', '', 'running', 'warn_and_continue', 1, 100001, 100000, 100000, 'target exceeded')`)
}

func execGenerationOneHistoryFixture(t *testing.T, database *sql.DB, statement string) {
	t.Helper()
	if _, err := database.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func insertGenerationOneRunForTest(t *testing.T, database *sql.DB, jobID string) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES (?, 0, 'commit', 'default', 'plan', 'catalog', '1', 'snapshot', 'tree', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image', 'passed', 1, 1, 2, 1)`, jobID)
	if err != nil {
		t.Fatal(err)
	}
}
