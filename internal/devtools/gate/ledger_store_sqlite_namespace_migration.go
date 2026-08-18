package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const legacyDurationLedgerSQLiteSchemaVersion = 13

const localDurationLedgerSQLiteSchemaVersion = 14

const executionScopeDurationLedgerSQLiteSchemaVersion = 15

const retainedProofDurationLedgerSQLiteSchemaVersion = 16

const sourceReplayIndexDurationLedgerSQLiteSchemaVersion = 17

const workloadInputReplayDurationLedgerSQLiteSchemaVersion = 18

const runExecutionIdentityDurationLedgerSQLiteSchemaVersion = 19

const planningReceiptDurationLedgerSQLiteSchemaVersion = 20

const workloadSemanticReplayDurationLedgerSQLiteSchemaVersion = 21

// migrateDurationLedgerSQLiteSchema13To14 adds only local-origin projections;
// the frozen remote PASS table is validated and left untouched.
func migrateDurationLedgerSQLiteSchema13To14(database *sql.DB, now func() time.Time) error {
	if database == nil {
		return errors.New("duration ledger SQLite namespace migration requires database")
	}
	if now == nil {
		return errors.New("duration ledger SQLite namespace migration clock is required")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite namespace migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite namespace migration", err)
	}
	if err := migrateDurationLedgerSQLiteNamespaceOnConnection(connection, now); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite namespace migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLiteNamespaceOnConnection(connection *sql.Conn, now func() time.Time) error {
	if now == nil {
		return errors.New("duration ledger SQLite namespace migration clock is required")
	}
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != legacyDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite namespace migration expected schema version %d, got %d", legacyDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := validateLegacyWorkloadPassEvidenceShape(connection); err != nil {
		return err
	}
	if err := executeDurationLedgerSQLiteNamespaceDDL(connection); err != nil {
		return err
	}
	if err := initializeLocalAuthorityStateOnConnection(connection, now); err != nil {
		return err
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, localDurationLedgerSQLiteSchemaVersion)
}

func executeDurationLedgerSQLiteNamespaceDDL(connection *sql.Conn) error {
	for _, statement := range []string{
		strictLocalWorkloadPassSQLiteSchema,
		fmt.Sprintf(`PRAGMA user_version = %d`, localDurationLedgerSQLiteSchemaVersion),
	} {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite namespace schema", err)
		}
	}
	return nil
}

// migrateDurationLedgerSQLiteSchema14To15 adds only the execution-scope side
// table and indexes. Existing remote rows, DDL and foreign keys are not
// altered or rewritten.
func migrateDurationLedgerSQLiteSchema14To15(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite execution-scope migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite execution-scope migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite execution-scope migration", err)
	}
	if err := migrateDurationLedgerSQLiteExecutionScopeOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite execution-scope migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLiteExecutionScopeOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != localDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite execution-scope migration expected schema version %d, got %d", localDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, localDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerRemoteCIExecutionScopeSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite execution-scope schema", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, executionScopeDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite execution-scope schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, executionScopeDurationLedgerSQLiteSchemaVersion)
}

// migrateDurationLedgerSQLiteSchema15To16 adds consumer-owned direct proofs and
// indexes only; it never mutates v15 rows or relaxes existing foreign keys.
func migrateDurationLedgerSQLiteSchema15To16(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite retained proof migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite retained proof migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite retained proof migration", err)
	}
	if err := migrateDurationLedgerSQLiteRetainedProofOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite retained proof migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLiteRetainedProofOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != executionScopeDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite retained proof migration expected schema version %d, got %d", executionScopeDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, executionScopeDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerRetainedWorkloadPassProofSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite retained proof schema", err)
		}
	}
	if err := backfillRetainedWorkloadPassProofs(connection); err != nil {
		return err
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, retainedProofDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite retained proof schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, retainedProofDurationLedgerSQLiteSchemaVersion)
}

// migrateDurationLedgerSQLiteSchema16To17 只新增 source replay 分区索引；
// authority、proof 与 retention 行保持逐字不变。
func migrateDurationLedgerSQLiteSchema16To17(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite source replay index migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite source replay index migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite source replay index migration", err)
	}
	if err := migrateDurationLedgerSQLiteSourceReplayIndexesOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite source replay index migration", err)
	}
	return nil
}

