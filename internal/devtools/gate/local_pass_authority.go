package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const localWorkloadPassOriginJobPrefix = "local:"

// LocalWorkloadPassOrigin is the independently auditable local execution
// projection.  It is never an ECI run or a remote receipt.
type LocalWorkloadPassOrigin struct {
	RunID                  string
	LocalGeneration        uint64
	SourceTreeSHA          string
	CatalogDigest          string
	HostContextDigest      string
	ToolchainClosureDigest string
	RunnerSemanticPolicy   string
	RunnerSemanticDigest   string
	CPUWindowStart         time.Time
	CPUWindowEnd           time.Time
	CPUSampleCount         int
	CPUBusyAveragePercent  float64
	AvailableCPU           float64
	AvailableMemoryGiB     float64
	Status                 ResultStatus
	CleanupComplete        bool
	StartedAt              time.Time
	CompletedAt            time.Time
	ProjectionDigest       string
}

// LocalWorkloadPassEntry binds one direct local execution to its PASS identity.
type LocalWorkloadPassEntry struct {
	Identity    WorkloadPassIdentity
	Environment LocalWorkloadPassEnvironment
	Execution   PlanGateExecution
}

// LocalWorkloadPassBatch is the only writer input for local PASS authority.
// All origin and execution rows are committed in one SQLite transaction.
type LocalWorkloadPassBatch struct {
	Origin  LocalWorkloadPassOrigin
	Entries []LocalWorkloadPassEntry
}

type localWorkloadPassProjection struct {
	Domain                 string                   `json:"domain"`
	RunID                  string                   `json:"run_id"`
	LocalGeneration        uint64                   `json:"local_generation"`
	SourceTreeSHA          string                   `json:"source_tree_sha"`
	CatalogDigest          string                   `json:"catalog_digest"`
	HostContextDigest      string                   `json:"host_context_digest"`
	ToolchainClosureDigest string                   `json:"toolchain_closure_digest"`
	RunnerSemanticPolicy   string                   `json:"runner_semantic_policy"`
	RunnerSemanticDigest   string                   `json:"runner_semantic_digest"`
	CPUWindowStartUnixMS   int64                    `json:"cpu_window_start_unix_ms"`
	CPUWindowEndUnixMS     int64                    `json:"cpu_window_end_unix_ms"`
	CPUSampleCount         int                      `json:"cpu_sample_count"`
	CPUBusyAveragePercent  float64                  `json:"cpu_busy_average_percent"`
	AvailableCPU           float64                  `json:"available_cpu"`
	AvailableMemoryGiB     float64                  `json:"available_memory_gib"`
	Status                 ResultStatus             `json:"status"`
	CleanupComplete        bool                     `json:"cleanup_complete"`
	StartedAtUnixMS        int64                    `json:"started_at_unix_ms"`
	CompletedAtUnixMS      int64                    `json:"completed_at_unix_ms"`
	Entries                []LocalWorkloadPassEntry `json:"entries"`
}

// LocalWorkloadPassProjectionDigest computes the origin projection digest used
// by both the write and lookup paths; path, commit and worktree are not key data.
func LocalWorkloadPassProjectionDigest(origin LocalWorkloadPassOrigin, entries []LocalWorkloadPassEntry) (string, error) {
	canonical := append([]LocalWorkloadPassEntry(nil), entries...)
	sort.Slice(canonical, func(left, right int) bool {
		return canonical[left].Identity.WorkloadID < canonical[right].Identity.WorkloadID
	})
	payload, err := json.Marshal(localWorkloadPassProjection{
		Domain:                 "local-workload-pass-origin/v1",
		RunID:                  origin.RunID,
		LocalGeneration:        origin.LocalGeneration,
		SourceTreeSHA:          origin.SourceTreeSHA,
		CatalogDigest:          origin.CatalogDigest,
		HostContextDigest:      origin.HostContextDigest,
		ToolchainClosureDigest: origin.ToolchainClosureDigest,
		RunnerSemanticPolicy:   origin.RunnerSemanticPolicy,
		RunnerSemanticDigest:   origin.RunnerSemanticDigest,
		CPUWindowStartUnixMS:   origin.CPUWindowStart.UTC().UnixMilli(),
		CPUWindowEndUnixMS:     origin.CPUWindowEnd.UTC().UnixMilli(),
		CPUSampleCount:         origin.CPUSampleCount,
		CPUBusyAveragePercent:  origin.CPUBusyAveragePercent,
		AvailableCPU:           origin.AvailableCPU,
		AvailableMemoryGiB:     origin.AvailableMemoryGiB,
		Status:                 origin.Status,
		CleanupComplete:        origin.CleanupComplete,
		StartedAtUnixMS:        origin.StartedAt.UnixMilli(),
		CompletedAtUnixMS:      origin.CompletedAt.UnixMilli(),
		Entries:                canonical,
	})
	if err != nil {
		return "", fmt.Errorf("encode local workload PASS origin projection: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// RecordLocalWorkloadPassBatch promotes direct, green local executions into
// the local namespace.  Failed, timed-out or drifted executions must never be
// passed to this writer and therefore cannot become authority evidence.
func (store *DurationLedgerStore) RecordLocalWorkloadPassBatch(batch LocalWorkloadPassBatch) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if err := validateLocalWorkloadPassBatch(batch); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return mapDurationLedgerSQLiteError("begin local workload PASS authority transaction", err)
	}
	defer transaction.Rollback()
	if err := recordLocalWorkloadPassBatchInTransaction(transaction, batch); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit local workload PASS authority transaction", err)
	}
	return nil
}

