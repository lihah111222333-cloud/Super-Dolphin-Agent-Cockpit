package gate

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// ShardOrchestrationOverheadEvidence 是一次 calibration run 派生的、尚未
// 写入 SQLite 的 overhead aggregate 与逐分片样本。它必须与 run authority
// 在同一个最终化事务内提交，避免先升权后丢失 overhead。
type ShardOrchestrationOverheadEvidence struct {
	Overhead ShardOrchestrationOverhead
	Samples  []ShardOrchestrationOverheadSample
}

// Validate 校验证据的 aggregate、样本数量和 generation/provenance 绑定。
func (evidence ShardOrchestrationOverheadEvidence) Validate() error {
	return validateShardOverheadEvidence(evidence.Overhead, evidence.Samples)
}

// DeriveShardOrchestrationOverheadEvidence 从一个完整 run 的 timing 和资源
// 投影派生 evidence。该函数不写 SQLite，供最终化事务在升权前预校验使用。
func DeriveShardOrchestrationOverheadEvidence(
	jobID string,
	acceptedGeneration uint64,
	context PlanningContext,
	resourceClassID string,
	resourceCPU, resourceMemoryGiB float64,
	snapshotID string,
	observations []TimingObservation,
	shards []RemoteCIShardRecord,
) (ShardOrchestrationOverheadEvidence, error) {
	if jobID == "" || acceptedGeneration == 0 || snapshotID == "" {
		return ShardOrchestrationOverheadEvidence{}, errors.New("shard overhead evidence run identity is required")
	}
	if context.Calibration || context.Platform == "" {
		return ShardOrchestrationOverheadEvidence{}, errors.New("shard overhead evidence requires normal planning environment")
	}
	if err := validateShardOverheadSourceRunSnapshot(context, snapshotID); err != nil {
		return ShardOrchestrationOverheadEvidence{}, err
	}
	if err := validateShardOverheadResource(resourceClassID, resourceCPU, resourceMemoryGiB, shards); err != nil {
		return ShardOrchestrationOverheadEvidence{}, err
	}
	p95MS, sampleCount, provenanceDigest, samples, err := DeriveShardOrchestrationOverhead(observations)
	if err != nil {
		return ShardOrchestrationOverheadEvidence{}, err
	}
	bindShardOverheadSampleIdentity(samples, acceptedGeneration, provenanceDigest)
	evidence := ShardOrchestrationOverheadEvidence{
		Overhead: newShardOrchestrationOverhead(context, resourceClassID, resourceCPU, resourceMemoryGiB, acceptedGeneration, snapshotID, p95MS, sampleCount, provenanceDigest),
		Samples:  samples,
	}
	if err := evidence.Validate(); err != nil {
		return ShardOrchestrationOverheadEvidence{}, err
	}
	return evidence, nil
}

// AcceptShardOrchestrationOverheadFromRun 从一个完整、权威且已清理的
// calibration-resource PASS run 选取全部分片 timing，派生 nearest-rank P95
// 并以 CAS 写入同一 SQLite authority。它不依赖完整 DurationCalibration。
func (store *DurationLedgerStore) AcceptShardOrchestrationOverheadFromRun(
	expectedGeneration uint64,
	jobID string,
	context PlanningContext,
	resourceClassID string,
	resourceCPU, resourceMemoryGiB float64,
) (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	record, err := store.LoadRemoteCIRun(jobID)
	if err != nil {
		return DurationLedgerSnapshot{}, fmt.Errorf("load authoritative overhead source run: %w", err)
	}
	if err := validateShardOverheadSourceRun(record, context); err != nil {
		return DurationLedgerSnapshot{}, err
	}
	if err := validateShardOverheadResource(resourceClassID, resourceCPU, resourceMemoryGiB, record.Shards); err != nil {
		return DurationLedgerSnapshot{}, err
	}
	evidence, err := DeriveShardOrchestrationOverheadEvidence(
		record.JobID, record.AcceptedGeneration, context, resourceClassID, resourceCPU,
		resourceMemoryGiB, record.ImageCacheSnapshotID, record.TimingObservations, record.Shards,
	)
	if err != nil {
		return DurationLedgerSnapshot{}, fmt.Errorf("derive shard orchestration overhead from run %q: %w", jobID, err)
	}
	return store.CompareAndSwapShardOverhead(expectedGeneration, evidence.Overhead, evidence.Samples)
}

// validateShardOverheadSourceRun 校验权威、成功、已清理 run 及其环境绑定。
func validateShardOverheadSourceRun(record RemoteCIRunRecord, context PlanningContext) error {
	if !record.Authoritative || record.Status != ResultStatusPassed || !record.CleanupComplete {
		return errors.New("shard overhead source run must be authoritative passed and cleanup complete")
	}
	if record.AcceptedGeneration == 0 {
		return errors.New("shard overhead source run accepted generation is required")
	}
	if context.Calibration || context.Platform == "" {
		return errors.New("shard overhead source run requires normal planning environment")
	}
	return validateShardOverheadSourceRunSnapshot(context, record.ImageCacheSnapshotID)
}

func validateShardOverheadSourceRunSnapshot(context PlanningContext, snapshotID string) error {
	if context.AcceptedSnapshotID == "" || snapshotID == "" || context.AcceptedSnapshotID != snapshotID {
		return errors.New("shard overhead source run snapshot does not match planning context")
	}
	return nil
}

func newShardOrchestrationOverhead(context PlanningContext, classID string, cpu, memoryGiB float64, generation uint64, snapshotID string, p95MS int64, sampleCount int, digest string) ShardOrchestrationOverhead {
	return ShardOrchestrationOverhead{
		SchemaVersion: ShardOrchestrationOverheadSchemaVersion, PolicyVersion: ShardOverheadPolicyVersion,
		Platform: context.Platform, Runner: context.Runner, Toolchain: context.Toolchain,
		CalibrationResourceClassID: classID, CalibrationResourceCPU: cpu, CalibrationResourceMemoryGiB: memoryGiB,
		P95MS: p95MS, SampleCount: sampleCount, ProvenanceDigest: digest, AcceptedGeneration: generation, AcceptedSnapshotID: snapshotID,
	}
}

func bindShardOverheadSampleIdentity(samples []ShardOrchestrationOverheadSample, generation uint64, digest string) {
	for index := range samples {
		samples[index].AcceptedGeneration = generation
		samples[index].ProvenanceDigest = digest
	}
}

// validateShardOverheadResource 校验所有分片均使用固定 calibration 4C/8GiB。
func validateShardOverheadResource(classID string, cpu, memoryGiB float64, shards []RemoteCIShardRecord) error {
	if err := cicontract.ValidateCalibrationResources(classID, cpu, memoryGiB); err != nil {
		return fmt.Errorf("shard overhead source resource must be exactly the independent calibration 4 vCPU and 8 GiB class: %w", err)
	}
	if len(shards) == 0 {
		return errors.New("shard overhead source run has no shards")
	}
	for _, shard := range shards {
		if shard.Resources.ClassID != classID || shard.Resources.CPU != cpu || shard.Resources.MemoryGiB != memoryGiB {
			return fmt.Errorf("shard %q resource does not match calibration overhead resource", shard.ShardIdentity)
		}
	}
	return nil
}
