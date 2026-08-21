package gate

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type durationLedgerSQLiteSchemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type durationLedgerSQLiteSchemaValidator struct {
	expectedSchema      func() (map[string]string, error)
	initializeAuthority func(*sql.DB, func() time.Time, *durationLedgerSQLiteSchemaValidator) error
}

func newDurationLedgerSQLiteSchemaValidator() *durationLedgerSQLiteSchemaValidator {
	return &durationLedgerSQLiteSchemaValidator{
		expectedSchema:      sync.OnceValues(buildDurationLedgerSQLiteReferenceSchema),
		initializeAuthority: initializeDurationLedgerSQLiteCurrentSchema,
	}
}

// durationLedgerSQLiteCurrentSchemaStatements returns the sole physical schema
// definition used by both empty-authority initialization and exact comparison.
func durationLedgerSQLiteCurrentSchemaStatements() []string {
	statements := durationLedgerSQLiteSchemaStatementsV14()
	statements = append(statements, durationLedgerRemoteCIExecutionScopeSchemaStatements()...)
	statements = append(statements, durationLedgerRetainedWorkloadPassProofSchemaStatements()...)
	statements = append(statements, durationLedgerSourceReplayIndexSchemaStatements()...)
	statements = append(statements, durationLedgerWorkloadInputReplayCacheSchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCIRunExecutionIdentitySchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCIPlanningReceiptSchemaStatements()...)
	statements = append(statements, durationLedgerWorkloadSemanticReplayCacheSchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCleanupJournalSchemaStatements()...)
	return append(statements, durationLedgerRemoteCITargetBindingSchemaStatements()...)
}