func recordLocalWorkloadPassBatchInTransaction(transaction *sql.Tx, batch LocalWorkloadPassBatch) error {
	if err := validateLocalAcceptedGeneration(transaction, batch.Origin.LocalGeneration); err != nil {
		return err
	}
	if err := insertLocalWorkloadPassOrigin(transaction, batch.Origin); err != nil {
		return err
	}
	for _, entry := range batch.Entries {
		if err := insertLocalWorkloadExecution(transaction, batch.Origin, entry); err != nil {
			return err
		}
		if err := insertLocalWorkloadPassEvidence(transaction, batch.Origin, entry); err != nil {
			return err
		}
	}
	return nil
}

// LookupLocalWorkloadPassEvidence reads only the local namespace; it never
// aliases a remote evidence row or invokes ECI/remote origin validation.
func (store *DurationLedgerStore) LookupLocalWorkloadPassEvidence(identities []WorkloadPassIdentity) ([]WorkloadPassEvidence, error) {
	return store.lookupLocalWorkloadPassEvidenceWithStats(identities, nil)
}

func (store *DurationLedgerStore) lookupLocalWorkloadPassEvidenceWithStats(identities []WorkloadPassIdentity, stats *localWorkloadPassLookupStats) ([]WorkloadPassEvidence, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := validateWorkloadPassIdentities(identities); err != nil {
		return nil, err
	}
	if len(identities) == 0 {
		return []WorkloadPassEvidence{}, nil
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin local workload PASS lookup", err)
	}
	defer transaction.Rollback()
	stats.recordAuthorityTransaction()
	generation, err := currentLocalAuthorityGeneration(transaction)
	if err != nil {
		return nil, fmt.Errorf("load local workload PASS generation: %w", err)
	}
	rows, err := queryLocalWorkloadPassEvidenceWithStats(transaction, identities, generation, stats)
	if err != nil {
		return nil, err
	}
	result, err := validateAndOrderLocalWorkloadPassEvidenceWithStats(transaction, identities, generation, rows, stats)
	if err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit local workload PASS lookup", err)
	}
	return result, nil
}

func validateLocalWorkloadPassBatch(batch LocalWorkloadPassBatch) error {
	if err := validateLocalWorkloadPassOrigin(batch.Origin); err != nil {
		return err
	}
	if len(batch.Entries) == 0 {
		return errors.New("local workload PASS batch requires direct executions")
	}
	seen := make(map[GateID]struct{}, len(batch.Entries))
	for _, entry := range batch.Entries {
		if _, ok := seen[entry.Identity.WorkloadID]; ok {
			return fmt.Errorf("local workload PASS batch workload %q is duplicated", entry.Identity.WorkloadID)
		}
		seen[entry.Identity.WorkloadID] = struct{}{}
		if err := validateLocalWorkloadPassEntry(batch.Origin, entry); err != nil {
			return err
		}
	}
	expected, err := LocalWorkloadPassProjectionDigest(batch.Origin, batch.Entries)
	if err != nil {
		return err
	}
	if batch.Origin.ProjectionDigest != expected {
		return errors.New("local workload PASS origin projection digest does not match direct executions")
	}
	return nil
}

func validateLocalWorkloadPassOrigin(origin LocalWorkloadPassOrigin) error {
	if err := validateLocalOriginIdentifiers(origin); err != nil {
		return err
	}
	if err := validateLocalOriginDigests(origin); err != nil {
		return err
	}
	if err := validateLocalOriginAdmission(origin); err != nil {
		return err
	}
	return validateLocalOriginState(origin)
}

