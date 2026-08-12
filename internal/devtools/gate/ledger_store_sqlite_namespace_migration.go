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
