package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const legacyDurationLedgerSQLiteSchemaVersion = 13

const localDurationLedgerSQLiteSchemaVersion = 14

const executionScopeDurationLedgerSQLiteSchemaVersion = 15

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
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, durationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite retained proof schema version", err)
	}
	return preflightDurationLedgerSQLiteSchemaVersion(connection, durationLedgerSQLiteSchemaVersion)
}

// backfillRetainedWorkloadPassProofs snapshots direct proof material for live
// reused consumers before strict v16 root pruning is allowed.
func backfillRetainedWorkloadPassProofs(connection *sql.Conn) error {
	expected, err := validateRetainedWorkloadPassProofBackfillSources(connection)
	if err != nil {
		return err
	}
	result, err := connection.ExecContext(context.Background(), `
		INSERT INTO ci_retained_workload_pass_proofs (
			consumer_job_id, workload_id, identity_digest, origin_job_id,
			origin_accepted_generation, origin_source_tree_sha,
			origin_receipt_set_sha256, origin_execution_json, evidence_sha256
		)
		SELECT results.job_id, results.workload_id, results.identity_digest,
			evidence.origin_job_id, evidence.accepted_generation,
			evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256,
			evidence.origin_execution_json, evidence.evidence_sha256
		FROM ci_run_workload_results AS results
		JOIN ci_runs AS consumer ON consumer.job_id = results.job_id
		JOIN ci_remote_baseline_state AS baseline ON baseline.singleton = 1
		JOIN ci_run_workload_results AS direct
			ON direct.job_id = results.origin_job_id
			AND direct.workload_id = results.workload_id
			AND direct.identity_digest = results.identity_digest
			AND direct.disposition = 'executed'
			AND direct.origin_job_id = direct.job_id
			AND direct.origin_accepted_generation = results.origin_accepted_generation
		JOIN ci_runs AS origin ON origin.job_id = direct.job_id
			AND origin.accepted_generation = results.origin_accepted_generation
		JOIN ci_workload_pass_evidence AS evidence
			ON evidence.identity_digest = results.identity_digest
			AND evidence.accepted_generation = results.origin_accepted_generation
			AND evidence.origin_job_id = results.origin_job_id
			AND evidence.workload_id = results.workload_id
			AND evidence.execution_digest = results.execution_digest
			AND evidence.input_digest = results.input_digest
			AND evidence.environment_digest = results.environment_digest
			AND evidence.evidence_sha256 = results.evidence_sha256
		WHERE results.disposition = 'reused'
			AND consumer.accepted_generation IN (
				baseline.generation,
				CAST(CAST(baseline.generation AS INTEGER) - 1 AS TEXT),
				CAST(CAST(baseline.generation AS INTEGER) - 2 AS TEXT)
			)`)
	if err != nil {
		return mapDurationLedgerSQLiteError("backfill retained workload pass proofs", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return mapDurationLedgerSQLiteError("count retained workload pass proof backfill", err)
	}
	if inserted != expected {
		return fmt.Errorf("retained workload pass proof backfill row count = %d, want %d", inserted, expected)
	}
	return nil
}

// validateRetainedWorkloadPassProofBackfillSources 将 v15 live reused consumer 的
// 所有 source、direct result、canonical identity 和 strict JSON 在写 v16 前闭合。
func validateRetainedWorkloadPassProofBackfillSources(connection *sql.Conn) (int64, error) {
	if connection == nil {
		return 0, errors.New("retained workload pass proof backfill connection is nil")
	}
	const liveReused = `
		FROM ci_run_workload_results AS results
		JOIN ci_runs AS consumer ON consumer.job_id = results.job_id
		JOIN ci_remote_baseline_state AS baseline ON baseline.singleton = 1
		WHERE results.disposition = 'reused'
			AND consumer.accepted_generation IN (baseline.generation, CAST(CAST(baseline.generation AS INTEGER) - 1 AS TEXT), CAST(CAST(baseline.generation AS INTEGER) - 2 AS TEXT))`
	var expected int64
	if err := connection.QueryRowContext(context.Background(), `SELECT COUNT(*) `+liveReused).Scan(&expected); err != nil {
		return 0, mapDurationLedgerSQLiteError("count live reused consumers for retained proof backfill", err)
	}
	rows, err := connection.QueryContext(context.Background(), `SELECT results.identity_digest, results.origin_accepted_generation, results.workload_id, results.execution_digest, results.input_digest, results.environment_digest, results.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, results.evidence_sha256
		FROM ci_run_workload_results AS results
		JOIN ci_runs AS consumer ON consumer.job_id = results.job_id
		JOIN ci_remote_baseline_state AS baseline ON baseline.singleton = 1
		LEFT JOIN ci_run_workload_results AS direct ON direct.job_id = results.origin_job_id AND direct.workload_id = results.workload_id AND direct.identity_digest = results.identity_digest AND direct.disposition = 'executed' AND direct.origin_job_id = direct.job_id AND direct.origin_accepted_generation = results.origin_accepted_generation
		LEFT JOIN ci_runs AS origin ON origin.job_id = direct.job_id AND origin.accepted_generation = results.origin_accepted_generation
		LEFT JOIN ci_workload_pass_evidence AS evidence ON evidence.identity_digest = results.identity_digest AND evidence.accepted_generation = results.origin_accepted_generation AND evidence.origin_job_id = results.origin_job_id AND evidence.workload_id = results.workload_id AND evidence.execution_digest = results.execution_digest AND evidence.input_digest = results.input_digest AND evidence.environment_digest = results.environment_digest AND evidence.evidence_sha256 = results.evidence_sha256
		WHERE results.disposition = 'reused' AND consumer.accepted_generation IN (baseline.generation, CAST(CAST(baseline.generation AS INTEGER) - 1 AS TEXT), CAST(CAST(baseline.generation AS INTEGER) - 2 AS TEXT)) AND direct.job_id IS NOT NULL AND origin.job_id IS NOT NULL AND evidence.identity_digest IS NOT NULL`)
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("query retained workload pass proof backfill sources", err)
	}
	defer rows.Close()
	var validated int64
	for rows.Next() {
		if err := validateRetainedWorkloadPassProofBackfillRow(rows); err != nil {
			return 0, err
		}
		validated++
	}
	if err := rows.Err(); err != nil {
		return 0, mapDurationLedgerSQLiteError("iterate retained workload pass proof backfill sources", err)
	}
	if validated != expected {
		return 0, fmt.Errorf("retained workload pass proof backfill verified row count = %d, want %d; source missing, non-direct, or canonical drifted", validated, expected)
	}
	return expected, nil
}