func validateLocalOriginAdmission(origin LocalWorkloadPassOrigin) error {
	admission := LocalHostAdmission{
		Allowed:               true,
		AvailableCPU:          origin.AvailableCPU,
		AvailableMemoryGiB:    origin.AvailableMemoryGiB,
		CPUWindowStart:        origin.CPUWindowStart,
		CPUWindowEnd:          origin.CPUWindowEnd,
		CPUSampleCount:        origin.CPUSampleCount,
		CPUBusyAveragePercent: origin.CPUBusyAveragePercent,
	}
	if err := ValidateLocalHostAdmissionObservation(admission); err != nil {
		return fmt.Errorf("local workload PASS origin host admission: %w", err)
	}
	if origin.CPUBusyAveragePercent > cicontract.LocalHostCPUBusyLimitPercent {
		return errors.New("local workload PASS origin CPU busy average exceeds local hard limit")
	}
	return nil
}

func validateLocalOriginIdentifiers(origin LocalWorkloadPassOrigin) error {
	if strings.TrimSpace(origin.RunID) == "" || strings.ContainsAny(origin.RunID, "\r\n") {
		return errors.New("local workload PASS origin run ID is required")
	}
	if origin.LocalGeneration == 0 {
		return errors.New("local workload PASS origin local generation is required")
	}
	if !validLocalSourceTreeSHA(origin.SourceTreeSHA) {
		return errors.New("local workload PASS origin source tree SHA is invalid")
	}
	return nil
}

func validateLocalOriginDigests(origin LocalWorkloadPassOrigin) error {
	for name, digest := range map[string]string{"catalog": origin.CatalogDigest, "host context": origin.HostContextDigest, "toolchain closure": origin.ToolchainClosureDigest, "runner semantic": origin.RunnerSemanticDigest, "projection": origin.ProjectionDigest} {
		if !isPrefixedSHA256Digest(digest) {
			return fmt.Errorf("local workload PASS origin %s digest is invalid", name)
		}
	}
	if origin.RunnerSemanticPolicy != LocalWorkloadRunnerSemanticPolicy {
		return errors.New("local workload PASS origin runner semantic policy is invalid")
	}
	return nil
}

func validateLocalOriginState(origin LocalWorkloadPassOrigin) error {
	if origin.Status != ResultStatusPassed || !origin.CleanupComplete {
		return errors.New("local workload PASS origin must be passed and cleaned")
	}
	if origin.StartedAt.IsZero() || origin.CompletedAt.IsZero() || origin.CompletedAt.Before(origin.StartedAt) {
		return errors.New("local workload PASS origin timing is invalid")
	}
	return nil
}

func validateLocalWorkloadPassEntry(origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) error {
	if err := validateWorkloadPassIdentity(entry.Identity); err != nil {
		return fmt.Errorf("local workload PASS identity: %w", err)
	}
	if err := validateLocalEntryEnvironment(origin, entry); err != nil {
		return err
	}
	if entry.Execution.GateID != entry.Identity.WorkloadID || entry.Execution.Status != ResultStatusPassed || entry.Execution.ExitCode != 0 {
		return fmt.Errorf("local workload PASS execution %q is not a direct green result", entry.Identity.WorkloadID)
	}
	if entry.Execution.ArgvDigest != entry.Identity.ExecutionDigest {
		return fmt.Errorf("local workload PASS execution %q argv digest drifted from identity", entry.Identity.WorkloadID)
	}
	if !strings.HasPrefix(entry.Execution.ShardIdentity, "local/") {
		return fmt.Errorf("local workload PASS execution %q lacks local shard identity", entry.Identity.WorkloadID)
	}
	if !validPlanGateResult(entry.Execution, ExecutorPlanReportSchemaVersion) {
		return fmt.Errorf("local workload PASS execution %q timing/profile proof is invalid", entry.Identity.WorkloadID)
	}
	return nil
}

func validateLocalEntryEnvironment(origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) error {
	if err := ValidateLocalWorkloadPassEnvironment(entry.Environment); err != nil {
		return err
	}
	digest, err := LocalWorkloadPassEnvironmentDigest(entry.Environment)
	if err != nil {
		return err
	}
	if digest != entry.Identity.EnvironmentDigest {
		return errors.New("local workload PASS identity environment digest does not match material")
	}
	hostContextDigest, err := LocalWorkloadPassHostContextDigest(entry.Environment)
	if err != nil {
		return err
	}
	if hostContextDigest != origin.HostContextDigest {
		return errors.New("local workload PASS entry host context drifted from origin")
	}
	if entry.Environment.ToolchainClosureDigest != origin.ToolchainClosureDigest || entry.Environment.RunnerSemanticPolicy != origin.RunnerSemanticPolicy || entry.Environment.BaseRunnerSemanticDigest != origin.RunnerSemanticDigest {
		return errors.New("local workload PASS entry base runner context drifted from origin")
	}
	if entry.Execution.ExecutionProfile.GoFlags != entry.Environment.GoFlags {
		return errors.New("local workload PASS execution GoFlags drifted from environment material")
	}
	return nil
}

