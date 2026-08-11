package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// RemoteCIRunAuthorityIdentity 将最终化操作绑定到不可变的 CI 运行记录。
type RemoteCIRunAuthorityIdentity struct {
	JobID              string
	AgentTokenDigest   string
	Force              bool
	Entrypoint         CIEntrypointID
	Profile            Profile
	PlanDigest         string
	CatalogDigest      string
	AcceptedGeneration uint64
	// Scope is the typed execution scope bound to the provisional run. Nil is
	// legacy/full and never causes a side-table row to be synthesized.
	Scope                        *RemoteCIExecutionScope
	ImageCacheSnapshotID         string
	SourceTreeSHA                string
	CandidateGateSourceSHA256    string
	CandidateGateToolchainSHA256 string
	RunnerImage                  string
	StartedAt                    time.Time
	// WorkloadPassIdentities 是 finalization 调用方从冻结 RunResult 携带的
	// 内容身份快照；它不写入 SQLite，也不改变 PASS identity 本身。
	WorkloadPassIdentities []WorkloadPassIdentity
}

// FinalizeRemoteCIRunAuthorityWithSamples 在同一 SQLite 事务中追加样本与回执、验证提升资格、提升新鲜证据并完成最终 CAS。
func (store *DurationLedgerStore) FinalizeRemoteCIRunAuthorityWithSamples(identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample, promoteFresh bool) error {
	return store.finalizeRemoteCIRunAuthority(identity, receipts, samples, promoteFresh, nil)
}

// FinalizeRemoteCIRunAuthorityWithShardOverhead 在同一 SQLite 事务内最终化
// calibration run，并把已派生、已验证的 overhead aggregate/样本与 authority
// 一起提交。任一 overhead CAS 或约束失败都会回滚升权、回执和样本。
func (store *DurationLedgerStore) FinalizeRemoteCIRunAuthorityWithShardOverhead(
	identity RemoteCIRunAuthorityIdentity,
	receipts []CheckReceiptRecord,
	samples []DurationSample,
	promoteFresh bool,
	evidence ShardOrchestrationOverheadEvidence,
) error {
	if err := evidence.Validate(); err != nil {
		return fmt.Errorf("validate shard orchestration overhead evidence: %w", err)
	}
	if evidence.Overhead.AcceptedGeneration != identity.AcceptedGeneration || evidence.Overhead.AcceptedSnapshotID != identity.ImageCacheSnapshotID {
		return errors.New("shard orchestration overhead evidence does not match remote CI authority identity")
	}
	return store.finalizeRemoteCIRunAuthority(identity, receipts, samples, promoteFresh, &evidence)
}

// finalizeRemoteCIRunAuthority 统一维护普通最终化和带 overhead 的原子最终化事务。
func (store *DurationLedgerStore) finalizeRemoteCIRunAuthority(identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample, promoteFresh bool, evidence *ShardOrchestrationOverheadEvidence) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	identity.WorkloadPassIdentities = slices.Clone(identity.WorkloadPassIdentities)
	if err := validateWorkloadPassIdentities(identity.WorkloadPassIdentities); err != nil {
		return fmt.Errorf("validate remote CI authority workload identities: %w", err)
	}
	if err := validateRemoteCIRunAuthorityFinalization(identity, receipts, samples); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "finalize remote CI run authority", func(tx *sql.Tx) error {
		return finalizeSQLiteRemoteCIRunAuthority(tx, store, identity, receipts, samples, promoteFresh, evidence, store.finalizeFault)
	})
}

// validateRemoteCIRunAuthorityFinalization 在事务开始前拒绝不完整或不匹配的最终化输入。
func validateRemoteCIRunAuthorityFinalization(identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample) error {
	if _, err := validatePassingCheckReceiptCollection(receipts); err != nil {
		return err
	}
	if len(samples) != 0 {
		if err := ValidateDurationLedger(DurationLedger{Version: durationLedgerVersion, Samples: samples}); err != nil {
			return fmt.Errorf("validate finalized duration samples: %w", err)
		}
	}
	if receipts[0].JobID != identity.JobID || receipts[0].CandidateTreeSHA != identity.SourceTreeSHA || receipts[0].AgentTokenDigest != identity.AgentTokenDigest || receipts[0].Force != identity.Force || receipts[0].AcceptedGeneration != identity.AcceptedGeneration || receipts[0].AcceptedSnapshotID != identity.ImageCacheSnapshotID {
		return errors.New("check receipts do not match provisional remote CI run identity")
	}
	return nil
}

// finalizeSQLiteRemoteCIRunAuthority 在同一事务中按固定顺序完成权威提升及保留清理。
func finalizeSQLiteRemoteCIRunAuthority(tx *sql.Tx, store *DurationLedgerStore, identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample, promoteFresh bool, evidence *ShardOrchestrationOverheadEvidence, fault durationLedgerFinalizeFault) error {
	if store == nil {
		return errors.New("duration ledger store is required for finalization")
	}
	verifiedIdentities, err := validateSQLiteRemoteCIRunAuthorityInputs(tx, identity, receipts)
	if err != nil {
		return err
	}
	if err := appendSQLiteRemoteCIRunAuthorityArtifacts(tx, identity, receipts, samples, fault); err != nil {
		return err
	}
	if err := promoteSQLiteRemoteCIRunAuthorityAndOverhead(tx, identity, evidence, fault); err != nil {
		return err
	}
	if err := promoteSQLiteRemoteCIRunFreshEvidence(tx, identity.JobID, promoteFresh, verifiedIdentities, fault); err != nil {
		return err
	}
	return compactDurationLedgerAuthority(tx)
}

