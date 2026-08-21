package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCITargetBindingSchemaCard 聚合本卡的 migration、rollback、immutable API、fault 与字段差集证据。
func TestRemoteCITargetBindingSchemaCard(t *testing.T) {
	t.Run("migration", TestDurationLedgerSQLiteV22ToV23AddsImmutableTargetBindingWithoutRunMutation)
	t.Run("rollback", TestDurationLedgerSQLiteV22ToV23MigrationRollsBackConflictingShape)
	t.Run("record-readback", TestRemoteCITargetBindingRecordAndExactReadback)
	t.Run("immutable-cascade", TestRemoteCITargetBindingIsImmutableAndCascadesWithRun)
	t.Run("invalid-orphan", TestRemoteCITargetBindingRejectsInvalidAndOrphanRows)
	t.Run("sqlite-constraints", TestRemoteCITargetBindingSQLiteConstraintsRejectDirectBypass)
	t.Run("field-difference", TestRemoteCITargetBindingFieldGuardFailsWhenWorkloadSetMappingIsRemoved)
}

func TestDurationLedgerSQLiteFreshV14InitializesLocalAuthority(t *testing.T) {
	path := t.TempDir() + "/fresh.sqlite"
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("fresh authority path exists before initialization: %v", err)
	}
	store, err := NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("fresh authority user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	assertLocalAuthorityStateGeneration(t, database, "1")
	for _, table := range []string{"ci_local_authority_state", "ci_local_workload_origins", "ci_local_workload_executions", "ci_local_workload_pass_evidence"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("fresh authority table %q count = %d, want 1", table, count)
		}
	}
}

func TestDurationLedgerSQLiteV13AdditiveMigrationPreservesRemoteAuthority(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v13-additive.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV13Fixture(t, database)
	insertRemotePassFixture(t, database)
	if _, err := database.Exec(`PRAGMA user_version = 13`); err != nil {
		t.Fatal(err)
	}
	beforeSchema, err := loadDurationLedgerSQLiteSchemaObjects(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{
		"index:idx_ci_run_workload_results_retention",
		"index:idx_ci_workload_pass_evidence_environment_replay",
	} {
		if _, ok := beforeSchema[object]; ok {
			t.Fatalf("frozen v13 fixture unexpectedly contains %q", object)
		}
	}
	beforeRemoteEvidenceFK := sqliteForeignKeyFingerprint(t, database, "ci_workload_pass_evidence")
	beforeEvidenceCount, beforeEvidenceDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`)
	beforeRunCount, beforeRunDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_runs ORDER BY job_id`)

	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	assertRemoteAuthorityPreserved(t, database, beforeSchema, beforeRemoteEvidenceFK, beforeEvidenceCount, beforeEvidenceDigest, beforeRunCount, beforeRunDigest)
	assertLocalAuthorityStateGeneration(t, database, "1")
}

func TestDurationLedgerSQLiteV14ToV15AddsOnlyExecutionScopeObjects(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v14-execution-scope.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV14Fixture(t, database)
	before := snapshotDurationLedgerSQLiteSchema(t, database)

	migrateDurationLedgerSQLiteSchema(t, database)
	after := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	assertDurationLedgerSQLiteSchemaObjectsUnchanged(t, before.schema, after, "v14 object", "v15 migration")
	assertSQLiteRowsFingerprintUnchanged(t, database, `SELECT * FROM ci_runs ORDER BY job_id`, before.runCount, before.runDigest, "v14 ci_runs", "v15 migration")
	assertDurationLedgerSQLiteSchemaObjectsPresent(t, after, []string{"table:ci_remote_run_execution_scopes", "index:idx_ci_remote_run_execution_scopes_job", "index:idx_ci_remote_run_execution_scopes_generation"}, "v15 execution-scope")
}