func validLocalSourceTreeSHA(value string) bool {
	if len(value) == 40 {
		for _, char := range value {
			if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
				return false
			}
		}
		return true
	}
	return isPrefixedSHA256Digest(value)
}

func validateLocalAcceptedGeneration(transaction *sql.Tx, generation uint64) error {
	return requireLocalAuthorityGeneration(transaction, generation)
}

func insertLocalWorkloadPassOrigin(tx *sql.Tx, origin LocalWorkloadPassOrigin) error {
	_, err := tx.Exec(`INSERT INTO ci_local_workload_origins (run_id, authority_kind, local_generation, source_tree_sha, catalog_digest, host_context_digest, toolchain_closure_digest, runner_semantic_policy, runner_semantic_digest, cpu_window_start_unix_ms, cpu_window_end_unix_ms, cpu_sample_count, cpu_busy_average_percent, available_cpu, available_memory_gib, status, cleanup_complete, started_at_unix_ms, completed_at_unix_ms, projection_digest) VALUES (?, 'local-canonical', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'passed', 1, ?, ?, ?)`, origin.RunID, origin.LocalGeneration, origin.SourceTreeSHA, origin.CatalogDigest, origin.HostContextDigest, origin.ToolchainClosureDigest, origin.RunnerSemanticPolicy, origin.RunnerSemanticDigest, origin.CPUWindowStart.UnixMilli(), origin.CPUWindowEnd.UnixMilli(), origin.CPUSampleCount, origin.CPUBusyAveragePercent, origin.AvailableCPU, origin.AvailableMemoryGiB, origin.StartedAt.UnixMilli(), origin.CompletedAt.UnixMilli(), origin.ProjectionDigest)
	if err == nil {
		return nil
	}
	if isSQLiteConstraintError(err) && localWorkloadPassOriginMatches(tx, origin) {
		return nil
	}
	return mapDurationLedgerSQLiteError("insert local workload PASS origin", err)
}

func localWorkloadPassOriginMatches(tx *sql.Tx, origin LocalWorkloadPassOrigin) bool {
	var generation, sourceTree, catalog, hostContext, toolchain, policy, runner, status, projection string
	var cleanup, started, completed, cpuWindowStart, cpuWindowEnd, cpuSamples int64
	var cpuBusyAverage, availableCPU, availableMemory float64
	err := tx.QueryRow(`SELECT local_generation, source_tree_sha, catalog_digest, host_context_digest, toolchain_closure_digest, runner_semantic_policy, runner_semantic_digest, cpu_window_start_unix_ms, cpu_window_end_unix_ms, cpu_sample_count, cpu_busy_average_percent, available_cpu, available_memory_gib, status, cleanup_complete, started_at_unix_ms, completed_at_unix_ms, projection_digest FROM ci_local_workload_origins WHERE run_id = ? AND authority_kind = 'local-canonical'`, origin.RunID).Scan(&generation, &sourceTree, &catalog, &hostContext, &toolchain, &policy, &runner, &cpuWindowStart, &cpuWindowEnd, &cpuSamples, &cpuBusyAverage, &availableCPU, &availableMemory, &status, &cleanup, &started, &completed, &projection)
	if err != nil {
		return false
	}
	return localOriginStoredFieldsMatch(generation, sourceTree, catalog, hostContext, toolchain, policy, runner, cpuWindowStart, cpuWindowEnd, cpuSamples, cpuBusyAverage, availableCPU, availableMemory, status, cleanup, started, completed, projection, origin)
}

func localOriginStoredFieldsMatch(generation, sourceTree, catalog, hostContext, toolchain, policy, runner string, cpuWindowStart, cpuWindowEnd, cpuSamples int64, cpuBusyAverage, availableCPU, availableMemory float64, status string, cleanup, started, completed int64, projection string, origin LocalWorkloadPassOrigin) bool {
	return localOriginIdentityFieldsMatch(generation, sourceTree, catalog, hostContext, origin) && localOriginAdmissionFieldsMatch(cpuWindowStart, cpuWindowEnd, cpuSamples, cpuBusyAverage, availableCPU, availableMemory, origin) && localOriginExecutionFieldsMatch(toolchain, policy, runner, status, cleanup, started, completed, projection, origin)
}

func localOriginAdmissionFieldsMatch(cpuWindowStart, cpuWindowEnd, cpuSamples int64, cpuBusyAverage, availableCPU, availableMemory float64, origin LocalWorkloadPassOrigin) bool {
	return cpuWindowStart == origin.CPUWindowStart.UnixMilli() && cpuWindowEnd == origin.CPUWindowEnd.UnixMilli() && cpuSamples == int64(origin.CPUSampleCount) && cpuBusyAverage == origin.CPUBusyAveragePercent && availableCPU == origin.AvailableCPU && availableMemory == origin.AvailableMemoryGiB
}

