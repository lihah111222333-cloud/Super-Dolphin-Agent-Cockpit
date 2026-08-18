package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// BatchResourceSample 是最近三代权威执行中按 workload 可查询的 batch 资源样本。
type BatchResourceSample struct {
	WorkloadID         GateID
	AcceptedGeneration uint64
	JobID              string
	ResourceClassID    string
	ResourceCPU        float64
	ResourceMemoryGiB  float64
	Observation        BatchResourceObservation
}

// BatchResourceIndex 只包含与本次 platform/runner/toolchain/mode 相同的权威样本。
type BatchResourceIndex struct {
	byWorkload map[GateID][]BatchResourceSample
}

// Estimate 返回 exact workload 与资源规格下最保守的历史 CPU/RSS 画像。
func (index BatchResourceIndex) Estimate(workloadID GateID, classID string, cpu, memoryGiB float64) (BatchResourceObservation, bool) {
	var result BatchResourceObservation
	found := false
	for _, sample := range index.byWorkload[workloadID] {
		if sample.ResourceClassID != classID || sample.ResourceCPU != cpu || sample.ResourceMemoryGiB != memoryGiB {
			continue
		}
		if !found || sample.Observation.AverageCPUMilliCores > result.AverageCPUMilliCores {
			result.AverageCPUMilliCores = sample.Observation.AverageCPUMilliCores
		}
		if !found || sample.Observation.PeakRSSBytes > result.PeakRSSBytes {
			result.PeakRSSBytes = sample.Observation.PeakRSSBytes
		}
		found = true
	}
	return result, found
}

// loadSQLiteBatchResourceIndex 从当前 accepted generation 最近三代加载实测资源画像。
func loadSQLiteBatchResourceIndex(database sqliteRowQueryer, planning PlanningContext, currentGeneration uint64) (BatchResourceIndex, error) {
	index := BatchResourceIndex{byWorkload: make(map[GateID][]BatchResourceSample)}
	if currentGeneration == 0 {
		return index, nil
	}
	retained := cicontract.RetentionGenerationWindow(currentGeneration)
	executionMode := DurationExecutionModeNormal
	if planning.Calibration {
		executionMode = DurationExecutionModeCalibration
	}
	rows, err := database.Query(`
		SELECT runs.accepted_generation, runs.job_id, executions.workload_id,
		       executions.execution_profile_json, shards.resources_json
		FROM ci_runs AS runs
		INNER JOIN ci_remote_run_execution_identities AS identities ON identities.job_id = runs.job_id
		INNER JOIN ci_workload_executions AS executions ON executions.job_id = runs.job_id
		INNER JOIN ci_shards AS shards ON shards.job_id = executions.job_id AND shards.shard_identity = executions.shard_identity
		WHERE runs.accepted_generation >= ? AND runs.accepted_generation <= ?
		  AND runs.status = 'passed' AND runs.authoritative = 1 AND runs.cleanup_complete = 1 AND runs.error_text = ''
		  AND executions.status = 'passed' AND executions.exit_code = 0
		  AND identities.platform = ? AND identities.runner_identity_digest = ?
		  AND identities.toolchain_digest = ? AND identities.execution_mode = ?
		ORDER BY runs.accepted_generation, runs.job_id, executions.workload_id`,
		retained[0], currentGeneration, planning.Platform, planning.Runner, planning.Toolchain, executionMode)
	if err != nil {
		return BatchResourceIndex{}, mapDurationLedgerSQLiteError("query batch resource history", err)
	}
	defer rows.Close()
	if err := appendSQLiteBatchResourceRows(&index, rows, currentGeneration); err != nil {
		return BatchResourceIndex{}, err
	}
	if err := rows.Err(); err != nil {
		return BatchResourceIndex{}, mapDurationLedgerSQLiteError("iterate batch resource history", err)
	}
	return index, nil
}

// appendSQLiteBatchResourceRows 严格解码并拒绝同一共享 batch 的资源证据漂移。
func appendSQLiteBatchResourceRows(index *BatchResourceIndex, rows *sql.Rows, currentGeneration uint64) error {
	seenBatches := make(map[string]BatchResourceObservation)
	for rows.Next() {
		var generation uint64
		var jobID, workloadID, profileJSON, resourcesJSON string
		if err := rows.Scan(&generation, &jobID, &workloadID, &profileJSON, &resourcesJSON); err != nil {
			return mapDurationLedgerSQLiteError("scan batch resource history", err)
		}
		if err := validateSQLiteDurationSampleGeneration(generation, currentGeneration); err != nil {
			return err
		}
		profile, err := decodeStoredRemoteCIExecutionProfile(profileJSON)
		if err != nil {
			return fmt.Errorf("decode batch resource execution profile for %q: %w", workloadID, err)
		}
		if profile.BatchResource == nil {
			continue
		}
		resources, err := decodeBatchResourceShardResources(resourcesJSON)
		if err != nil {
			return fmt.Errorf("decode batch resource shard for %q: %w", workloadID, err)
		}
		batchKey := jobID + "\x00" + profile.BatchResource.BatchID
		if previous, ok := seenBatches[batchKey]; ok && previous != *profile.BatchResource {
			return errors.New("batch resource history contains inconsistent shared batch evidence")
		}
		seenBatches[batchKey] = *profile.BatchResource
		index.byWorkload[GateID(workloadID)] = append(index.byWorkload[GateID(workloadID)], BatchResourceSample{
			WorkloadID: GateID(workloadID), AcceptedGeneration: generation, JobID: jobID,
			ResourceClassID: resources.ClassID, ResourceCPU: resources.CPU, ResourceMemoryGiB: resources.MemoryGiB,
			Observation: *profile.BatchResource,
		})
	}
	return nil
}

func decodeBatchResourceShardResources(encoded string) (RemoteCIShardResources, error) {
	var resources RemoteCIShardResources
	if err := DecodeStrictJSON([]byte(encoded), &resources); err != nil {
		return RemoteCIShardResources{}, err
	}
	if err := resources.Validate(); err != nil {
		return RemoteCIShardResources{}, err
	}
	if math.IsNaN(resources.CPU) || math.IsNaN(resources.MemoryGiB) {
		return RemoteCIShardResources{}, errors.New("batch resource shard resources are not finite")
	}
	return resources, nil
}