func TestDurationLedgerSQLiteV18ToV19AddsExecutionIdentityWithoutBackfill(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v18-run-execution-identity.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV18Fixture(t, database)
	before := snapshotDurationLedgerSQLiteSchema(t, database)

	if err := migrateDurationLedgerSQLiteSchema18To19(database); err != nil {
		t.Fatal(err)
	}
	after := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	assertDurationLedgerSQLiteSchemaObjectsUnchanged(t, before.schema, after, "v18 object", "v19 migration")
	assertSQLiteRowsFingerprintUnchanged(t, database, `SELECT * FROM ci_runs ORDER BY job_id`, before.runCount, before.runDigest, "v18 ci_runs", "v19 migration")
	assertDurationLedgerSQLiteSchemaObjectsPresent(t, after, []string{"table:ci_remote_run_execution_identities"}, "v19 run execution identity")
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_remote_run_execution_identities`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("migrated execution identity rows = %d, want no backfill", count)
	}
	legacy, err := loadRemoteCIRunRow(database, "remote-fixture-job")
	if err != nil {
		t.Fatalf("load migrated v18 PASS run: %v", err)
	}
	if legacy.Platform != "" || legacy.RunnerIdentityDigest != "" || legacy.ToolchainDigest != "" || legacy.ExecutionMode != "" {
		t.Fatalf("migrated v18 run gained execution identity: %#v", legacy)
	}
}

func TestDurationLedgerSQLiteV20ToV21AddsSemanticReplayColumnsWithoutBackfill(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v20-semantic-replay-cache.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV20Fixture(t, database)
	tree := strings.Repeat("a", 40)
	algorithm := digestForWorkloadPass("legacy-input-algorithm")
	input := digestForWorkloadPass("legacy-input")
	cache := digestForWorkloadPass("legacy-cache")
	if _, err := database.Exec(`INSERT INTO ci_workload_input_replay_cache(accepted_generation, source_tree_sha, input_algorithm_digest, workload_id, input_digest, cache_sha256) VALUES('1', ?, ?, 'legacy-workload', ?, ?)`, tree, algorithm, input, cache); err != nil {
		t.Fatal(err)
	}

	migrateDurationLedgerSQLiteSchema(t, database)
	var version int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", version, durationLedgerSQLiteSchemaVersion)
	}
	var gotInput, compile, declaration, runtime string
	var fallback bool
	if err := database.QueryRow(`SELECT input_digest, compile_digest, declaration_digest, runtime_digest, runtime_fallback FROM ci_workload_input_replay_cache WHERE workload_id = 'legacy-workload'`).Scan(&gotInput, &compile, &declaration, &runtime, &fallback); err != nil {
		t.Fatal(err)
	}
	if gotInput != input || compile != "" || declaration != "" || runtime != "" || fallback {
		t.Fatalf("migrated semantic replay row = input:%q compile:%q declaration:%q runtime:%q fallback:%t", gotInput, compile, declaration, runtime, fallback)
	}
}

func TestDurationLedgerSQLiteV21ToV22AddsIndependentCleanupJournalWithoutRunMutation(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v21-remote-cleanup-journal.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV20Fixture(t, database)
	if err := migrateDurationLedgerSQLiteSchema20To21(database); err != nil {
		t.Fatal(err)
	}
	before := snapshotDurationLedgerSQLiteSchema(t, database)

	migrateDurationLedgerSQLiteSchema(t, database)
	after := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	assertDurationLedgerSQLiteSchemaObjectsUnchanged(t, before.schema, after, "v21 object", "v22 cleanup journal migration")
	assertSQLiteRowsFingerprintUnchanged(t, database, `SELECT * FROM ci_runs ORDER BY job_id`, before.runCount, before.runDigest, "v21 ci_runs", "v22 cleanup journal migration")
	assertDurationLedgerSQLiteSchemaObjectsPresent(t, after, []string{
		"table:ci_remote_cleanup_journals",
		"table:ci_remote_cleanup_targets",
		"index:idx_ci_remote_cleanup_targets_pending",
	}, "v22 remote cleanup journal")
}

// TestDurationLedgerSQLiteV22ToV23AddsImmutableTargetBindingWithoutRunMutation 锁定 additive target binding 迁移不回填或改写既有 run。
func TestDurationLedgerSQLiteV22ToV23AddsImmutableTargetBindingWithoutRunMutation(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v22-remote-target-binding.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV20Fixture(t, database)
	if err := migrateDurationLedgerSQLiteSchema20To21(database); err != nil {
		t.Fatal(err)
	}
	if err := migrateDurationLedgerSQLiteSchema21To22(database); err != nil {
		t.Fatal(err)
	}
	before := snapshotDurationLedgerSQLiteSchema(t, database)

	migrateDurationLedgerSQLiteSchema(t, database)
	after := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	assertDurationLedgerSQLiteSchemaObjectsUnchanged(t, before.schema, after, "v22 object", "v23 target binding migration")
	assertSQLiteRowsFingerprintUnchanged(t, database, `SELECT * FROM ci_runs ORDER BY job_id`, before.runCount, before.runDigest, "v22 ci_runs", "v23 target binding migration")
	assertDurationLedgerSQLiteSchemaObjectsPresent(t, after, []string{
		"table:ci_remote_run_target_bindings",
		"index:idx_ci_remote_run_target_bindings_profile",
		"trigger:trg_ci_remote_run_target_bindings_immutable",
	}, "v23 remote target binding")
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_remote_run_target_bindings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("v23 target binding migration backfilled %d rows", count)
	}
}

// TestDurationLedgerSQLiteV22ToV23MigrationRollsBackConflictingShape 锁定冲突 DDL 不得部分推进版本或索引。
func TestDurationLedgerSQLiteV22ToV23MigrationRollsBackConflictingShape(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v22-target-binding-conflict.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV20Fixture(t, database)
	if err := migrateDurationLedgerSQLiteSchema20To21(database); err != nil {
		t.Fatal(err)
	}
	if err := migrateDurationLedgerSQLiteSchema21To22(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE ci_remote_run_target_bindings (job_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := migrateDurationLedgerSQLiteSchema22To23(database); err == nil {
		t.Fatal("conflicting target binding migration unexpectedly succeeded")
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 22 {
		t.Fatalf("failed migration user_version = %d, want 22", got)
	}
	objects := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	if _, exists := objects["index:idx_ci_remote_run_target_bindings_profile"]; exists {
		t.Fatal("failed migration left target binding index behind")
	}
	if _, exists := objects["trigger:trg_ci_remote_run_target_bindings_immutable"]; exists {
		t.Fatal("failed migration left target binding trigger behind")
	}
}

// TestRemoteCITargetBindingRecordAndExactReadback 锁定 job 一对一 binding 的完整写入和精确回读。
func TestRemoteCITargetBindingRecordAndExactReadback(t *testing.T) {
	store, database := newRemoteCITargetBindingStore(t)
	insertRemoteCITargetBindingParent(t, database, "target-binding-job")
	binding := remoteCITargetBindingFixture("target-binding-job")

	got, err := store.RecordRemoteCITargetBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, binding) {
		t.Fatalf("record readback = %#v, want %#v", got, binding)
	}
	loaded, err := store.LoadRemoteCITargetBinding(binding.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, binding) {
		t.Fatalf("loaded binding = %#v, want %#v", loaded, binding)
	}
}

// TestRemoteCITargetBindingIsImmutableAndCascadesWithRun 锁定重复 record、SQL UPDATE 与 orphan 生命周期。
func TestRemoteCITargetBindingIsImmutableAndCascadesWithRun(t *testing.T) {
	store, database := newRemoteCITargetBindingStore(t)
	insertRemoteCITargetBindingParent(t, database, "immutable-job")
	binding := remoteCITargetBindingFixture("immutable-job")
	if _, err := store.RecordRemoteCITargetBinding(binding); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordRemoteCITargetBinding(binding); err == nil {
		t.Fatal("duplicate target binding record unexpectedly succeeded")
	}
	if _, err := database.Exec(`UPDATE ci_remote_run_target_bindings SET mount_path = 'other-target' WHERE job_id = ?`, binding.JobID); err == nil {
		t.Fatal("target binding SQL update unexpectedly succeeded")
	}
	if _, err := database.Exec(`DELETE FROM ci_runs WHERE job_id = ?`, binding.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRemoteCITargetBinding(binding.JobID); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("load after parent cascade error = %v", err)
	}
}

// TestRemoteCITargetBindingRejectsInvalidAndOrphanRows 锁定生产字段 validation 与 FK fail-fast。
func TestRemoteCITargetBindingRejectsInvalidAndOrphanRows(t *testing.T) {
	store, database := newRemoteCITargetBindingStore(t)
	insertRemoteCITargetBindingParent(t, database, "validation-job")
	valid := remoteCITargetBindingFixture("validation-job")
	for name, mutate := range map[string]func(*RemoteCITargetBinding){
		"repository": func(value *RemoteCITargetBinding) { value.RepositoryID = "" },
		"implicit repository": func(value *RemoteCITargetBinding) {
			value.RepositoryID = "default"
		},
		"object format":   func(value *RemoteCITargetBinding) { value.ObjectFormat = "md5" },
		"commit":          func(value *RemoteCITargetBinding) { value.CommitSHA = strings.Repeat("0", 40) },
		"mount":           func(value *RemoteCITargetBinding) { value.MountPath = "../target" },
		"manifest path":   func(value *RemoteCITargetBinding) { value.ManifestPath = "/absolute/workloads.json" },
		"manifest digest": func(value *RemoteCITargetBinding) { value.ManifestDigest = "sha256:UPPER" },
		"zero digest":     func(value *RemoteCITargetBinding) { value.ManifestDigest = "sha256:" + strings.Repeat("0", 64) },
		"digest substitution": func(value *RemoteCITargetBinding) {
			value.ProfileWorkloadSetDigest = value.ManifestDigest
		},
		"profile":      func(value *RemoteCITargetBinding) { value.Profile = "unknown" },
		"workload set": func(value *RemoteCITargetBinding) { value.ProfileWorkloadSetDigest = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.JobID += "-" + strings.ReplaceAll(name, " ", "-")
			insertRemoteCITargetBindingParent(t, database, candidate.JobID)
			mutate(&candidate)
			if _, err := store.RecordRemoteCITargetBinding(candidate); err == nil {
				t.Fatal("invalid target binding unexpectedly succeeded")
			}
		})
	}
	orphan := valid
	orphan.JobID = "missing-parent"
	if _, err := store.RecordRemoteCITargetBinding(orphan); err == nil {
		t.Fatal("orphan target binding unexpectedly succeeded")
	}
}

// TestRemoteCITargetBindingSQLiteConstraintsRejectDirectBypass 锁定 FK、path、zero digest 与 digest 分离 CHECK。
func TestRemoteCITargetBindingSQLiteConstraintsRejectDirectBypass(t *testing.T) {
	_, database := newRemoteCITargetBindingStore(t)
	base := remoteCITargetBindingFixture("direct-constraint")
	for name, mutate := range map[string]func(*RemoteCITargetBinding){
		"backslash path": func(value *RemoteCITargetBinding) { value.MountPath = `target\nested` },
		"implicit path":  func(value *RemoteCITargetBinding) { value.MountPath = "default" },
		"zero digest": func(value *RemoteCITargetBinding) {
			value.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
		},
		"digest substitution": func(value *RemoteCITargetBinding) {
			value.ProfileWorkloadSetDigest = value.ManifestDigest
		},
	} {
		t.Run(name, func(t *testing.T) {
			binding := base
			binding.JobID += "-" + strings.ReplaceAll(name, " ", "-")
			insertRemoteCITargetBindingParent(t, database, binding.JobID)
			mutate(&binding)
			if err := insertRemoteCITargetBindingDirect(database, binding); err == nil {
				t.Fatal("direct invalid target binding insert unexpectedly succeeded")
			}
		})
	}
	orphan := base
	orphan.JobID = "direct-orphan"
	if err := insertRemoteCITargetBindingDirect(database, orphan); err == nil {
		t.Fatal("direct orphan target binding insert unexpectedly succeeded")
	}
}

func insertRemoteCITargetBindingDirect(database *sql.DB, binding RemoteCITargetBinding) error {
	query := fmt.Sprintf(`INSERT INTO %s (job_id, repository_id, object_format, commit_sha, tree_sha, mount_path, manifest_path, manifest_sha256, manifest_schema_version, manifest_abi_digest, manifest_digest, profile, profile_workload_set_digest) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, cicontract.RemoteRunTargetBindingsTable)
	_, err := database.Exec(query, binding.JobID, binding.RepositoryID, binding.ObjectFormat, binding.CommitSHA, binding.TreeSHA, binding.MountPath, binding.ManifestPath, binding.ManifestSHA256, binding.ManifestSchemaVersion, binding.ManifestABIDigest, binding.ManifestDigest, binding.Profile, binding.ProfileWorkloadSetDigest)
	return err
}

