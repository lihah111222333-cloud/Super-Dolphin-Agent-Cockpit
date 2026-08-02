package gate

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestDurationLedgerAcceptedGenerationRetention(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	db, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	t.Run("one generation has no row cap", func(t *testing.T) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i != 500; i++ {
			if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, platform, runner, toolchain, succeeded, duration_ms) VALUES ('1', ?, ?, 'linux/amd64', 'eci', 'go', 1, 1)`, fmt.Sprintf("workload-%d", i), fmt.Sprintf("digest-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := compactDurationLedgerAuthority(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		assertGenerationCount(t, db, "duration_samples", "1", 500)
	})

	t.Run("every retained generation has no row cap", func(t *testing.T) {
		largeStore := newTestDurationLedgerStore(t)
		if _, err := largeStore.CompareAndSwap(0, NewDurationLedger()); err != nil {
			t.Fatal(err)
		}
		largeDB, err := largeStore.openSQLiteAuthority(false)
		if err != nil {
			t.Fatal(err)
		}
		defer largeDB.Close()
		tx, err := largeDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for generation := uint64(1); generation <= 4; generation++ {
			for row := 0; row != 500; row++ {
				if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, platform, runner, toolchain, succeeded, duration_ms) VALUES (?, ?, ?, 'linux/amd64', 'eci', 'go', 1, 1)`, fmt.Sprintf("%d", generation), fmt.Sprintf("workload-%d-%d", generation, row), fmt.Sprintf("command-%d-%d", generation, row)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := compactDurationLedgerAuthority(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		assertGenerationCount(t, largeDB, "duration_samples", "1", 0)
		for generation := uint64(2); generation <= 4; generation++ {
			assertGenerationCount(t, largeDB, "duration_samples", fmt.Sprintf("%d", generation), 500)
		}
	})

	t.Run("fourth generation removes every first-generation root and cascades children", func(t *testing.T) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
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
		var children int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ci_shards WHERE job_id = 'run-1'`).Scan(&children); err != nil {
			t.Fatal(err)
		}
		if children != 0 {
			t.Fatalf("deleted run children = %d, want 0", children)
		}
		var shared int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ci_workload_catalogs WHERE catalog_digest = 'shared'`).Scan(&shared); err != nil {
			t.Fatal(err)
		}
		if shared != 1 {
			t.Fatalf("shared catalog count = %d, want 1", shared)
		}
	})

	t.Run("sparse roots share one global generation window", func(t *testing.T) {
		sparseStore := newTestDurationLedgerStore(t)
		if _, err := sparseStore.CompareAndSwap(0, NewDurationLedger()); err != nil {
			t.Fatal(err)
		}
		sparseDB, err := sparseStore.openSQLiteAuthority(false)
		if err != nil {
			t.Fatal(err)
		}
		defer sparseDB.Close()
		tx, err := sparseDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, platform, runner, toolchain, succeeded, duration_ms) VALUES ('11', 'sparse-sample', 'sparse-command', 'linux/amd64', 'eci', 'go', 1, 1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO ci_workload_catalogs (catalog_digest, catalog_version, authoritative, workload_count, created_at_unix_ms) VALUES ('sparse', 1, 1, 1, 1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO ci_runs (job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES ('sparse-run', 'commit', 'default', 'plan', 'sparse', '12', 'tree', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image', 'passed', 1, 1, 2, 1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, accepted_generation, updated_at_unix_ms) VALUES ('sparse-checkpoint', 1, '13', 1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO ci_catalog_observations (catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms) VALUES ('sparse', 'tree-14', 'commit', 'default', '14', 1)`); err != nil {
			t.Fatal(err)
		}
		if err := compactDurationLedgerAuthority(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		assertGenerationCount(t, sparseDB, "duration_samples", "11", 0)
		assertGenerationCount(t, sparseDB, "ci_runs", "12", 1)
		assertGenerationCount(t, sparseDB, "remote_ci_calibration_checkpoints", "13", 1)
		assertGenerationCount(t, sparseDB, "ci_catalog_observations", "14", 1)
	})

	t.Run("rollback restores compaction candidates", func(t *testing.T) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, platform, runner, toolchain, succeeded, duration_ms) VALUES ('5', 'rollback', 'rollback', 'linux/amd64', 'eci', 'go', 1, 1)`); err != nil {
			t.Fatal(err)
		}
		if err := compactDurationLedgerAuthority(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		assertGenerationCount(t, db, "duration_samples", "5", 0)
		assertGenerationCount(t, db, "duration_samples", "2", 1)
	})
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
	if _, err := tx.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, platform, runner, toolchain, succeeded, duration_ms) VALUES (?, ?, ?, 'linux/amd64', 'eci', 'go', 1, 1)`, g, "sample-"+g, "command-"+g); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, accepted_generation, updated_at_unix_ms) VALUES (?, 1, ?, ?)`, "checkpoint-"+g, g, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO ci_runs (job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES (?, 'commit', 'default', ?, ?, ?, ?, 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image', 'passed', 1, 1, 2, 1)`, "run-"+g, "plan-"+g, catalog, g, "tree-"+g); err != nil {
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
