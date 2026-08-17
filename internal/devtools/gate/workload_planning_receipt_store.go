package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// WorkloadPlanningReceiptEligibility identifies an optional receipt projection.
type WorkloadPlanningReceiptEligibility string

const (
	WorkloadPlanningReceiptEligibilityMissing     WorkloadPlanningReceiptEligibility = "missing"
	WorkloadPlanningReceiptEligibilityPlaceholder WorkloadPlanningReceiptEligibility = "placeholder"
	WorkloadPlanningReceiptEligibilityPresent     WorkloadPlanningReceiptEligibility = "present"
)

// WorkloadPlanningReceiptReadback is the canonical receipt and its stored projection.
type WorkloadPlanningReceiptReadback struct {
	Eligibility          WorkloadPlanningReceiptEligibility
	Status               cicontract.WorkloadPlanningReceiptStatus
	Reason               cicontract.WorkloadPlanningReceiptReason
	Receipt              *WorkloadPlanningReceipt
	ReceiptBytes         []byte
	ReceiptDigest        string
	MappingRoot          string
	PlannedWorkloadCount int
	PlannedShardCount    int
}

// InitializeWorkloadPlanningReceipt 为远程任务插入唯一的规划前占位回执。
func (store *DurationLedgerStore) InitializeWorkloadPlanningReceipt(jobID, gatePlanDigest, catalogDigest string, ledgerGeneration uint64) (WorkloadPlanningReceiptReadback, error) {
	if err := validatePlanningReceiptAnchor(jobID, gatePlanDigest, catalogDigest, ledgerGeneration); err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	receipt := WorkloadPlanningReceipt{Status: cicontract.WorkloadPlanningReceiptStatusNotApplicable, NotApplicable: &WorkloadPlanningReceiptNotApplicable{Reason: cicontract.WorkloadPlanningReceiptReasonPrePlanningFailure, GatePlanDigest: gatePlanDigest, CatalogDigest: catalogDigest, LedgerGeneration: ledgerGeneration}}
	return store.writePlanningReceipt(jobID, receipt, gatePlanDigest, catalogDigest, ledgerGeneration, true)
}

// TransitionWorkloadPlanningReceipt 执行唯一的占位态到终态 CAS 转换。
func (store *DurationLedgerStore) TransitionWorkloadPlanningReceipt(jobID string, receipt WorkloadPlanningReceipt) (WorkloadPlanningReceiptReadback, error) {
	if store == nil {
		return WorkloadPlanningReceiptReadback{}, errors.New("duration ledger store is nil")
	}
	if err := validatePlanningReceiptTransition(jobID, receipt); err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	gatePlanDigest, catalogDigest, generation, status, reason, root, workloads, shards := planningReceiptColumns(receipt)
	encoded, err := EncodeCanonicalWorkloadPlanningReceipt(receipt)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	digest, err := WorkloadPlanningReceiptDigest(receipt)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	defer database.Close()
	var readback WorkloadPlanningReceiptReadback
	err = withSQLiteWriteTransaction(database, "transition planning receipt", func(transaction *sql.Tx) error {
		result, execErr := transaction.Exec(`UPDATE ci_remote_run_planning_receipts SET status = ?, reason = ?, receipt_json = ?, receipt_digest = ?, mapping_root = ?, planned_workload_count = ?, planned_shard_count = ? WHERE job_id = ? AND status = 'not_applicable' AND reason = 'pre_planning_failure' AND gate_plan_digest = ? AND catalog_digest = ? AND ledger_generation = ?`, status, reason, string(encoded), digest, root, workloads, shards, jobID, gatePlanDigest, catalogDigest, strconv.FormatUint(generation, 10))
		if execErr != nil {
			return mapDurationLedgerSQLiteError("transition planning receipt", execErr)
		}
		affected, countErr := result.RowsAffected()
		if countErr != nil {
			return fmt.Errorf("read planning receipt CAS count: %w", countErr)
		}
		if affected != 1 {
			return planningReceiptCASFailure(transaction, jobID)
		}
		var err error
		readback, err = loadSQLitePlanningReceipt(transaction, jobID)
		return err
	})
	return readback, err
}

// validatePlanningReceiptTransition 校验终态回执及其占位状态转换边界。
func validatePlanningReceiptTransition(jobID string, receipt WorkloadPlanningReceipt) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("remote CI job ID is required")
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	_, _, _, _, reason, _, _, _ := planningReceiptColumns(receipt)
	if reason == string(cicontract.WorkloadPlanningReceiptReasonPrePlanningFailure) {
		return errors.New("planning receipt transition cannot retain pre-planning placeholder")
	}
	return nil
}

// LoadWorkloadPlanningReceipt 返回显式的缺失、占位或有效资格状态。
func (store *DurationLedgerStore) LoadWorkloadPlanningReceipt(jobID string) (WorkloadPlanningReceiptReadback, error) {
	if store == nil {
		return WorkloadPlanningReceiptReadback{}, errors.New("duration ledger store is nil")
	}
	if strings.TrimSpace(jobID) == "" {
		return WorkloadPlanningReceiptReadback{}, errors.New("remote CI job ID is required")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, mapDurationLedgerSQLiteError("begin planning receipt read", err)
	}
	defer transaction.Rollback()
	readback, err := loadSQLitePlanningReceipt(transaction, jobID)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	if err := transaction.Commit(); err != nil {
		return WorkloadPlanningReceiptReadback{}, mapDurationLedgerSQLiteError("commit planning receipt read", err)
	}
	return readback, nil
}