func newRemoteCITargetBindingStore(t *testing.T) (*DurationLedgerStore, *sql.DB) {
	t.Helper()
	store, err := NewDurationLedgerStore(t.TempDir() + "/target-binding.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, database
}

func insertRemoteCITargetBindingParent(t *testing.T, database *sql.DB, jobID string) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO ci_runs(job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete) VALUES(?, 'git-pre-push', 'push', 'plan', 'catalog', '1', 'snapshot', ?, 'image', 'failed', 0, 1, 2, 1)`, jobID, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
}

func remoteCITargetBindingFixture(jobID string) RemoteCITargetBinding {
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	return RemoteCITargetBinding{
		JobID:                    jobID,
		RepositoryID:             "target-repository",
		ObjectFormat:             GitObjectFormatSHA1,
		CommitSHA:                strings.Repeat("a", 40),
		TreeSHA:                  strings.Repeat("b", 40),
		MountPath:                "target",
		ManifestPath:             ".super-dolphin/workloads.json",
		ManifestSHA256:           digest("c"),
		ManifestSchemaVersion:    cicontract.RemoteCITargetBindingManifestSchemaVersion,
		ManifestABIDigest:        digest("d"),
		ManifestDigest:           digest("e"),
		Profile:                  ProfilePush,
		ProfileWorkloadSetDigest: digest("f"),
	}
}

func TestRemoteCleanupJournalPersistsIntentAndRejectsProviderIDDrift(t *testing.T) {
	store, err := NewDurationLedgerStore(t.TempDir() + "/cleanup.sqlite")
	mustRemoteCleanupJournalCall(t, err)
	database, err := store.openSQLiteAuthority(true)
	mustRemoteCleanupJournalCall(t, err)
	mustRemoteCleanupJournalCall(t, database.Close())
	const jobID = "job-cleanup-fixture"
	const prefix = "source/job-cleanup-fixture/"
	const groupName = "sdci-cleanup-fixture-s00"
	mustRemoteCleanupJournalCall(t, store.BeginRemoteCleanupJournal(jobID, prefix))
	mustRemoteCleanupJournalCall(t, store.BeginRemoteCleanupJournal(jobID, prefix))
	mustRemoteCleanupJournalCall(t, store.RecordRemoteCleanupContainerGroupIntent(jobID, groupName))
	mustRemoteCleanupJournalCall(t, store.SetRemoteCleanupContainerGroupProviderID(jobID, groupName, "eci-cleanup-fixture"))
	if err := store.SetRemoteCleanupContainerGroupProviderID(jobID, groupName, "eci-other"); err == nil {
		t.Fatal("provider ID drift unexpectedly succeeded")
	}
	journal, err := store.LoadRemoteCleanupJournal(jobID)
	mustRemoteCleanupJournalCall(t, err)
	if len(journal.Targets) != 2 || journal.Targets[0].Kind != RemoteCleanupTargetECIContainerGroup || journal.Targets[0].ProviderID != "eci-cleanup-fixture" || journal.Targets[1].Kind != RemoteCleanupTargetOSSPrefix || journal.Targets[1].Key != prefix {
		t.Fatalf("cleanup journal = %#v", journal)
	}
}

func mustRemoteCleanupJournalCall(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type durationLedgerSQLiteSchemaSnapshot struct {
	schema    map[string]string
	runCount  int
	runDigest string
}

func createDurationLedgerSQLiteV14Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range durationLedgerSQLiteSchemaStatementsV14() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v14 schema: %v", err)
		}
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := initializeLocalAuthorityStateOnConnection(connection, func() time.Time {
		return time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	}); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	insertRemotePassFixture(t, database)
	if _, err := database.Exec(`PRAGMA user_version = 14`); err != nil {
		t.Fatal(err)
	}
}

func createDurationLedgerSQLiteV18Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	statements := append(durationLedgerSQLiteSchemaStatementsV14(), durationLedgerRemoteCIExecutionScopeSchemaStatements()...)
	statements = append(statements, durationLedgerRetainedWorkloadPassProofSchemaStatements()...)
	statements = append(statements, durationLedgerSourceReplayIndexSchemaStatements()...)
	statements = append(statements, durationLedgerWorkloadInputReplayCacheSchemaStatements()...)
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v18 schema: %v", err)
		}
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := initializeLocalAuthorityStateOnConnection(connection, time.Now); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	insertRemotePassFixture(t, database)
	if _, err := database.Exec(`PRAGMA user_version = 18`); err != nil {
		t.Fatal(err)
	}
}

func createDurationLedgerSQLiteV20Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	statements := append(durationLedgerSQLiteSchemaStatementsV14(), durationLedgerRemoteCIExecutionScopeSchemaStatements()...)
	statements = append(statements, durationLedgerRetainedWorkloadPassProofSchemaStatements()...)
	statements = append(statements, durationLedgerSourceReplayIndexSchemaStatements()...)
	statements = append(statements, durationLedgerWorkloadInputReplayCacheSchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCIRunExecutionIdentitySchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCIPlanningReceiptSchemaStatements()...)
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v20 schema: %v", err)
		}
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := initializeLocalAuthorityStateOnConnection(connection, time.Now); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	insertRemotePassFixture(t, database)
	if _, err := database.Exec(`PRAGMA user_version = 20`); err != nil {
		t.Fatal(err)
	}
}

func snapshotDurationLedgerSQLiteSchema(t *testing.T, database *sql.DB) durationLedgerSQLiteSchemaSnapshot {
	t.Helper()
	schema := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	runCount, runDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_runs ORDER BY job_id`)
	return durationLedgerSQLiteSchemaSnapshot{schema: schema, runCount: runCount, runDigest: runDigest}
}