func localOriginIdentityFieldsMatch(generation, sourceTree, catalog, hostContext string, origin LocalWorkloadPassOrigin) bool {
	return generation == fmt.Sprint(origin.LocalGeneration) && sourceTree == origin.SourceTreeSHA && catalog == origin.CatalogDigest && hostContext == origin.HostContextDigest
}

func localOriginExecutionFieldsMatch(toolchain, policy, runner, status string, cleanup, started, completed int64, projection string, origin LocalWorkloadPassOrigin) bool {
	return toolchain == origin.ToolchainClosureDigest && policy == origin.RunnerSemanticPolicy && runner == origin.RunnerSemanticDigest && status == string(origin.Status) && cleanup == 1 && started == origin.StartedAt.UnixMilli() && completed == origin.CompletedAt.UnixMilli() && projection == origin.ProjectionDigest
}

func insertLocalWorkloadExecution(tx *sql.Tx, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) error {
	encoded, err := json.Marshal(entry.Execution)
	if err != nil {
		return fmt.Errorf("encode local workload execution: %w", err)
	}
	environmentJSON, err := json.Marshal(entry.Environment)
	if err != nil {
		return fmt.Errorf("encode local workload environment: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO ci_local_workload_executions (run_id, workload_id, local_generation, identity_digest, execution_digest, input_digest, environment_digest, status, exit_code, started_at_unix_ms, completed_at_unix_ms, environment_json, execution_json) VALUES (?, ?, ?, ?, ?, ?, ?, 'passed', 0, ?, ?, ?, ?)`, origin.RunID, entry.Identity.WorkloadID, origin.LocalGeneration, entry.Identity.IdentityDigest, entry.Identity.ExecutionDigest, entry.Identity.InputDigest, entry.Identity.EnvironmentDigest, entry.Execution.StartedAt.UnixMilli(), entry.Execution.CompletedAt.UnixMilli(), string(environmentJSON), string(encoded))
	if err != nil {
		if isSQLiteConstraintError(err) && localWorkloadExecutionMatches(tx, origin, entry, string(encoded)) {
			return nil
		}
		return mapDurationLedgerSQLiteError("insert local workload execution", err)
	}
	return nil
}

func localWorkloadExecutionMatches(tx *sql.Tx, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry, encoded string) bool {
	var identity, execution, input, environment, status, stored, storedEnvironment string
	var generation string
	var exitCode, started, completed int64
	err := tx.QueryRow(`SELECT local_generation, identity_digest, execution_digest, input_digest, environment_digest, status, exit_code, started_at_unix_ms, completed_at_unix_ms, environment_json, execution_json FROM ci_local_workload_executions WHERE run_id = ? AND workload_id = ?`, origin.RunID, entry.Identity.WorkloadID).Scan(&generation, &identity, &execution, &input, &environment, &status, &exitCode, &started, &completed, &storedEnvironment, &stored)
	if err != nil {
		return false
	}
	environmentJSON, err := json.Marshal(entry.Environment)
	if err != nil {
		return false
	}
	return localExecutionStoredFieldsMatch(generation, identity, execution, input, environment, status, exitCode, started, completed, storedEnvironment, stored, origin, entry, string(environmentJSON), encoded)
}

func localExecutionStoredFieldsMatch(generation, identity, execution, input, environment, status string, exitCode, started, completed int64, storedEnvironment, stored string, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry, environmentJSON, encoded string) bool {
	return localExecutionIdentityFieldsMatch(generation, identity, execution, input, environment, origin, entry) && localExecutionTimingFieldsMatch(status, exitCode, started, completed, storedEnvironment, stored, entry, environmentJSON, encoded)
}

func localExecutionIdentityFieldsMatch(generation, identity, execution, input, environment string, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) bool {
	return generation == fmt.Sprint(origin.LocalGeneration) && identity == entry.Identity.IdentityDigest && execution == entry.Identity.ExecutionDigest && input == entry.Identity.InputDigest && environment == entry.Identity.EnvironmentDigest
}

func localExecutionTimingFieldsMatch(status string, exitCode, started, completed int64, storedEnvironment, stored string, entry LocalWorkloadPassEntry, environmentJSON, encoded string) bool {
	return status == string(ResultStatusPassed) && exitCode == 0 && started == entry.Execution.StartedAt.UnixMilli() && completed == entry.Execution.CompletedAt.UnixMilli() && storedEnvironment == environmentJSON && stored == encoded
}

func insertLocalWorkloadPassEvidence(tx *sql.Tx, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) error {
	evidence := WorkloadPassEvidence{Identity: entry.Identity, OriginJobID: localWorkloadPassOriginJobPrefix + origin.RunID, OriginAcceptedGeneration: origin.LocalGeneration, OriginSourceTreeSHA: origin.SourceTreeSHA, OriginReceiptSetSHA256: origin.ProjectionDigest, OriginExecution: entry.Execution}
	var err error
	evidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(entry.Execution)
	if err != nil {
		return fmt.Errorf("encode local workload PASS evidence execution: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO ci_local_workload_pass_evidence (namespace, identity_digest, local_generation, workload_id, execution_digest, input_digest, environment_digest, origin_local_run_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES ('local', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, entry.Identity.IdentityDigest, origin.LocalGeneration, entry.Identity.WorkloadID, entry.Identity.ExecutionDigest, entry.Identity.InputDigest, entry.Identity.EnvironmentDigest, origin.RunID, origin.SourceTreeSHA, origin.ProjectionDigest, string(encoded), evidence.EvidenceSHA256)
	if err != nil {
		if isSQLiteConstraintError(err) && localWorkloadPassEvidenceMatches(tx, origin, entry, string(encoded), evidence.EvidenceSHA256) {
			return nil
		}
		return mapDurationLedgerSQLiteError("insert local workload PASS evidence", err)
	}
	return nil
}

func localWorkloadPassEvidenceMatches(tx *sql.Tx, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry, encoded, evidenceDigest string) bool {
	var namespace, generation, workloadID, identity, execution, input, environment, originID, tree, projection, storedExecution, storedEvidence string
	err := tx.QueryRow(`SELECT namespace, local_generation, workload_id, identity_digest, execution_digest, input_digest, environment_digest, origin_local_run_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256 FROM ci_local_workload_pass_evidence WHERE identity_digest = ? AND local_generation = ?`, entry.Identity.IdentityDigest, origin.LocalGeneration).Scan(&namespace, &generation, &workloadID, &identity, &execution, &input, &environment, &originID, &tree, &projection, &storedExecution, &storedEvidence)
	if err != nil {
		return false
	}
	return localEvidenceStoredFieldsMatch(namespace, generation, workloadID, identity, execution, input, environment, originID, tree, projection, storedExecution, storedEvidence, origin, entry, encoded, evidenceDigest)
}

func localEvidenceStoredFieldsMatch(namespace, generation, workloadID, identity, execution, input, environment, originID, tree, projection, storedExecution, storedEvidence string, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry, encoded, evidenceDigest string) bool {
	return localEvidenceIdentityFieldsMatch(namespace, generation, workloadID, identity, execution, input, environment, origin, entry) && localEvidenceOriginFieldsMatch(originID, tree, projection, storedExecution, storedEvidence, origin, encoded, evidenceDigest)
}

func localEvidenceIdentityFieldsMatch(namespace, generation, workloadID, identity, execution, input, environment string, origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) bool {
	return namespace == string(WorkloadPassNamespaceLocal) && generation == fmt.Sprint(origin.LocalGeneration) && workloadID == string(entry.Identity.WorkloadID) && identity == entry.Identity.IdentityDigest && execution == entry.Identity.ExecutionDigest && input == entry.Identity.InputDigest && environment == entry.Identity.EnvironmentDigest
}

func localEvidenceOriginFieldsMatch(originID, tree, projection, storedExecution, storedEvidence string, origin LocalWorkloadPassOrigin, encoded, evidenceDigest string) bool {
	return originID == origin.RunID && tree == origin.SourceTreeSHA && projection == origin.ProjectionDigest && storedExecution == encoded && storedEvidence == evidenceDigest
}

type localStoredWorkloadPassEvidence struct {
	evidence WorkloadPassEvidence
	originID string
}

func queryLocalWorkloadPassEvidenceWithStats(tx *sql.Tx, identities []WorkloadPassIdentity, generation uint64, stats *localWorkloadPassLookupStats) ([]localStoredWorkloadPassEvidence, error) {
	result := make([]localStoredWorkloadPassEvidence, 0, len(identities))
	for start := 0; start < len(identities); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(identities))
		batch, err := queryLocalWorkloadPassEvidenceBatchWithStats(tx, identities[start:end], generation, stats)
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
	}
	return result, nil
}

func queryLocalWorkloadPassEvidenceBatchWithStats(tx *sql.Tx, identities []WorkloadPassIdentity, generation uint64, stats *localWorkloadPassLookupStats) ([]localStoredWorkloadPassEvidence, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(identities)), ",")
	args := make([]any, 0, len(identities)+3)
	for _, identity := range identities {
		args = append(args, identity.IdentityDigest)
	}
	for _, retained := range retainedWorkloadPassGenerations(generation) {
		args = append(args, retained)
	}
	rows, err := tx.Query(`SELECT namespace, identity_digest, local_generation, workload_id, execution_digest, input_digest, environment_digest, origin_local_run_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256 FROM ci_local_workload_pass_evidence WHERE namespace = 'local' AND identity_digest IN (`+placeholders+`) AND local_generation IN (?, ?, ?) ORDER BY identity_digest, length(local_generation) DESC, local_generation DESC`, args...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query local workload PASS evidence", err)
	}
	defer rows.Close()
	stats.recordIdentityBatchQuery()
	requested := make(map[string]WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		requested[identity.IdentityDigest] = identity
	}
	result := make([]localStoredWorkloadPassEvidence, 0, len(identities))
	seen := make(map[string]struct{}, len(identities))
	for rows.Next() {
		stored, err := scanLocalWorkloadPassEvidence(rows, requested)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[stored.evidence.Identity.IdentityDigest]; ok {
			continue
		}
		seen[stored.evidence.Identity.IdentityDigest] = struct{}{}
		result = append(result, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate local workload PASS evidence", err)
	}
	return result, nil
}

func scanLocalWorkloadPassEvidence(rows *sql.Rows, requested map[string]WorkloadPassIdentity) (localStoredWorkloadPassEvidence, error) {
	var namespace, identityDigest, generation, workloadID, executionDigest, inputDigest, environmentDigest, originID, sourceTree, projectionDigest, executionJSON, evidenceDigest string
	if err := rows.Scan(&namespace, &identityDigest, &generation, &workloadID, &executionDigest, &inputDigest, &environmentDigest, &originID, &sourceTree, &projectionDigest, &executionJSON, &evidenceDigest); err != nil {
		return localStoredWorkloadPassEvidence{}, mapDurationLedgerSQLiteError("scan local workload PASS evidence", err)
	}
	if namespace != string(WorkloadPassNamespaceLocal) {
		return localStoredWorkloadPassEvidence{}, errors.New("local workload PASS evidence namespace is invalid")
	}
	requestedIdentity, ok := requested[identityDigest]
	if !ok {
		return localStoredWorkloadPassEvidence{}, errors.New("local workload PASS evidence returned an unrequested identity")
	}
	parsedGeneration, err := parseLocalAcceptedGeneration(generation)
	if err != nil {
		return localStoredWorkloadPassEvidence{}, err
	}
	evidence := WorkloadPassEvidence{Identity: WorkloadPassIdentity{IdentityDigest: identityDigest, WorkloadID: GateID(workloadID), ExecutionDigest: executionDigest, InputDigest: inputDigest, EnvironmentDigest: environmentDigest}, OriginJobID: localWorkloadPassOriginJobPrefix + originID, OriginAcceptedGeneration: parsedGeneration, OriginSourceTreeSHA: sourceTree, OriginReceiptSetSHA256: projectionDigest, EvidenceSHA256: evidenceDigest}
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return localStoredWorkloadPassEvidence{}, fmt.Errorf("decode local workload PASS execution: %w", err)
	}
	if !workloadPassIdentityMatches(evidence.Identity, requestedIdentity) {
		return localStoredWorkloadPassEvidence{}, errors.New("local workload PASS evidence identity does not match lookup request")
	}
	return localStoredWorkloadPassEvidence{evidence: evidence, originID: originID}, nil
}