// validateSQLiteRemoteCIRunAuthorityInputs 在写入回执前重验 provisional run 与目录覆盖。
func validateSQLiteRemoteCIRunAuthorityInputs(tx *sql.Tx, identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord) (map[GateID]WorkloadPassIdentity, error) {
	stored, err := verifySQLiteProvisionalRemoteCIRunForAuthority(tx, identity)
	if err != nil {
		return nil, err
	}
	catalog, err := loadSQLiteWorkloadCatalog(tx, identity.CatalogDigest)
	if err != nil {
		return nil, fmt.Errorf("load remote CI workload catalog for authority identity: %w", err)
	}
	executionCatalog, err := ProjectRemoteCIExecutionCatalog(catalog.Catalog, identity.Scope)
	if err != nil {
		return nil, fmt.Errorf("project remote CI authority execution catalog: %w", err)
	}
	if err := validateWorkloadCatalogPassingCheckReceipts(executionCatalog, receipts); err != nil {
		return nil, fmt.Errorf("validate remote CI catalog check receipts: %w", err)
	}
	verified, err := validateSQLiteRemoteCIRunWorkloadPassIdentities(catalog.Catalog, stored.WorkloadResults, identity.WorkloadPassIdentities)
	if err != nil {
		return nil, err
	}
	for _, result := range stored.WorkloadResults {
		if result.Disposition != WorkloadDispositionReused {
			continue
		}
		if err := verifySQLiteRetainedWorkloadPassProof(tx, stored.JobID, result); err != nil {
			return nil, fmt.Errorf("validate reused workload %q before authority CAS: %w", result.Identity.WorkloadID, err)
		}
	}
	return verified, nil
}

// promoteSQLiteRemoteCIRunAuthorityAndOverhead 在同一事务内完成 authority CAS
// 后的 overhead 写入；事务外不可观察到中间状态。
func promoteSQLiteRemoteCIRunAuthorityAndOverhead(tx *sql.Tx, identity RemoteCIRunAuthorityIdentity, evidence *ShardOrchestrationOverheadEvidence, fault durationLedgerFinalizeFault) error {
	if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepCAS); err != nil {
		return err
	}
	if err := promoteSQLiteRemoteCIRunAuthorityCAS(tx, identity.JobID); err != nil {
		return err
	}
	return writeSQLiteShardOverheadIfPresent(tx, evidence, fault)
}

// writeSQLiteShardOverheadIfPresent 将已验证 overhead 绑定当前账本 generation 写入事务。
func writeSQLiteShardOverheadIfPresent(tx *sql.Tx, evidence *ShardOrchestrationOverheadEvidence, fault durationLedgerFinalizeFault) error {
	if evidence == nil {
		return nil
	}
	if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepShardOverhead); err != nil {
		return err
	}
	generation, exists, err := sqliteCurrentGeneration(tx)
	if err != nil {
		return err
	}
	if !exists {
		return ErrDurationLedgerMetadataMissing
	}
	if err := writeSQLiteShardOverheadCAS(tx, generation, evidence.Overhead, evidence.Samples); err != nil {
		return fmt.Errorf("write shard orchestration overhead in authority transaction: %w", err)
	}
	return nil
}

// promoteSQLiteRemoteCIRunFreshEvidence 在需要时把 fresh PASS 提升到复用证据。
func promoteSQLiteRemoteCIRunFreshEvidence(tx *sql.Tx, jobID string, promoteFresh bool, verifiedIdentities map[GateID]WorkloadPassIdentity, fault durationLedgerFinalizeFault) error {
	if !promoteFresh {
		return nil
	}
	if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepPromotion); err != nil {
		return err
	}
	return promoteSQLiteRemoteCIWorkloadPassEvidence(tx, jobID, verifiedIdentities)
}

// verifySQLiteProvisionalRemoteCIRunForAuthority 在提升前于事务内重新验证 provisional 运行记录。
func verifySQLiteProvisionalRemoteCIRunForAuthority(tx *sql.Tx, identity RemoteCIRunAuthorityIdentity) (RemoteCIRunRecord, error) {
	record := RemoteCIRunRecord{JobID: identity.JobID, AgentTokenDigest: identity.AgentTokenDigest, Force: identity.Force, Entrypoint: identity.Entrypoint, Profile: identity.Profile, PlanDigest: identity.PlanDigest, CatalogDigest: identity.CatalogDigest, AcceptedGeneration: identity.AcceptedGeneration, Scope: identity.Scope, ImageCacheSnapshotID: identity.ImageCacheSnapshotID, SourceTreeSHA: identity.SourceTreeSHA, CandidateGateSourceSHA256: identity.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: identity.CandidateGateToolchainSHA256, RunnerImage: identity.RunnerImage, StartedAt: identity.StartedAt}
	if err := verifySQLiteRemoteCIRunIdentity(tx, record); err != nil {
		return RemoteCIRunRecord{}, err
	}
	stored, err := loadRemoteCIRunRow(tx, identity.JobID)
	if err != nil {
		return RemoteCIRunRecord{}, err
	}
	if err := loadRemoteCIRunDetails(tx, identity.JobID, &stored); err != nil {
		return RemoteCIRunRecord{}, err
	}
	stored.Authoritative = true
	if err := validateSQLiteRemoteCIRunCatalogCoverage(tx, stored); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("validate provisional remote CI catalog coverage: %w", err)
	}
	if err := validateRemoteCIRunRecord(stored); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("validate provisional remote CI run for authority promotion: %w", err)
	}
	return stored, nil
}
