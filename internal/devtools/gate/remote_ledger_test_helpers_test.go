package gate

import (
	"database/sql"
	"testing"
)

func insertRemoteRunForTest(t *testing.T, database *sql.DB, jobID string) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES (?, 0, 'commit', 'default', 'plan', 'catalog', '1', 'snapshot', 'tree', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image', 'passed', 1, 1, 2, 1)`, jobID)
	if err != nil {
		t.Fatal(err)
	}
}