func parseLocalAcceptedGeneration(value string) (uint64, error) {
	parsed, err := parseUintCanonical(value)
	if err != nil || parsed == 0 {
		return 0, errors.New("local workload PASS accepted generation is invalid")
	}
	return parsed, nil
}

func parseUintCanonical(value string) (uint64, error) {
	var parsed uint64
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("canonical uint is invalid")
	}
	for _, char := range value {
		if char < '0' || char > '9' || parsed > (^uint64(0)-uint64(char-'0'))/10 {
			return 0, errors.New("canonical uint is invalid")
		}
		parsed = parsed*10 + uint64(char-'0')
	}
	return parsed, nil
}

func validateAndOrderLocalWorkloadPassEvidenceWithStats(tx *sql.Tx, identities []WorkloadPassIdentity, generation uint64, rows []localStoredWorkloadPassEvidence, stats *localWorkloadPassLookupStats) ([]WorkloadPassEvidence, error) {
	byIdentity := make(map[string]localStoredWorkloadPassEvidence, len(rows))
	originIDs := make([]string, 0, len(rows))
	seenOrigins := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seenOrigins[row.originID]; !exists {
			seenOrigins[row.originID] = struct{}{}
			originIDs = append(originIDs, row.originID)
		}
	}
	origins, err := loadLocalWorkloadPassOriginsBatch(tx, originIDs, stats)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		origin, exists := origins[row.originID]
		if !exists {
			return nil, fmt.Errorf("local workload PASS origin %q is missing", row.originID)
		}
		if err := validateLocalStoredWorkloadPassEvidence(generation, row, origin.origin, origin.entries); err != nil {
			return nil, err
		}
		byIdentity[row.evidence.Identity.IdentityDigest] = row
	}
	result := make([]WorkloadPassEvidence, 0, len(rows))
	for _, identity := range identities {
		if row, ok := byIdentity[identity.IdentityDigest]; ok {
			result = append(result, row.evidence)
		}
	}
	return result, nil
}