func durationLedgerRemoteCITargetBindingSchemaStatements() []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE CHECK (length(job_id) > 0 AND trim(job_id) = job_id),
			repository_id TEXT NOT NULL CHECK (length(repository_id) BETWEEN 1 AND 128 AND trim(repository_id) = repository_id AND repository_id NOT IN ('default', 'cwd', 'HEAD')),
			object_format TEXT NOT NULL CHECK (object_format IN ('%s', '%s')),
			commit_sha TEXT NOT NULL CHECK (((object_format = '%s' AND length(commit_sha) = 40) OR (object_format = '%s' AND length(commit_sha) = 64)) AND commit_sha NOT GLOB '*[^0-9a-f]*' AND commit_sha NOT IN (printf('%%040d', 0), printf('%%064d', 0))),
			tree_sha TEXT NOT NULL CHECK (((object_format = '%s' AND length(tree_sha) = 40) OR (object_format = '%s' AND length(tree_sha) = 64)) AND tree_sha NOT GLOB '*[^0-9a-f]*' AND tree_sha NOT IN (printf('%%040d', 0), printf('%%064d', 0))),
			mount_path TEXT NOT NULL CHECK (length(mount_path) > 0 AND trim(mount_path) = mount_path AND substr(mount_path, 1, 1) <> '/' AND instr(mount_path, '\') = 0 AND mount_path NOT IN ('.', '..', 'default', 'cwd', 'HEAD') AND mount_path NOT LIKE './%%' AND mount_path NOT LIKE '../%%' AND mount_path NOT LIKE '%%/./%%' AND mount_path NOT LIKE '%%/../%%' AND mount_path NOT LIKE '%%/.' AND mount_path NOT LIKE '%%/..' AND instr(mount_path, '//') = 0),
			manifest_path TEXT NOT NULL CHECK (length(manifest_path) > 0 AND trim(manifest_path) = manifest_path AND substr(manifest_path, 1, 1) <> '/' AND instr(manifest_path, '\') = 0 AND manifest_path NOT IN ('.', '..', 'default', 'cwd', 'HEAD') AND manifest_path NOT LIKE './%%' AND manifest_path NOT LIKE '../%%' AND manifest_path NOT LIKE '%%/./%%' AND manifest_path NOT LIKE '%%/../%%' AND manifest_path NOT LIKE '%%/.' AND manifest_path NOT LIKE '%%/..' AND instr(manifest_path, '//') = 0),
			manifest_sha256 TEXT NOT NULL CHECK (length(manifest_sha256) = 71 AND substr(manifest_sha256, 1, 7) = 'sha256:' AND substr(manifest_sha256, 8) NOT GLOB '*[^0-9a-f]*' AND substr(manifest_sha256, 8) GLOB '*[1-9a-f]*'),
			manifest_schema_version TEXT NOT NULL CHECK (manifest_schema_version = '%s'),
			manifest_abi_digest TEXT NOT NULL CHECK (length(manifest_abi_digest) = 71 AND substr(manifest_abi_digest, 1, 7) = 'sha256:' AND substr(manifest_abi_digest, 8) NOT GLOB '*[^0-9a-f]*' AND substr(manifest_abi_digest, 8) GLOB '*[1-9a-f]*'),
			manifest_digest TEXT NOT NULL CHECK (length(manifest_digest) = 71 AND substr(manifest_digest, 1, 7) = 'sha256:' AND substr(manifest_digest, 8) NOT GLOB '*[^0-9a-f]*' AND substr(manifest_digest, 8) GLOB '*[1-9a-f]*'),
			profile TEXT NOT NULL CHECK (profile IN ('%s', '%s', '%s', '%s', '%s')),
			profile_workload_set_digest TEXT NOT NULL CHECK (length(profile_workload_set_digest) = 71 AND substr(profile_workload_set_digest, 1, 7) = 'sha256:' AND substr(profile_workload_set_digest, 8) NOT GLOB '*[^0-9a-f]*' AND substr(profile_workload_set_digest, 8) GLOB '*[1-9a-f]*'),
			CHECK (manifest_sha256 <> manifest_abi_digest AND manifest_sha256 <> manifest_digest AND manifest_sha256 <> profile_workload_set_digest AND manifest_abi_digest <> manifest_digest AND manifest_abi_digest <> profile_workload_set_digest AND manifest_digest <> profile_workload_set_digest),
			UNIQUE (job_id, repository_id, object_format, commit_sha, tree_sha, mount_path, manifest_path, manifest_sha256, manifest_schema_version, manifest_abi_digest, manifest_digest, profile, profile_workload_set_digest)
		);`,
			cicontract.RemoteRunTargetBindingsTable,
			GitObjectFormatSHA1, GitObjectFormatSHA256, GitObjectFormatSHA1, GitObjectFormatSHA256,
			GitObjectFormatSHA1, GitObjectFormatSHA256,
			cicontract.RemoteCITargetBindingManifestSchemaVersion,
			ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion, ProfileRelease,
		),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (profile, profile_workload_set_digest, repository_id, object_format, tree_sha, job_id);`, cicontract.RemoteRunTargetBindingsProfileIndex, cicontract.RemoteRunTargetBindingsTable),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT, 'remote CI target binding is immutable'); END;`, cicontract.RemoteRunTargetBindingsImmutableTrigger, cicontract.RemoteRunTargetBindingsTable),
	}
}

func durationLedgerRemoteCleanupJournalSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS ci_remote_cleanup_journals (
			job_id TEXT PRIMARY KEY CHECK (length(job_id) > 0),
			created_at TEXT NOT NULL CHECK (length(created_at) > 0)
		);`,
		`CREATE TABLE IF NOT EXISTS ci_remote_cleanup_targets (
			job_id TEXT NOT NULL CHECK (length(job_id) > 0),
			target_kind TEXT NOT NULL CHECK (target_kind IN ('oss_prefix', 'eci_container_group')),
			target_key TEXT NOT NULL CHECK (length(target_key) > 0),
			provider_id TEXT NOT NULL DEFAULT '',
			deterministic_name TEXT NOT NULL DEFAULT '',
			job_tag TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (job_id, target_kind, target_key),
			CHECK ((target_kind = 'oss_prefix' AND provider_id = '' AND deterministic_name = '' AND job_tag = '') OR (target_kind = 'eci_container_group' AND target_key = deterministic_name AND job_tag = job_id))
		);`,
		`CREATE INDEX IF NOT EXISTS idx_ci_remote_cleanup_targets_pending ON ci_remote_cleanup_targets (job_id, target_kind, provider_id);`,
	}
}