func snapshotDurationLedgerSQLiteSchemaObjects(t *testing.T, database *sql.DB) map[string]string {
	t.Helper()
	schema, err := loadDurationLedgerSQLiteSchemaObjects(database)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func migrateDurationLedgerSQLiteSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
}

func assertDurationLedgerSQLiteSchemaObjectsUnchanged(t *testing.T, before, after map[string]string, objectDescription, migration string) {
	t.Helper()
	for object, ddl := range before {
		if got := after[object]; got != ddl {
			t.Fatalf("%s %s changed during %s: before=%q after=%q", objectDescription, object, migration, ddl, got)
		}
	}
}

func assertSQLiteRowsFingerprintUnchanged(t *testing.T, database *sql.DB, query string, wantCount int, wantDigest, rowDescription, migration string) {
	t.Helper()
	if got, digest := sqliteRowsFingerprint(t, database, query); got != wantCount || digest != wantDigest {
		t.Fatalf("%s changed during %s: before=(%d,%s) after=(%d,%s)", rowDescription, migration, wantCount, wantDigest, got, digest)
	}
}

func assertDurationLedgerSQLiteSchemaObjectsPresent(t *testing.T, schema map[string]string, objects []string, description string) {
	t.Helper()
	for _, object := range objects {
		if got := schema[object]; got == "" {
			t.Fatalf("%s object %q is missing", description, object)
		}
	}
}

