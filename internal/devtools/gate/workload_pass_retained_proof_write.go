package gate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// insertRetainedWorkloadPassProof writes the immutable consumer-owned proof.
// A SQLite constraint is idempotent only after strict, canonical whole-proof
// readback; it is never an upsert or a replacement.
func insertRetainedWorkloadPassProof(tx *sql.Tx, consumerJobID string, consumerAcceptedGeneration uint64, result RemoteCIWorkloadResult) error {
	evidence, err := loadSQLiteReusableWorkloadEvidence(tx, result)
	if err != nil {
		return err
	}
	if err := validateReusableWorkloadEvidenceBinding(evidence, result); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("validate retained workload pass proof: %w", err)
	}
	executionJSON, err := json.Marshal(evidence.OriginExecution)
	if err != nil {
		return fmt.Errorf("encode retained workload pass proof execution: %w", err)
	}
	expected := retainedWorkloadPassProof{
		ConsumerJobID: consumerJobID, ConsumerAcceptedGeneration: strconv.FormatUint(consumerAcceptedGeneration, 10),
		WorkloadID: string(result.Identity.WorkloadID), IdentityDigest: evidence.Identity.IdentityDigest,
		OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: strconv.FormatUint(evidence.OriginAcceptedGeneration, 10),
		OriginSourceTreeSHA: evidence.OriginSourceTreeSHA, OriginReceiptSetSHA256: evidence.OriginReceiptSetSHA256,
		OriginExecutionJSON: string(executionJSON), EvidenceSHA256: evidence.EvidenceSHA256,
	}
	if err := expected.canonicalizeExecutionJSON(); err != nil {
		return fmt.Errorf("canonicalize retained workload pass proof execution: %w", err)
	}
	return insertOrCompareRetainedWorkloadPassProof(tx, expected, result.Identity)
}

// retainedWorkloadPassProof is the complete comparison row. Tags are the sole
// INSERT projection source; consumer generation is read from ci_runs.
type retainedWorkloadPassProof struct {
	ConsumerJobID              string `proof:"consumer_job_id"`
	ConsumerAcceptedGeneration string `proof:"-"`
	WorkloadID                 string `proof:"workload_id"`
	IdentityDigest             string `proof:"identity_digest"`
	OriginJobID                string `proof:"origin_job_id"`
	OriginAcceptedGeneration   string `proof:"origin_accepted_generation"`
	OriginSourceTreeSHA        string `proof:"origin_source_tree_sha"`
	OriginReceiptSetSHA256     string `proof:"origin_receipt_set_sha256"`
	OriginExecutionJSON        string `proof:"origin_execution_json"`
	EvidenceSHA256             string `proof:"evidence_sha256"`
}

func insertOrCompareRetainedWorkloadPassProof(tx *sql.Tx, expected retainedWorkloadPassProof, identity WorkloadPassIdentity) error {
	columns, err := retainedWorkloadPassProofColumns()
	if err != nil {
		return err
	}
	arguments, err := expected.insertArguments()
	if err != nil {
		return err
	}
	query := fmt.Sprintf("INSERT INTO ci_retained_workload_pass_proofs (%s) VALUES (%s)", strings.Join(columns, ", "), strings.TrimRight(strings.Repeat("?, ", len(columns)), ", "))
	_, insertErr := tx.Exec(query, arguments...)
	if insertErr == nil {
		return nil
	}
	if !isSQLiteConstraintError(insertErr) {
		return mapDurationLedgerSQLiteError("insert retained workload pass proof", insertErr)
	}
	actual, err := loadRetainedWorkloadPassProofForCollision(tx, expected.ConsumerJobID, GateID(expected.WorkloadID))
	if errors.Is(err, sql.ErrNoRows) {
		return mapDurationLedgerSQLiteError("insert retained workload pass proof", insertErr)
	}
	if err != nil {
		return fmt.Errorf("reload conflicting retained workload pass proof: %w", err)
	}
	if err := actual.validate(identity); err != nil {
		return fmt.Errorf("validate conflicting retained workload pass proof: %w", err)
	}
	if !actual.matches(expected) {
		return errors.New("conflicting retained workload pass proof for consumer and workload")
	}
	return nil
}

func (proof retainedWorkloadPassProof) matches(expected retainedWorkloadPassProof) bool {
	return proof == expected
}

func retainedWorkloadPassProofColumns() ([]string, error) {
	typeOfProof := reflect.TypeFor[retainedWorkloadPassProof]()
	columns := make([]string, 0)
	seenColumns := make(map[string]struct{})
	for field := range typeOfProof.Fields() {
		column, ok := field.Tag.Lookup("proof")
		if !ok || column == "" {
			return nil, fmt.Errorf("retained workload pass proof field %q has no projection tag", field.Name)
		}
		if column == "-" {
			continue
		}
		if _, duplicate := seenColumns[column]; duplicate {
			return nil, fmt.Errorf("retained workload pass proof field %q reuses projection tag %q", field.Name, column)
		}
		seenColumns[column] = struct{}{}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, errors.New("retained workload pass proof insert projection is empty")
	}
	return columns, nil
}

func (proof retainedWorkloadPassProof) insertArguments() ([]any, error) {
	value := reflect.ValueOf(proof)
	arguments := make([]any, 0, value.NumField()-1)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.Tag.Get("proof") == "-" {
			continue
		}
		if field.Tag.Get("proof") == "" || field.Type.Kind() != reflect.String {
			return nil, fmt.Errorf("retained workload pass proof field %q is not a tagged string projection", field.Name)
		}
		arguments = append(arguments, value.Field(index).String())
	}
	return arguments, nil
}