func durationLedgerRemoteCIRunExecutionIdentitySchemaStatements() []string {
	return []string{fmt.Sprintf(`CREATE TABLE IF NOT EXISTS ci_remote_run_execution_identities (
	job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	platform TEXT NOT NULL CHECK (platform = '%s'),
	runner_identity_digest TEXT NOT NULL CHECK (length(runner_identity_digest) = 71 AND substr(runner_identity_digest, 1, 7) = 'sha256:' AND substr(runner_identity_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	toolchain_digest TEXT NOT NULL CHECK (length(toolchain_digest) = 71 AND substr(toolchain_digest, 1, 7) = 'sha256:' AND substr(toolchain_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	execution_mode TEXT NOT NULL CHECK (execution_mode IN ('normal', 'calibration'))
);`, cicontract.TargetPlatform)}
}

func durationLedgerRemoteCIPlanningReceiptSchemaStatements() []string {
	return []string{`CREATE TABLE IF NOT EXISTS ci_remote_run_planning_receipts (
	job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	status TEXT NOT NULL CHECK (status IN ('not_applicable', 'planned')),
	reason TEXT NOT NULL CHECK (reason IN ('', 'pre_planning_failure', 'all_hit')),
	gate_plan_digest TEXT NOT NULL CHECK (length(gate_plan_digest) = 71 AND substr(gate_plan_digest, 1, 7) = 'sha256:' AND substr(gate_plan_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	catalog_digest TEXT NOT NULL CHECK (length(catalog_digest) = 71 AND substr(catalog_digest, 1, 7) = 'sha256:' AND substr(catalog_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	ledger_generation TEXT NOT NULL CHECK (ledger_generation <> '' AND ledger_generation NOT GLOB '0*' AND ledger_generation NOT GLOB '*[^0-9]*'),
	receipt_json TEXT NOT NULL CHECK (length(receipt_json) > 0),
	receipt_digest TEXT NOT NULL CHECK (length(receipt_digest) = 71 AND substr(receipt_digest, 1, 7) = 'sha256:' AND substr(receipt_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	mapping_root TEXT NOT NULL CHECK (mapping_root = '' OR (length(mapping_root) = 71 AND substr(mapping_root, 1, 7) = 'sha256:' AND substr(mapping_root, 8) NOT GLOB '*[^0-9a-f]*')),
	planned_workload_count INTEGER NOT NULL CHECK (planned_workload_count >= 0),
	planned_shard_count INTEGER NOT NULL CHECK (planned_shard_count >= 0),
	CHECK ((status = 'not_applicable' AND reason <> '' AND mapping_root = '' AND planned_workload_count = 0 AND planned_shard_count = 0) OR (status = 'planned' AND reason = ''))
);`, `CREATE INDEX IF NOT EXISTS idx_ci_remote_run_planning_receipts_status ON ci_remote_run_planning_receipts (status, reason, job_id);`}
}

// durationLedgerSQLiteSchemaStatementsV14 is the complete pre-scope schema.
func durationLedgerSQLiteSchemaStatementsV14() []string {
	statements := durationLedgerSQLiteLegacySchemaStatements()
	statements = append(statements, durationLedgerCompileTimingSchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCITerminalEvidenceSchemaStatements()...)
	return append(statements, strictLocalWorkloadPassSQLiteSchema)
}