func assertRemoteAuthorityPreserved(t *testing.T, database *sql.DB, beforeSchema map[string]string, beforeForeignKey string, beforeEvidenceCount int, beforeEvidenceDigest string, beforeRunCount int, beforeRunDigest string) {
	t.Helper()
	afterSchema, err := loadDurationLedgerSQLiteSchemaObjects(database)
	if err != nil {
		t.Fatal(err)
	}
	for object, ddl := range beforeSchema {
		if got := afterSchema[object]; got != ddl {
			t.Fatalf("remote schema object %s changed during additive migration: before=%q after=%q", object, ddl, got)
		}
	}
	if got := sqliteForeignKeyFingerprint(t, database, "ci_workload_pass_evidence"); got != beforeForeignKey {
		t.Fatalf("remote PASS evidence foreign keys changed during additive migration: before=%q after=%q", beforeForeignKey, got)
	}
	if got, digest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`); got != beforeEvidenceCount || digest != beforeEvidenceDigest {
		t.Fatalf("remote PASS evidence changed during additive migration: before=(%d,%s) after=(%d,%s)", beforeEvidenceCount, beforeEvidenceDigest, got, digest)
	}
	if got, digest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_runs ORDER BY job_id`); got != beforeRunCount || digest != beforeRunDigest {
		t.Fatalf("remote ci_runs changed during additive migration: before=(%d,%s) after=(%d,%s)", beforeRunCount, beforeRunDigest, got, digest)
	}
}