type localWorkloadPassOriginCache struct {
	origin  LocalWorkloadPassOrigin
	entries []LocalWorkloadPassEntry
	err     error
}

func validateLocalStoredWorkloadPassEvidence(generation uint64, row localStoredWorkloadPassEvidence, origin LocalWorkloadPassOrigin, entries []LocalWorkloadPassEntry) error {
	if err := validateWorkloadPassEvidence(row.evidence); err != nil {
		return fmt.Errorf("local workload PASS evidence: %w", err)
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(generation, row.evidence.OriginAcceptedGeneration); err != nil {
		return err
	}
	if err := validateLocalStoredEvidenceBinding(row.evidence, origin); err != nil {
		return err
	}
	if err := validateLocalEvidenceMatchesOriginExecution(row.evidence, entries); err != nil {
		return err
	}
	if err := validateLocalStoredEvidenceProjection(origin, entries); err != nil {
		return err
	}
	return validateLocalStoredEvidenceDigest(row.evidence)
}

func validateLocalEvidenceMatchesOriginExecution(evidence WorkloadPassEvidence, entries []LocalWorkloadPassEntry) error {
	for _, entry := range entries {
		if entry.Identity.WorkloadID != evidence.Identity.WorkloadID {
			continue
		}
		if !workloadPassIdentityMatches(entry.Identity, evidence.Identity) {
			return errors.New("local workload PASS evidence identity diverges from origin execution")
		}
		if !localPlanGateExecutionMatches(entry.Execution, evidence.OriginExecution) {
			return errors.New("local workload PASS evidence execution diverges from origin execution")
		}
		return nil
	}
	return errors.New("local workload PASS evidence workload is absent from origin executions")
}

func localPlanGateExecutionMatches(left, right PlanGateExecution) bool {
	leftEncoded, leftErr := json.Marshal(left)
	rightEncoded, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftEncoded) == string(rightEncoded)
}