func durationLedgerSQLiteLegacySchemaStatements() []string {
	statements := []string{
		durationLedgerSQLiteSchema,
		strictWorkloadPassReuseSQLiteSchema,
		strictCheckReceiptReuseSQLiteSchema,
		durationLedgerLiveTimingWarningTableSchema,
		durationLedgerRunTimingWarningTableSchema,
		durationLedgerLiveTimingWarningIndexSchema,
		durationLedgerRunTimingWarningIndexSchema,
	}
	return statements
}

func durationLedgerSQLiteLegacySchemaStatementsV13() []string {
	statements := []string{
		durationLedgerSQLiteSchema,
		frozenWorkloadPassReuseSQLiteSchemaV13,
		strictCheckReceiptReuseSQLiteSchema,
		durationLedgerLiveTimingWarningTableSchema,
		durationLedgerRunTimingWarningTableSchema,
		durationLedgerLiveTimingWarningIndexSchema,
		durationLedgerRunTimingWarningIndexSchema,
	}
	statements = append(statements, durationLedgerCompileTimingSchemaStatements()...)
	return append(statements, durationLedgerRemoteCITerminalEvidenceSchemaStatements()...)
}

func (validator *durationLedgerSQLiteSchemaValidator) preflight(queryer durationLedgerSQLiteSchemaQueryer, schemaVersion int) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(queryer)
	if err != nil {
		return err
	}
	if len(actual) == 0 {
		if schemaVersion != 0 {
			return fmt.Errorf("duration ledger SQLite empty authority has nonzero schema version %d", schemaVersion)
		}
		return nil
	}
	if schemaVersion != durationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite schema version %d is incompatible; refuse migration", schemaVersion)
	}
	expected, err := validator.expectedSchema()
	if err != nil {
		return err
	}
	return compareDurationLedgerSQLiteSchemaObjects(actual, expected)
}

func compareDurationLedgerSQLiteSchemaObjects(actual, expected map[string]string) error {
	for key, expectedDDL := range expected {
		actualDDL, ok := actual[key]
		if !ok {
			return fmt.Errorf("duration ledger SQLite schema object %q is missing; refuse partial authority migration", key)
		}
		if actualDDL != expectedDDL {
			return fmt.Errorf("duration ledger SQLite schema object %q has incompatible authority shape; refuse migration", key)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("duration ledger SQLite schema object %q is not canonical; refuse migration", key)
		}
	}
	return nil
}

// buildDurationLedgerSQLiteReferenceSchema 返回由唯一 current DDL 构造的物理 schema。
func buildDurationLedgerSQLiteReferenceSchema() (map[string]string, error) {
	return buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteCurrentSchemaStatements())
}

func buildDurationLedgerSQLiteReferenceSchemaForStatements(statements []string) (map[string]string, error) {
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open duration ledger SQLite reference schema: %w", err)
	}
	reference.SetMaxOpenConns(1)
	defer reference.Close()
	for _, schema := range statements {
		if _, err := reference.Exec(schema); err != nil {
			return nil, mapDurationLedgerSQLiteError("build duration ledger SQLite reference schema", err)
		}
	}
	return loadDurationLedgerSQLiteSchemaObjects(reference)
}

func loadDurationLedgerSQLiteSchemaObjects(queryer durationLedgerSQLiteSchemaQueryer) (map[string]string, error) {
	rows, err := queryer.QueryContext(context.Background(), `
		SELECT type, name, sql
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("read duration ledger SQLite physical schema", err)
	}
	defer rows.Close()
	objects := map[string]string{}
	for rows.Next() {
		var (
			objectType, name string
			ddl              sql.NullString
		)
		if err := rows.Scan(&objectType, &name, &ddl); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan duration ledger SQLite physical schema", err)
		}
		normalizedDDL := "<null>"
		if ddl.Valid {
			normalizedDDL = normalizeSQLiteDDL(ddl.String)
		}
		objects[objectType+":"+name] = normalizedDDL
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate duration ledger SQLite physical schema", err)
	}
	return objects, nil
}