// migrateDurationLedgerSQLiteSchema17To18 添加内容寻址的 workload input replay
// 索引；既有 authority、PASS proof 和 timing 行保持逐字不变。
func migrateDurationLedgerSQLiteSchema17To18(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite workload input replay cache migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite workload input replay cache migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite workload input replay cache migration", err)
	}
	if err := migrateDurationLedgerSQLiteWorkloadInputReplayCacheOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite workload input replay cache migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLiteWorkloadInputReplayCacheOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != sourceReplayIndexDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite workload input replay cache migration expected schema version %d, got %d", sourceReplayIndexDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, sourceReplayIndexDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerWorkloadInputReplayCacheSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite workload input replay cache", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, workloadInputReplayDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite workload input replay cache schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, workloadInputReplayDurationLedgerSQLiteSchemaVersion)
}

// migrateDurationLedgerSQLiteSchema18To19 添加 run execution identity 侧表；
// 既有 v18 run 不回填身份，也不会被猜测为当前执行环境。
func migrateDurationLedgerSQLiteSchema18To19(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite run execution identity migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite run execution identity migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite run execution identity migration", err)
	}
	if err := migrateDurationLedgerSQLiteRunExecutionIdentityOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite run execution identity migration", err)
	}
	return nil
}

// migrateDurationLedgerSQLiteSchema19To20 仅增加规划回执侧表和索引，不回填既有 run。
func migrateDurationLedgerSQLiteSchema19To20(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite planning receipt migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite planning receipt migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite planning receipt migration", err)
	}
	if err := migrateDurationLedgerSQLitePlanningReceiptOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite planning receipt migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLitePlanningReceiptOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != runExecutionIdentityDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite planning receipt migration expected schema version %d, got %d", runExecutionIdentityDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, runExecutionIdentityDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerRemoteCIPlanningReceiptSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite planning receipt", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, planningReceiptDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite planning receipt schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, planningReceiptDurationLedgerSQLiteSchemaVersion)
}

// durationLedgerWorkloadSemanticReplayCacheSchemaStatements 只属于 v20 到 v21
// 的显式迁移步骤，禁止混入 current schema 声明文件伪装为启动兼容 DDL。
func durationLedgerWorkloadSemanticReplayCacheSchemaStatements() []string {
	return []string{
		`ALTER TABLE ci_workload_input_replay_cache ADD COLUMN compile_digest TEXT NOT NULL DEFAULT '' CHECK (compile_digest = '' OR (length(compile_digest) = 71 AND substr(compile_digest, 1, 7) = 'sha256:' AND substr(compile_digest, 8) NOT GLOB '*[^0-9a-f]*'));`,
		`ALTER TABLE ci_workload_input_replay_cache ADD COLUMN declaration_digest TEXT NOT NULL DEFAULT '' CHECK (declaration_digest = '' OR (length(declaration_digest) = 71 AND substr(declaration_digest, 1, 7) = 'sha256:' AND substr(declaration_digest, 8) NOT GLOB '*[^0-9a-f]*'));`,
		`ALTER TABLE ci_workload_input_replay_cache ADD COLUMN runtime_digest TEXT NOT NULL DEFAULT '' CHECK (runtime_digest = '' OR (length(runtime_digest) = 71 AND substr(runtime_digest, 1, 7) = 'sha256:' AND substr(runtime_digest, 8) NOT GLOB '*[^0-9a-f]*'));`,
		`ALTER TABLE ci_workload_input_replay_cache ADD COLUMN runtime_fallback INTEGER NOT NULL DEFAULT 0 CHECK (runtime_fallback IN (0, 1));`,
	}
}