// writePlanningReceipt 在事务内写入规划回执占位记录并立即回读校验。
func (store *DurationLedgerStore) writePlanningReceipt(jobID string, receipt WorkloadPlanningReceipt, gatePlanDigest, catalogDigest string, generation uint64, initialize bool) (WorkloadPlanningReceiptReadback, error) {
	if store == nil {
		return WorkloadPlanningReceiptReadback{}, errors.New("duration ledger store is nil")
	}
	encoded, err := EncodeCanonicalWorkloadPlanningReceipt(receipt)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	digest, err := WorkloadPlanningReceiptDigest(receipt)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	status, reason, root, workloads, shards := planningReceiptStorage(receipt)
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	defer database.Close()
	var readback WorkloadPlanningReceiptReadback
	err = withSQLiteWriteTransaction(database, "initialize planning receipt", func(transaction *sql.Tx) error {
		if !initialize {
			return errors.New("planning receipt write is not an initialization")
		}
		if _, err := transaction.Exec(`INSERT INTO ci_remote_run_planning_receipts (job_id, status, reason, gate_plan_digest, catalog_digest, ledger_generation, receipt_json, receipt_digest, mapping_root, planned_workload_count, planned_shard_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobID, status, reason, gatePlanDigest, catalogDigest, strconv.FormatUint(generation, 10), string(encoded), digest, root, workloads, shards); err != nil {
			return mapDurationLedgerSQLiteError("insert planning receipt placeholder", err)
		}
		var err error
		readback, err = loadSQLitePlanningReceipt(transaction, jobID)
		return err
	})
	return readback, err
}

func validatePlanningReceiptAnchor(jobID, gatePlanDigest, catalogDigest string, generation uint64) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("remote CI job ID is required")
	}
	if err := cicontract.ValidateWorkloadPlanningReceiptDigest(gatePlanDigest); err != nil {
		return fmt.Errorf("gate plan digest: %w", err)
	}
	if err := cicontract.ValidateWorkloadPlanningReceiptDigest(catalogDigest); err != nil {
		return fmt.Errorf("catalog digest: %w", err)
	}
	if generation == 0 {
		return errors.New("planning receipt ledger generation must be positive")
	}
	return nil
}

func planningReceiptColumns(receipt WorkloadPlanningReceipt) (string, string, uint64, string, string, string, int, int) {
	if receipt.Status == cicontract.WorkloadPlanningReceiptStatusPlanned {
		p := receipt.Planned
		return p.GatePlanDigest, p.CatalogDigest, p.LedgerGeneration, string(receipt.Status), "", p.MappingRoot, p.PlannedWorkloadCount, p.PlannedShardCount
	}
	n := receipt.NotApplicable
	return n.GatePlanDigest, n.CatalogDigest, n.LedgerGeneration, string(receipt.Status), string(n.Reason), "", 0, 0
}

func planningReceiptStorage(receipt WorkloadPlanningReceipt) (string, string, string, int, int) {
	_, _, _, status, reason, root, workloads, shards := planningReceiptColumns(receipt)
	return status, reason, root, workloads, shards
}

func planningReceiptCASFailure(transaction *sql.Tx, jobID string) error {
	readback, err := loadSQLitePlanningReceipt(transaction, jobID)
	if err != nil {
		return err
	}
	if readback.Eligibility == WorkloadPlanningReceiptEligibilityMissing {
		return fmt.Errorf("planning receipt for job %q is missing", jobID)
	}
	return fmt.Errorf("planning receipt for job %q is no longer a placeholder", jobID)
}

type planningReceiptRow struct {
	status, reason, gate, catalog, generation, receiptJSON, receiptDigest, root string
	workloads, shards                                                           int
}

func loadSQLitePlanningReceipt(transaction *sql.Tx, jobID string) (WorkloadPlanningReceiptReadback, error) {
	row, missing, err := scanPlanningReceiptRow(transaction, jobID)
	if missing {
		return WorkloadPlanningReceiptReadback{Eligibility: WorkloadPlanningReceiptEligibilityMissing}, nil
	}
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	if err := validatePlanningReceiptRow(row.status, row.reason, row.gate, row.catalog, row.generation, row.receiptDigest, row.root, row.workloads, row.shards); err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	receipt, err := decodeStoredPlanningReceipt(row.receiptJSON, row.receiptDigest)
	if err != nil {
		return WorkloadPlanningReceiptReadback{}, err
	}
	return verifyStoredPlanningReceiptProjection(row, receipt)
}

func scanPlanningReceiptRow(transaction *sql.Tx, jobID string) (planningReceiptRow, bool, error) {
	var row planningReceiptRow
	err := transaction.QueryRow(`SELECT status, reason, gate_plan_digest, catalog_digest, ledger_generation, receipt_json, receipt_digest, mapping_root, planned_workload_count, planned_shard_count FROM ci_remote_run_planning_receipts WHERE job_id = ?`, jobID).Scan(&row.status, &row.reason, &row.gate, &row.catalog, &row.generation, &row.receiptJSON, &row.receiptDigest, &row.root, &row.workloads, &row.shards)
	if errors.Is(err, sql.ErrNoRows) {
		return planningReceiptRow{}, true, nil
	}
	if err != nil {
		return planningReceiptRow{}, false, mapDurationLedgerSQLiteError("load planning receipt row", err)
	}
	return row, false, nil
}

func decodeStoredPlanningReceipt(encoded, storedDigest string) (WorkloadPlanningReceipt, error) {
	receipt, err := DecodeCanonicalWorkloadPlanningReceipt([]byte(encoded))
	if err != nil {
		return WorkloadPlanningReceipt{}, fmt.Errorf("stored planning receipt JSON: %w", err)
	}
	digest, err := WorkloadPlanningReceiptDigest(receipt)
	if err != nil || digest != storedDigest {
		return WorkloadPlanningReceipt{}, errors.New("stored planning receipt digest drifted")
	}
	return receipt, nil
}

// verifyStoredPlanningReceiptProjection 校验规范回执与 SQLite 投影逐字段一致。
func verifyStoredPlanningReceiptProjection(row planningReceiptRow, receipt WorkloadPlanningReceipt) (WorkloadPlanningReceiptReadback, error) {
	gate, catalog, generation, status, reason, root, workloads, shards := planningReceiptColumns(receipt)
	if !storedPlanningReceiptProjectionMatches(row, gate, catalog, generation, status, reason, root, workloads, shards) {
		return WorkloadPlanningReceiptReadback{}, errors.New("stored planning receipt projection drifted")
	}
	eligibility := WorkloadPlanningReceiptEligibilityPresent
	if status == string(cicontract.WorkloadPlanningReceiptStatusNotApplicable) && reason == string(cicontract.WorkloadPlanningReceiptReasonPrePlanningFailure) {
		eligibility = WorkloadPlanningReceiptEligibilityPlaceholder
	}
	return WorkloadPlanningReceiptReadback{Eligibility: eligibility, Status: receipt.Status, Reason: cicontract.WorkloadPlanningReceiptReason(reason), Receipt: &receipt, ReceiptBytes: append([]byte(nil), row.receiptJSON...), ReceiptDigest: row.receiptDigest, MappingRoot: row.root, PlannedWorkloadCount: row.workloads, PlannedShardCount: row.shards}, nil
}

// storedPlanningReceiptProjectionMatches 比较回执规范字段与持久化投影。
func storedPlanningReceiptProjectionMatches(row planningReceiptRow, gate, catalog string, generation uint64, status, reason, root string, workloads, shards int) bool {
	return gate == row.gate && catalog == row.catalog && generationText(generation) == row.generation && status == row.status && reason == row.reason && root == row.root && workloads == row.workloads && shards == row.shards
}

func validatePlanningReceiptRow(status, reason, gate, catalog, generation, receiptDigest, root string, workloads, shards int) error {
	if err := validatePlanningReceiptStatusReason(status, reason); err != nil {
		return err
	}
	if err := validatePlanningReceiptDigests(gate, catalog, receiptDigest, root); err != nil {
		return err
	}
	return validatePlanningReceiptGeneration(generation, workloads, shards)
}

func validatePlanningReceiptStatusReason(status, reason string) error {
	if err := cicontract.ValidateWorkloadPlanningReceiptStatus(cicontract.WorkloadPlanningReceiptStatus(status)); err != nil {
		return err
	}
	if status == string(cicontract.WorkloadPlanningReceiptStatusNotApplicable) {
		if err := cicontract.ValidateWorkloadPlanningReceiptReason(cicontract.WorkloadPlanningReceiptReason(reason)); err != nil {
			return err
		}
	} else if reason != "" {
		return errors.New("planned planning receipt row has a reason")
	}
	return nil
}

func validatePlanningReceiptDigests(gate, catalog, receiptDigest, root string) error {
	for name, digest := range map[string]string{"gate_plan_digest": gate, "catalog_digest": catalog, "receipt_digest": receiptDigest} {
		if err := cicontract.ValidateWorkloadPlanningReceiptDigest(digest); err != nil {
			return fmt.Errorf("stored %s: %w", name, err)
		}
	}
	if root != "" {
		if err := cicontract.ValidateWorkloadPlanningReceiptDigest(root); err != nil {
			return fmt.Errorf("stored mapping root: %w", err)
		}
	}
	return nil
}

// validatePlanningReceiptGeneration 校验持久化代际与计数的规范表示。
func validatePlanningReceiptGeneration(generation string, workloads, shards int) error {
	parsed, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || parsed == 0 || generationText(parsed) != generation || workloads < 0 || shards < 0 {
		return errors.New("stored planning receipt row has invalid generation or counts")
	}
	return nil
}

func generationText(value uint64) string { return strconv.FormatUint(value, 10) }