func TestDurationLedgerSQLiteV14MissingLocalStateFailsFastWithoutRebuild(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "missing-local-state.sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM ci_local_authority_state`); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil || !strings.Contains(err.Error(), "local authority state is missing") {
		t.Fatalf("missing local state error = %v, want fail-fast", err)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("missing local state refusal changed sqlite_master")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_local_authority_state`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing local state refusal rebuilt state row: count=%d", count)
	}
}

func TestDurationLedgerSQLiteUnknownSchemaVersionFailsFastWithoutRebuild(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "unknown-version.sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown schema version error = %v, want fail-fast", err)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("unknown schema version refusal changed sqlite_master")
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 99 {
		t.Fatalf("unknown schema version refusal changed user_version to %d", got)
	}
}

func createDurationLedgerSQLiteV13Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range durationLedgerSQLiteLegacySchemaStatementsV13() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v13 schema: %v", err)
		}
	}
}

func insertRemotePassFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO ci_runs(job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text) VALUES ('remote-fixture-job', 0, 'test', 'medium', 'sha256:plan', 'sha256:catalog', '1', 'snapshot-fixture', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '', '', 'eci-fixture', 'passed', 1, 1700000000000, 1700000001000, 1, '')`); err != nil {
		t.Fatalf("insert remote run fixture: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO ci_run_agent_identities(job_id, agent_token_digest, started_at_unix_ms) VALUES ('remote-fixture-job', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1700000000000)`); err != nil {
		t.Fatalf("insert remote run agent fixture: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_evidence(identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES ('sha256:identity-fixture', '1', 'backend:fixture', 'sha256:execution-fixture', 'sha256:input-fixture', 'sha256:environment-fixture', 'remote-fixture-job', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:receipt-fixture', '{}', 'sha256:evidence-fixture')`); err != nil {
		t.Fatalf("insert remote evidence fixture: %v", err)
	}
}

func assertLocalAuthorityStateGeneration(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow(`SELECT generation FROM ci_local_authority_state WHERE singleton = 1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("local authority generation = %q, want %q", got, want)
	}
}

func sqliteRowsFingerprint(t *testing.T, database *sql.DB, query string) (int, string) {
	t.Helper()
	rows, err := database.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for index, value := range values {
			fmt.Fprintf(hash, "%d=%s\x00", index, sqliteFingerprintValue(value))
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count, "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sqliteForeignKeyFingerprint(t *testing.T, database *sql.DB, table string) string {
	t.Helper()
	rows, err := database.Query(`PRAGMA foreign_key_list('` + table + `')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for index, value := range values {
			fmt.Fprintf(hash, "%d=%s\x00", index, sqliteFingerprintValue(value))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sqliteFingerprintValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case []byte:
		return string(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