// migrateDurationLedgerSQLiteSchema20To21 只扩展 workload input 派生缓存，
// 不回填或改写任何 PASS authority、receipt 或历史耗时证据。
func migrateDurationLedgerSQLiteSchema20To21(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite semantic replay cache migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite semantic replay cache migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite semantic replay cache migration", err)
	}
	if err := migrateDurationLedgerSQLiteSemanticReplayCacheOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite semantic replay cache migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLiteSemanticReplayCacheOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != planningReceiptDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite semantic replay cache migration expected schema version %d, got %d", planningReceiptDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, planningReceiptDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerWorkloadSemanticReplayCacheSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite semantic replay cache", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, workloadSemanticReplayDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite semantic replay cache schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, workloadSemanticReplayDurationLedgerSQLiteSchemaVersion)
}

// migrateDurationLedgerSQLiteSchema21To22 只新增独立运维 cleanup journal；它不
// 引用、回填或改写 ci_runs、PASS、timing、receipt 和 retention 历史根。
func migrateDurationLedgerSQLiteSchema21To22(database *sql.DB) error {
	if database == nil {
		return errors.New("duration ledger SQLite remote cleanup journal migration requires database")
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite remote cleanup journal migration connection", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return mapDurationLedgerSQLiteError("begin duration ledger SQLite remote cleanup journal migration", err)
	}
	if err := migrateDurationLedgerSQLiteRemoteCleanupJournalOnConnection(connection); err != nil {
		return rollbackDurationLedgerSQLiteNamespace(connection, err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return mapDurationLedgerSQLiteError("commit duration ledger SQLite remote cleanup journal migration", err)
	}
	return nil
}

func migrateDurationLedgerSQLiteRemoteCleanupJournalOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != workloadSemanticReplayDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite remote cleanup journal migration expected schema version %d, got %d", workloadSemanticReplayDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, workloadSemanticReplayDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerRemoteCleanupJournalSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite remote cleanup journal", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, durationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite remote cleanup journal schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, durationLedgerSQLiteSchemaVersion)
}

func migrateDurationLedgerSQLiteRunExecutionIdentityOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != workloadInputReplayDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite run execution identity migration expected schema version %d, got %d", workloadInputReplayDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, workloadInputReplayDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerRemoteCIRunExecutionIdentitySchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite run execution identity", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, runExecutionIdentityDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite run execution identity schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, runExecutionIdentityDurationLedgerSQLiteSchemaVersion)
}

// migrateDurationLedgerSQLiteSourceReplayIndexesOnConnection 在同一写事务内校验 v16、
// 创建两路分区索引并推进到 v17。
func migrateDurationLedgerSQLiteSourceReplayIndexesOnConnection(connection *sql.Conn) error {
	version, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if version != retainedProofDurationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite source replay index migration expected schema version %d, got %d", retainedProofDurationLedgerSQLiteSchemaVersion, version)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(connection, retainedProofDurationLedgerSQLiteSchemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerSourceReplayIndexSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite source replay indexes", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, sourceReplayIndexDurationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite source replay index schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, sourceReplayIndexDurationLedgerSQLiteSchemaVersion)
}