func validateRetainedWorkloadPassProofBackfillRow(rows workloadPassEvidenceScanner) error {
	var generation, workloadID, executionJSON string
	var evidence WorkloadPassEvidence
	if err := rows.Scan(&evidence.Identity.IdentityDigest, &generation, &workloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest, &evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256); err != nil {
		return mapDurationLedgerSQLiteError("scan retained workload pass proof backfill source", err)
	}
	if err := populateRetainedWorkloadPassProofBackfillEvidence(&evidence, generation, workloadID, executionJSON); err != nil {
		return err
	}
	if err := validateRetainedWorkloadPassProofBackfillEvidence(evidence, executionJSON); err != nil {
		return fmt.Errorf("retained workload pass proof backfill canonical evidence: %w", err)
	}
	return nil
}

// validateRetainedWorkloadPassProofBackfillEvidence 允许 v15 旧无域 identity
// 原样进入辅助投影；strict JSON、origin 与 evidence 摘要仍须闭合，但旧
// execution profile 不按当前语义重验，否则自然 MISS 会在迁移前被阻断。
func validateRetainedWorkloadPassProofBackfillEvidence(evidence WorkloadPassEvidence, executionJSON string) error {
	err := validateWorkloadPassEvidence(evidence)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errLegacyWorkloadPassIdentityDomain) {
		return err
	}
	if err := validateWorkloadPassEvidenceOrigin(evidence); err != nil {
		return err
	}
	expected, err := legacyWorkloadPassEvidenceSHA256(evidence, executionJSON)
	if err != nil {
		return err
	}
	if evidence.EvidenceSHA256 != expected {
		return errors.New("workload pass evidence SHA-256 does not match content")
	}
	return nil
}

// legacyWorkloadPassEvidenceSHA256 以 v15 持久化的原始 execution
// JSON 重放旧 evidence 摘要，避免当前结构编码给历史证据添加新字段。
func legacyWorkloadPassEvidenceSHA256(evidence WorkloadPassEvidence, executionJSON string) (string, error) {
	payload, err := json.Marshal(struct {
		Identity                 WorkloadPassIdentity `json:"identity"`
		OriginJobID              string               `json:"origin_job_id"`
		OriginAcceptedGeneration uint64               `json:"origin_accepted_generation"`
		OriginSourceTreeSHA      string               `json:"origin_source_tree_sha"`
		OriginReceiptSetSHA256   string               `json:"origin_receipt_set_sha256"`
		OriginExecution          json.RawMessage      `json:"origin_execution"`
	}{
		Identity:                 evidence.Identity,
		OriginJobID:              evidence.OriginJobID,
		OriginAcceptedGeneration: evidence.OriginAcceptedGeneration,
		OriginSourceTreeSHA:      evidence.OriginSourceTreeSHA,
		OriginReceiptSetSHA256:   evidence.OriginReceiptSetSHA256,
		OriginExecution:          json.RawMessage(executionJSON),
	})
	if err != nil {
		return "", fmt.Errorf("encode retained workload pass proof legacy evidence: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
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
	case executionScopeDurationLedgerSQLiteSchemaVersion:
		statements = append(durationLedgerSQLiteSchemaStatementsV14(), durationLedgerRemoteCIExecutionScopeSchemaStatements()...)
	case durationLedgerSQLiteSchemaVersion:
		statements = durationLedgerSQLiteCurrentSchemaStatements()
	default:
		return fmt.Errorf("duration ledger SQLite schema version %d is unsupported for preflight", version)
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
