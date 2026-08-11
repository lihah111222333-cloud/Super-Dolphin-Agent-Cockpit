package gate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const retentionTestAgentTokenDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestDurationLedgerSchemaInitializationReleasesSQLiteConnection(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	db, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(db, store.nowFunc, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction after schema initialization: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
}

func TestDurationLedgerAcceptedGenerationRetention(t *testing.T) {
	db := newRetentionTestSQLiteDB(t)
	defer db.Close()

	t.Run("one generation has no row cap", func(t *testing.T) { testSingleGenerationHasNoRowCap(t, db) })
	t.Run("fourth generation removes every first-generation root and cascades children", func(t *testing.T) { testRetentionCascade(t, db) })
	t.Run("fourth generation first write compacts after each accepted generation", testFourthGenerationFirstWrite)
	t.Run("rollback restores compaction candidates", func(t *testing.T) { testRetentionRollback(t, db) })
	t.Run("every retained generation has no row cap", testRetainedGenerationsHaveNoRowCap)
	t.Run("sparse roots share one global generation window", testSparseRootsShareGlobalGenerationWindow)
}

func TestDurationLedgerRetentionPlanMaterializesGenerationSetOnce(t *testing.T) {
	db := newRetentionTestSQLiteDB(t)
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	bindings := cicontract.RetentionRootBindings()
	assertRetentionGenerationQueryPlan(t, tx, bindings)

	if _, err := tx.Exec(`CREATE TEMP TABLE ` + retentionStaleGenerationsTable + ` (accepted_generation TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	assertRetentionDeletePlans(t, tx, bindings)
}

// TestDurationLedgerRetentionRejectsFutureGenerationRoot 验证 retention 不会把
// 尚未进入 accepted baseline 的伪造根纳入窗口或静默删除。
func TestDurationLedgerRetentionRejectsFutureGenerationRoot(t *testing.T) {
	db := newRetentionTestSQLiteDB(t)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms) VALUES ('5', 'future-retention', 'future-retention', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'linux/amd64', 'eci', 'go', 'normal', 'small', 2, 4, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := compactDurationLedgerAuthority(tx); err == nil || !strings.Contains(err.Error(), "was never accepted") {
		t.Fatalf("future retention root error = %v, want accepted-generation rejection", err)
	}
	var rows int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM duration_samples WHERE accepted_generation = '5'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("future retention root rows after failed compaction = %d, want 1", rows)
	}
}

// TestRetentionRootBindingsRequireRemoteRunsInsteadOfV16ProofProjection keeps
// ci_runs as a root while rejecting the v16 consumer-proof side table as a substitute.
func TestRetentionRootBindingsRequireRemoteRunsInsteadOfV16ProofProjection(t *testing.T) {
	bindings := cicontract.RetentionRootBindings()
	if err := validateRetentionRootBindings(bindings); err != nil {
		t.Fatalf("canonical retention roots rejected: %v", err)
	}

	withoutRemoteRuns := make([]cicontract.RetentionRootBinding, 0, len(bindings)-1)
	for _, binding := range bindings {
		if binding.Table != cicontract.RemoteRunsTable {
			withoutRemoteRuns = append(withoutRemoteRuns, binding)
		}
	}
	if err := validateRetentionRootBindings(withoutRemoteRuns); err == nil || !strings.Contains(err.Error(), cicontract.RemoteRunsTable) {
		t.Fatalf("retention roots without remote runs error = %v, want missing remote-runs root", err)
	}

	proofSubstitution := append([]cicontract.RetentionRootBinding(nil), bindings...)
	for index := range proofSubstitution {
		if proofSubstitution[index].Table == cicontract.RemoteRunsTable {
			proofSubstitution[index].Table = cicontract.RetainedWorkloadPassProofsTable
		}
	}
	if err := validateRetentionRootBindings(proofSubstitution); err == nil || !strings.Contains(err.Error(), cicontract.RetainedWorkloadPassProofsTable) {
		t.Fatalf("retention roots with v16 proof substitution error = %v, want rejected auxiliary proof table", err)
	}
}

func assertRetentionGenerationQueryPlan(t *testing.T, tx *sql.Tx, bindings []cicontract.RetentionRootBinding) {
	t.Helper()
	generationQuery := retentionGenerationQuery(bindings)
	if strings.Contains(generationQuery, "WITH") || strings.Contains(generationQuery, "NOT IN") {
		t.Fatalf("generation query regressed to repeated CTE/negative membership: %s", generationQuery)
	}
	for _, binding := range bindings {
		if got := strings.Count(generationQuery, binding.Table); got != 1 {
			t.Fatalf("generation query references %s %d times, want once: %s", binding.Table, got, generationQuery)
		}
	}
	plan := retentionQueryPlanDetails(t, tx, generationQuery)
	for _, binding := range bindings {
		index := retentionGenerationIndexName(t, binding)
		want := fmt.Sprintf("SCAN %s USING COVERING INDEX %s", binding.Table, index)
		found := false
		for _, detail := range plan {
			if strings.Contains(detail, "SCAN "+binding.Table) {
				found = true
				if !strings.Contains(detail, want) {
					t.Fatalf("generation plan for %s is not a narrow index scan: %v", binding.Table, plan)
				}
			}
		}
		if !found {
			t.Fatalf("generation plan omits root %s: %v", binding.Table, plan)
		}
	}
}

func assertRetentionDeletePlans(t *testing.T, tx *sql.Tx, bindings []cicontract.RetentionRootBinding) {
	t.Helper()
	for _, binding := range bindings {
		index := retentionGenerationIndexName(t, binding)
		deleteQuery := retentionDeleteQuery(binding)
		if strings.Contains(deleteQuery, "NOT IN") {
			t.Fatalf("delete query uses negative membership: %s", deleteQuery)
		}
		deletePlan := strings.Join(retentionQueryPlanDetails(t, tx, deleteQuery), "\n")
		want := fmt.Sprintf("SEARCH %s USING COVERING INDEX %s", binding.Table, index)
		if !strings.Contains(deletePlan, want) {
			t.Fatalf("delete plan for %s does not seek accepted_generation index %q: %s", binding.Table, index, deletePlan)
		}
		if strings.Contains(deletePlan, "SCAN "+binding.Table) {
			t.Fatalf("delete plan for %s regressed to a table scan: %s", binding.Table, deletePlan)
		}
		if !strings.Contains(deletePlan, "FOR IN-OPERATOR") {
			t.Fatalf("delete plan for %s does not use materialized IN set: %s", binding.Table, deletePlan)
		}
	}
}

func retentionGenerationIndexName(t *testing.T, binding cicontract.RetentionRootBinding) string {
	t.Helper()
	indexes := map[string]string{
		"duration_samples":                  "idx_duration_samples_retention",
		"duration_shard_overheads":          "idx_duration_shard_overheads_retention",
		"duration_shard_overhead_samples":   "idx_duration_shard_overhead_samples_retention",
		"ci_catalog_observations":           "idx_ci_catalog_observations_retention",
		"ci_runs":                           "idx_ci_runs_accepted_generation",
		"ci_workload_pass_evidence":         "idx_ci_workload_pass_evidence_retention",
		"remote_ci_calibration_checkpoints": "idx_remote_ci_calibration_checkpoints_retention",
	}
	index, ok := indexes[binding.Table]
	if !ok {
		t.Fatalf("no retention index expectation for root %q", binding.Table)
	}
	return index
}

const retentionDurationFixtureBatchRows = 200

// insertRetentionDurationFixtureRows 在同一事务中批量写入 retention 矩阵的样本行。
func insertRetentionDurationFixtureRows(t *testing.T, tx *sql.Tx, generation string, rowCount int, workloadPrefix, commandPrefix string) {
	t.Helper()
	for offset := 0; offset < rowCount; offset += retentionDurationFixtureBatchRows {
		batchRows := retentionDurationFixtureBatchRows
		if remaining := rowCount - offset; remaining < batchRows {
			batchRows = remaining
		}
		var query strings.Builder
		query.WriteString(`INSERT INTO duration_samples (
			accepted_generation, workload_id, command_digest, input_digest,
			platform, runner, toolchain, execution_mode, resource_class_id,
			resource_cpu, resource_memory_gib, succeeded, duration_ms
		) VALUES `)
		args := make([]any, 0, batchRows*3)
		for row := 0; row < batchRows; row++ {
			if row > 0 {
				query.WriteString(",")
			}
			query.WriteString("(?,?,?,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','linux/amd64','eci','go','normal','small',2,4,1,1)")
			index := offset + row
			args = append(args, generation, fmt.Sprintf("%s-%d", workloadPrefix, index), fmt.Sprintf("%s-%d", commandPrefix, index))
		}
		if _, err := tx.Exec(query.String(), args...); err != nil {
			t.Fatal(err)
		}
	}
}

func testSingleGenerationHasNoRowCap(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	insertRetentionDurationFixtureRows(t, tx, "1", 500, "workload", "digest")
	if err := compactDurationLedgerAuthority(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertGenerationCount(t, db, "duration_samples", "1", 500)
}

func testRetainedGenerationsHaveNoRowCap(t *testing.T) {
	t.Helper()
	db := newRetentionTestSQLiteDB(t)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for generation := uint64(1); generation <= 4; generation++ {
		insertRetentionDurationFixtureRows(t, tx, fmt.Sprintf("%d", generation), 500, fmt.Sprintf("workload-%d", generation), fmt.Sprintf("command-%d", generation))
	}
	if err := compactDurationLedgerAuthority(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertGenerationCount(t, db, "duration_samples", "1", 0)
	for generation := uint64(2); generation <= 4; generation++ {
		assertGenerationCount(t, db, "duration_samples", fmt.Sprintf("%d", generation), 500)
	}
}

func testRetentionCascade(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for generation := uint64(1); generation <= 4; generation++ {
		insertGenerationRoots(t, tx, generation, generation == 1 || generation == 4)
	}
	if err := compactDurationLedgerAuthority(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"duration_samples", "ci_catalog_observations", "ci_runs", "remote_ci_calibration_checkpoints"} {
		assertGenerationCount(t, db, table, "1", 0)
	}
	assertGenerationCount(t, db, "ci_runs", "2", 1)
	assertQueryCount(t, db, `SELECT COUNT(*) FROM ci_shards WHERE job_id = 'run-1'`, 0)
	assertQueryCount(t, db, `SELECT COUNT(*) FROM ci_workload_catalogs WHERE catalog_digest = 'shared'`, 1)
}

func testFourthGenerationFirstWrite(t *testing.T) {
	t.Helper()
	db := newRetentionTestSQLiteDBAtGeneration(t, 4)
	defer db.Close()

	for generation := uint64(1); generation <= 4; generation++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		insertGenerationRoots(t, tx, generation, generation == 1 || generation == 4)
		if err := compactDurationLedgerAuthority(tx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	assertGenerationCount(t, db, "ci_runs", "1", 0)
	for generation := uint64(2); generation <= 4; generation++ {
		assertGenerationCount(t, db, "ci_runs", fmt.Sprintf("%d", generation), 1)
	}
	assertQueryCount(t, db, `SELECT COUNT(*) FROM ci_shards WHERE job_id = 'run-1'`, 0)
	assertQueryCount(t, db, `SELECT COUNT(*) FROM ci_workload_catalogs WHERE catalog_digest = 'shared'`, 1)
}

func testSparseRootsShareGlobalGenerationWindow(t *testing.T) {
	t.Helper()
	db := newRetentionTestSQLiteDBAtGeneration(t, 12)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms) VALUES ('9', 'sparse-sample', 'sparse-command', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'linux/amd64', 'eci', 'go', 'normal', 'small', 2, 4, 1, 1)`,
		`INSERT INTO ci_workload_catalogs (catalog_digest, catalog_version, authoritative, workload_count, created_at_unix_ms) VALUES ('sparse', 1, 1, 1, 1)`,
		`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES ('sparse-run', 0, 'commit', 'default', 'plan', 'sparse', '10', 'snapshot-10', 'tree', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image', 'passed', 1, 1, 2, 1)`,
		`INSERT INTO ci_catalog_observations (catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms) VALUES ('sparse', 'tree-12', 'commit', 'default', '12', 1)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, agent_token_digest, accepted_generation, updated_at_unix_ms) VALUES ('sparse-checkpoint', 3, ?, '11', 1)`, retentionTestAgentTokenDigest); err != nil {
		t.Fatal(err)
	}
	if err := compactDurationLedgerAuthority(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertGenerationCount(t, db, "duration_samples", "9", 0)
	assertGenerationCount(t, db, "ci_runs", "10", 1)
	assertGenerationCount(t, db, "remote_ci_calibration_checkpoints", "11", 1)
	assertGenerationCount(t, db, "ci_catalog_observations", "12", 1)
}

func testRetentionRollback(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms) VALUES ('1', 'rollback', 'rollback', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'linux/amd64', 'eci', 'go', 'normal', 'small', 2, 4, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := compactDurationLedgerAuthority(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertGenerationCount(t, db, "duration_samples", "1", 0)
	assertGenerationCount(t, db, "duration_samples", "2", 1)
}

func newRetentionTestSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	return newRetentionTestSQLiteDBAtGeneration(t, 4)
}

func newRetentionTestSQLiteDBAtGeneration(t *testing.T, generation uint64) *sql.DB {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, generation)
	db, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func retentionQueryPlanDetails(t *testing.T, tx *sql.Tx, query string) []string {
	t.Helper()
	rows, err := tx.Query(`EXPLAIN QUERY PLAN ` + query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return details
}

func insertGenerationRoots(t *testing.T, tx *sql.Tx, generation uint64, shared bool) {
	t.Helper()
	g := fmt.Sprintf("%d", generation)
	catalog := fmt.Sprintf("catalog-%d", generation)
	if shared {
		catalog = "shared"
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO ci_workload_catalogs (catalog_digest, catalog_version, authoritative, workload_count, created_at_unix_ms) VALUES (?, 1, 1, 1, ?)`, catalog, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO ci_catalog_observations (catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms) VALUES (?, ?, 'commit', 'default', ?, ?)`, catalog, "tree-"+g, g, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms) VALUES (?, ?, ?, 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'linux/amd64', 'eci', 'go', 'normal', 'small', 2, 4, 1, 1)`, g, "sample-"+g, "command-"+g); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, agent_token_digest, accepted_generation, updated_at_unix_ms) VALUES (?, 3, ?, ?, ?)`, "checkpoint-"+g, retentionTestAgentTokenDigest, g, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES (?, 0, 'commit', 'default', ?, ?, ?, ?, ?, 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image', 'passed', 1, 1, 2, 1)`, "run-"+g, "plan-"+g, catalog, g, "snapshot-"+g, "tree-"+g); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO ci_shards (job_id, shard_identity, container_group_id, container_status) VALUES (?, 'shard', 'container', 'Succeeded')`, "run-"+g); err != nil {
		t.Fatal(err)
	}
}

func assertGenerationCount(t *testing.T, db *sql.DB, table, generation string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE accepted_generation = ?`, generation).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s generation %s count = %d, want %d", table, generation, got, want)
	}
}

func assertQueryCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("query count = %d, want %d", got, want)
	}
}