// backfillRetainedWorkloadPassProofs snapshots direct proof material for live
// reused consumers before strict v16 root pruning is allowed.
func backfillRetainedWorkloadPassProofs(connection *sql.Conn) error {
	proofs, err := validateRetainedWorkloadPassProofBackfillSources(connection)
	if err != nil {
		return err
	}
	const insertProof = `
		INSERT INTO ci_retained_workload_pass_proofs (
			consumer_job_id, workload_id, identity_digest, origin_job_id,
			origin_accepted_generation, origin_source_tree_sha,
			origin_receipt_set_sha256, origin_execution_json, evidence_sha256
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, proof := range proofs {
		arguments, err := proof.insertArguments()
		if err != nil {
			return err
		}
		if _, err := connection.ExecContext(context.Background(), insertProof, arguments...); err != nil {
			return mapDurationLedgerSQLiteError("backfill retained workload pass proof", err)
		}
	}
	return nil
}

// validateRetainedWorkloadPassProofBackfillSources 将 v15 live reused consumer 的
// 所有 source、direct result、canonical identity 和 strict JSON 在写 v16 前闭合。
func validateRetainedWorkloadPassProofBackfillSources(connection *sql.Conn) ([]retainedWorkloadPassProof, error) {
	if connection == nil {
		return nil, errors.New("retained workload pass proof backfill connection is nil")
	}
	const liveReused = `
		FROM ci_run_workload_results AS results
		JOIN ci_runs AS consumer ON consumer.job_id = results.job_id
		JOIN ci_remote_baseline_state AS baseline ON baseline.singleton = 1
		WHERE results.disposition = 'reused'
			AND consumer.accepted_generation IN (baseline.generation, CAST(CAST(baseline.generation AS INTEGER) - 1 AS TEXT), CAST(CAST(baseline.generation AS INTEGER) - 2 AS TEXT))`
	var expected int64
	if err := connection.QueryRowContext(context.Background(), `SELECT COUNT(*) `+liveReused).Scan(&expected); err != nil {
		return nil, mapDurationLedgerSQLiteError("count live reused consumers for retained proof backfill", err)
	}
	rows, err := connection.QueryContext(context.Background(), `SELECT results.job_id, consumer.accepted_generation, results.identity_digest, results.origin_accepted_generation, results.workload_id, results.execution_digest, results.input_digest, results.environment_digest, results.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, results.evidence_sha256
		FROM ci_run_workload_results AS results
		JOIN ci_runs AS consumer ON consumer.job_id = results.job_id
		JOIN ci_remote_baseline_state AS baseline ON baseline.singleton = 1
		LEFT JOIN ci_run_workload_results AS direct ON direct.job_id = results.origin_job_id AND direct.workload_id = results.workload_id AND direct.identity_digest = results.identity_digest AND direct.disposition = 'executed' AND direct.origin_job_id = direct.job_id AND direct.origin_accepted_generation = results.origin_accepted_generation
		LEFT JOIN ci_runs AS origin ON origin.job_id = direct.job_id AND origin.accepted_generation = results.origin_accepted_generation
		LEFT JOIN ci_workload_pass_evidence AS evidence ON evidence.identity_digest = results.identity_digest AND evidence.accepted_generation = results.origin_accepted_generation AND evidence.origin_job_id = results.origin_job_id AND evidence.workload_id = results.workload_id AND evidence.execution_digest = results.execution_digest AND evidence.input_digest = results.input_digest AND evidence.environment_digest = results.environment_digest AND evidence.evidence_sha256 = results.evidence_sha256
		WHERE results.disposition = 'reused' AND consumer.accepted_generation IN (baseline.generation, CAST(CAST(baseline.generation AS INTEGER) - 1 AS TEXT), CAST(CAST(baseline.generation AS INTEGER) - 2 AS TEXT)) AND direct.job_id IS NOT NULL AND origin.job_id IS NOT NULL AND evidence.identity_digest IS NOT NULL`)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query retained workload pass proof backfill sources", err)
	}
	defer rows.Close()
	proofs := make([]retainedWorkloadPassProof, 0)
	var scanned int64
	for rows.Next() {
		proof, retired, err := classifyRetainedWorkloadPassProofBackfillRow(rows)
		if err != nil {
			return nil, err
		}
		scanned++
		if !retired {
			proofs = append(proofs, proof)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate retained workload pass proof backfill sources", err)
	}
	if scanned != expected {
		return nil, fmt.Errorf("retained workload pass proof backfill verified row count = %d, want %d; source missing, non-direct, or canonical drifted", scanned, expected)
	}
	return proofs, nil
}

func classifyRetainedWorkloadPassProofBackfillRow(rows workloadPassEvidenceScanner) (retainedWorkloadPassProof, bool, error) {
	proof, err := validateRetainedWorkloadPassProofBackfillRow(rows)
	if err == nil {
		return proof, false, nil
	}
	if errors.Is(err, errLegacyWorkloadPassIdentityDomain) {
		return retainedWorkloadPassProof{}, true, nil
	}
	return retainedWorkloadPassProof{}, false, err
}

func validateRetainedWorkloadPassProofBackfillRow(rows workloadPassEvidenceScanner) (retainedWorkloadPassProof, error) {
	var consumerJobID, consumerGeneration, generation, workloadID, executionJSON string
	var evidence WorkloadPassEvidence
	if err := rows.Scan(&consumerJobID, &consumerGeneration, &evidence.Identity.IdentityDigest, &generation, &workloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest, &evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256); err != nil {
		return retainedWorkloadPassProof{}, mapDurationLedgerSQLiteError("scan retained workload pass proof backfill source", err)
	}
	if err := populateRetainedWorkloadPassProofBackfillEvidence(&evidence, generation, workloadID, executionJSON); err != nil {
		return retainedWorkloadPassProof{}, err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return retainedWorkloadPassProof{}, fmt.Errorf("retained workload pass proof backfill canonical evidence: %w", err)
	}
	return retainedWorkloadPassProof{ConsumerJobID: consumerJobID, ConsumerAcceptedGeneration: consumerGeneration, WorkloadID: workloadID, IdentityDigest: evidence.Identity.IdentityDigest, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: generation, OriginSourceTreeSHA: evidence.OriginSourceTreeSHA, OriginReceiptSetSHA256: evidence.OriginReceiptSetSHA256, OriginExecutionJSON: executionJSON, EvidenceSHA256: evidence.EvidenceSHA256}, nil
}

func populateRetainedWorkloadPassProofBackfillEvidence(evidence *WorkloadPassEvidence, generation, workloadID, executionJSON string) error {
	parsed, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || parsed == 0 || generation != strconv.FormatUint(parsed, 10) {
		return errors.New("retained workload pass proof backfill origin generation is invalid")
	}
	evidence.Identity.WorkloadID = GateID(workloadID)
	evidence.OriginAcceptedGeneration = parsed
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return fmt.Errorf("retained workload pass proof backfill execution JSON: %w", err)
	}
	return nil
}

func preflightDurationLedgerSQLiteSchemaVersion(queryer durationLedgerSQLiteSchemaQueryer, version int) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(queryer)
	if err != nil {
		return err
	}
	var statements []string
	switch version {
	case legacyDurationLedgerSQLiteSchemaVersion:
		statements = durationLedgerSQLiteLegacySchemaStatementsV13()
	case localDurationLedgerSQLiteSchemaVersion:
		statements = durationLedgerSQLiteSchemaStatementsV14()
	default:
		if version < executionScopeDurationLedgerSQLiteSchemaVersion || version > durationLedgerSQLiteSchemaVersion {
			return fmt.Errorf("duration ledger SQLite schema version %d is unsupported for preflight", version)
		}
		statements = append(durationLedgerSQLiteSchemaStatementsV14(), durationLedgerRemoteCIExecutionScopeSchemaStatements()...)
		for _, step := range []struct {
			version    int
			statements func() []string
		}{
			{retainedProofDurationLedgerSQLiteSchemaVersion, durationLedgerRetainedWorkloadPassProofSchemaStatements},
			{sourceReplayIndexDurationLedgerSQLiteSchemaVersion, durationLedgerSourceReplayIndexSchemaStatements},
			{workloadInputReplayDurationLedgerSQLiteSchemaVersion, durationLedgerWorkloadInputReplayCacheSchemaStatements},
			{runExecutionIdentityDurationLedgerSQLiteSchemaVersion, durationLedgerRemoteCIRunExecutionIdentitySchemaStatements},
			{planningReceiptDurationLedgerSQLiteSchemaVersion, durationLedgerRemoteCIPlanningReceiptSchemaStatements},
			{workloadSemanticReplayDurationLedgerSQLiteSchemaVersion, durationLedgerWorkloadSemanticReplayCacheSchemaStatements},
			{durationLedgerSQLiteSchemaVersion, durationLedgerRemoteCleanupJournalSchemaStatements},
		} {
			if version >= step.version {
				statements = append(statements, step.statements()...)
			}
		}
	}
	expected, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(statements)
	if err != nil {
		return err
	}
	return compareDurationLedgerSQLiteSchemaObjects(actual, expected)
}

func rollbackDurationLedgerSQLiteNamespace(connection *sql.Conn, cause error) error {
	if _, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`); rollbackErr != nil {
		return errors.Join(cause, mapDurationLedgerSQLiteError("rollback duration ledger SQLite namespace migration", rollbackErr))
	}
	return cause
}

// validateLegacyDurationLedgerSQLiteSchema rejects any v13 shape drift before
// the additive local tables are created.
func validateLegacyWorkloadPassEvidenceShape(queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(queryer)
	if err != nil {
		return err
	}
	expected, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteLegacySchemaStatementsV13())
	if err != nil {
		return err
	}
	return compareDurationLedgerSQLiteSchemaObjects(actual, expected)
}