func loadRetainedWorkloadPassProofForCollision(tx *sql.Tx, consumerJobID string, workloadID GateID) (retainedWorkloadPassProof, error) {
	columns, err := retainedWorkloadPassProofColumns()
	if err != nil {
		return retainedWorkloadPassProof{}, err
	}
	projection := make([]string, 0, len(columns)+1)
	projection = append(projection, "consumer.accepted_generation")
	for _, column := range columns {
		projection = append(projection, "proof."+column)
	}
	var proof retainedWorkloadPassProof
	destinations, err := proof.scanDestinations()
	if err != nil {
		return retainedWorkloadPassProof{}, err
	}
	row := tx.QueryRow(fmt.Sprintf("SELECT %s FROM ci_retained_workload_pass_proofs AS proof JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id WHERE proof.consumer_job_id = ? AND proof.workload_id = ?", strings.Join(projection, ", ")), consumerJobID, string(workloadID))
	if err := row.Scan(append([]any{&proof.ConsumerAcceptedGeneration}, destinations...)...); err != nil {
		return retainedWorkloadPassProof{}, err
	}
	return proof, nil
}

func (proof *retainedWorkloadPassProof) scanDestinations() ([]any, error) {
	value := reflect.ValueOf(proof).Elem()
	destinations := make([]any, 0, value.NumField()-1)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.Tag.Get("proof") == "-" {
			continue
		}
		if field.Tag.Get("proof") == "" || field.Type.Kind() != reflect.String {
			return nil, fmt.Errorf("retained workload pass proof field %q is not a scannable tagged string projection", field.Name)
		}
		destinations = append(destinations, value.Field(index).Addr().Interface())
	}
	return destinations, nil
}

func (proof *retainedWorkloadPassProof) canonicalizeExecutionJSON() error {
	var execution PlanGateExecution
	if err := decodeStoredWorkloadPassExecutionJSON(proof.OriginExecutionJSON, &execution); err != nil {
		return err
	}
	encoded, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	proof.OriginExecutionJSON = string(encoded)
	return nil
}

func (proof retainedWorkloadPassProof) validate(identity WorkloadPassIdentity) error {
	if err := proof.canonicalizeExecutionJSON(); err != nil {
		return err
	}
	generation, err := strconv.ParseUint(proof.OriginAcceptedGeneration, 10, 64)
	if err != nil || generation == 0 {
		return errors.New("retained workload pass proof origin generation is invalid")
	}
	var execution PlanGateExecution
	if err := decodeStoredWorkloadPassExecutionJSON(proof.OriginExecutionJSON, &execution); err != nil {
		return err
	}
	return validateWorkloadPassEvidence(WorkloadPassEvidence{Identity: identity, OriginJobID: proof.OriginJobID, OriginAcceptedGeneration: generation, OriginSourceTreeSHA: proof.OriginSourceTreeSHA, OriginReceiptSetSHA256: proof.OriginReceiptSetSHA256, OriginExecution: execution, EvidenceSHA256: proof.EvidenceSHA256})
}

// replaceSQLiteRemoteCIWorkloadResults atomically replaces a run's workload
// projection; retained-proof collisions must therefore abort the whole batch.
func replaceSQLiteRemoteCIWorkloadResults(tx *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := tx.Exec(`DELETE FROM ci_run_workload_results WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI workload results", err)
	}
	for _, result := range record.WorkloadResults {
		if err := storeSQLiteRemoteCIWorkloadResult(tx, record, result); err != nil {
			return err
		}
	}
	return nil
}

func storeSQLiteRemoteCIWorkloadResult(tx *sql.Tx, record RemoteCIRunRecord, result RemoteCIWorkloadResult) error {
	if result.Disposition == WorkloadDispositionExecuted && (result.OriginJobID != record.JobID || result.OriginAcceptedGeneration != record.AcceptedGeneration) {
		return fmt.Errorf("executed workload result %q must originate from this run", result.Identity.WorkloadID)
	}
	if result.Disposition == WorkloadDispositionReused {
		if err := verifySQLiteReusableWorkloadEvidence(tx, result); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO ci_run_workload_results (job_id, workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.JobID, string(result.Identity.WorkloadID), result.Identity.IdentityDigest, result.Identity.ExecutionDigest, result.Identity.InputDigest, result.Identity.EnvironmentDigest, result.Disposition, result.OriginJobID, strconv.FormatUint(result.OriginAcceptedGeneration, 10), result.EvidenceSHA256); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI workload result", err)
	}
	if result.Disposition == WorkloadDispositionReused {
		return insertRetainedWorkloadPassProof(tx, record.JobID, record.AcceptedGeneration, result)
	}
	return nil
}

func verifySQLiteReusableWorkloadEvidence(tx *sql.Tx, result RemoteCIWorkloadResult) error {
	evidence, err := loadSQLiteReusableWorkloadEvidence(tx, result)
	if err != nil {
		return err
	}
	if err := validateReusableWorkloadEvidenceBinding(evidence, result); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("reused workload result %q origin %q evidence proof: %w", result.Identity.WorkloadID, evidence.OriginJobID, err)
	}
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return fmt.Errorf("load reused workload evidence accepted generation: %w", err)
	}
	origin, err := loadWorkloadPassEvidenceBaseOriginContext(tx, evidence, currentGeneration, nil)
	if err != nil {
		return fmt.Errorf("load reused workload evidence origin %q: %w", result.OriginJobID, err)
	}
	if err := validateStoredWorkloadPassEvidenceBase(tx, origin, evidence); err != nil {
		return fmt.Errorf("reused workload result %q origin proof: %w", result.Identity.WorkloadID, err)
	}
	return nil
}