func validateLocalStoredEvidenceBinding(evidence WorkloadPassEvidence, origin LocalWorkloadPassOrigin) error {
	if evidence.OriginAcceptedGeneration != origin.LocalGeneration {
		return errors.New("local workload PASS evidence generation drifted")
	}
	if evidence.OriginSourceTreeSHA != origin.SourceTreeSHA {
		return errors.New("local workload PASS evidence source tree drifted")
	}
	if evidence.OriginReceiptSetSHA256 != origin.ProjectionDigest {
		return errors.New("local workload PASS evidence origin projection drifted")
	}
	if evidence.OriginJobID != localWorkloadPassOriginJobPrefix+origin.RunID {
		return errors.New("local workload PASS evidence origin is not local")
	}
	return nil
}

func validateLocalStoredEvidenceProjection(origin LocalWorkloadPassOrigin, entries []LocalWorkloadPassEntry) error {
	expected, err := LocalWorkloadPassProjectionDigest(origin, entries)
	if err != nil {
		return err
	}
	if expected != origin.ProjectionDigest {
		return errors.New("local workload PASS origin projection digest is invalid")
	}
	return nil
}

func validateLocalStoredEvidenceDigest(evidence WorkloadPassEvidence) error {
	expected, err := WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		return err
	}
	if expected != evidence.EvidenceSHA256 {
		return errors.New("local workload PASS evidence digest is invalid")
	}
	return nil
}

func decodeLocalWorkloadPassEnvironmentJSON(encoded string, target *LocalWorkloadPassEnvironment) error {
	if target == nil {
		return errors.New("local workload PASS environment destination is nil")
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("local workload PASS environment JSON has trailing value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("local workload PASS environment JSON has trailing data: %w", err)
	}
	return nil
}

func validateLocalExecutionColumns(execution PlanGateExecution, status string, exitCode, startedAt, completedAt int64) error {
	if status != string(ResultStatusPassed) || exitCode != 0 {
		return errors.New("local workload PASS execution columns are not passing")
	}
	if execution.Status != ResultStatusPassed || execution.ExitCode != 0 {
		return errors.New("local workload PASS execution JSON is not passing")
	}
	if execution.StartedAt.UnixMilli() != startedAt || execution.CompletedAt.UnixMilli() != completedAt {
		return errors.New("local workload PASS execution timing columns diverge from JSON")
	}
	return nil
}
