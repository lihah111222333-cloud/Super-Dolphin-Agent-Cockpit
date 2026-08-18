package gate

import (
	"errors"
	"fmt"
	"math"
)

const (
	compileBatchBootstrapCPUMilliCores int64 = 500
	compileBatchBootstrapRSSBytes      int64 = 1 << 30
)

type compileWaveResource struct{ cpu, rss int64 }

// compileGroupResourceEstimate 将 batch 历史按本组 wave 聚合；同 wave 相加，
// 不同 wave 取峰值。任一 selector 无历史时整组明确标记 bootstrap。
func compileGroupResourceEstimate(partition []compilePlanningSelector, batches []CompileGroupBatch, classID string, index BatchResourceIndex) (string, int64, int64, error) {
	cpu, memoryGiB, fullCPU, fullRSS, err := compileGroupResourceCapacity(partition, classID)
	if err != nil {
		return "", 0, 0, err
	}
	if len(batches) == 0 || !parallelResourceBootstrapAllowed(partition[0].input.PackageTarget, partition[0].input.SemanticKey) {
		return "bootstrap", fullCPU, fullRSS, nil
	}
	selectors := make(map[GateID]struct{}, len(partition))
	for _, selector := range partition {
		selectors[GateID(selector.planned.Workload.ID)] = struct{}{}
	}
	waves := make(map[int]compileWaveResource)
	allMeasured := true
	for _, batch := range batches {
		batchCPU, batchRSS, measured, err := compileBatchResourceEstimate(batch, selectors, classID, cpu, memoryGiB, fullCPU, fullRSS, index)
		if err != nil {
			return "", 0, 0, err
		}
		allMeasured = allMeasured && measured
		if err := addCompileWaveResource(waves, batch.Wave, batchCPU, batchRSS); err != nil {
			return "", 0, 0, err
		}
	}
	return finalizeCompileGroupResourceEstimate(waves, allMeasured, fullCPU, fullRSS)
}

// compileGroupResourceCapacity 解析组内统一的 CPU/RSS 分片容量。
func compileGroupResourceCapacity(partition []compilePlanningSelector, classID string) (float64, float64, int64, int64, error) {
	if len(partition) == 0 || classID == "" {
		return 0, 0, 0, 0, errors.New("compile group resource estimate requires a partition and class")
	}
	cpu, memoryGiB := partition[0].planned.ResourceCPU, partition[0].planned.ResourceMemoryGiB
	if cpu <= 0 || memoryGiB <= 0 || memoryGiB > float64(math.MaxInt64)/(1<<30) {
		return 0, 0, 0, 0, errors.New("compile group resource estimate has invalid shard resources")
	}
	return cpu, memoryGiB, int64(math.Ceil(cpu * 1000)), int64(math.Ceil(memoryGiB * float64(1<<30))), nil
}

func compileBatchResourceEstimate(batch CompileGroupBatch, selectors map[GateID]struct{}, classID string, cpu, memoryGiB float64, fullCPU, fullRSS int64, index BatchResourceIndex) (int64, int64, bool, error) {
	batchCPU, batchRSS, allMeasured := int64(0), int64(0), true
	for _, selectorID := range batch.SelectorIDs {
		if _, ok := selectors[selectorID]; !ok {
			return 0, 0, false, fmt.Errorf("compile batch resource selector %q is absent", selectorID)
		}
		observation, measured := index.Estimate(selectorID, classID, cpu, memoryGiB)
		if !measured {
			allMeasured = false
			observation.AverageCPUMilliCores = minInt64(compileBatchBootstrapCPUMilliCores, fullCPU)
			observation.PeakRSSBytes = minInt64(compileBatchBootstrapRSSBytes, fullRSS)
		}
		batchCPU = maxInt64Value(batchCPU, observation.AverageCPUMilliCores)
		batchRSS = maxInt64Value(batchRSS, observation.PeakRSSBytes)
	}
	return batchCPU, batchRSS, allMeasured, nil
}

func addCompileWaveResource(waves map[int]compileWaveResource, waveID int, cpu, rss int64) error {
	wave := waves[waveID]
	if cpu > math.MaxInt64-wave.cpu || rss > math.MaxInt64-wave.rss {
		return errors.New("compile batch resource wave overflows")
	}
	wave.cpu, wave.rss = wave.cpu+cpu, wave.rss+rss
	waves[waveID] = wave
	return nil
}

func finalizeCompileGroupResourceEstimate(waves map[int]compileWaveResource, allMeasured bool, fullCPU, fullRSS int64) (string, int64, int64, error) {
	estimatedCPU, estimatedRSS := int64(0), int64(0)
	for _, wave := range waves {
		estimatedCPU = maxInt64Value(estimatedCPU, wave.cpu)
		estimatedRSS = maxInt64Value(estimatedRSS, wave.rss)
	}
	if estimatedCPU > fullCPU || estimatedRSS > fullRSS {
		return "bootstrap", fullCPU, fullRSS, nil
	}
	if allMeasured {
		return "measured", estimatedCPU, estimatedRSS, nil
	}
	return "bootstrap", estimatedCPU, estimatedRSS, nil
}

func parallelResourceBootstrapAllowed(packageTarget, semanticKey string) bool {
	if semanticKey != CompileGroupSemanticGoTestRace {
		return false
	}
	switch packageTarget {
	case AtomicArchtestPackageTarget, AtomicSuperDolphinGatePackageTarget, AtomicCodexAppPackageTarget,
		AtomicMcpLSPPackageTarget, AtomicAgentTerminalPackageTarget:
		return false
	default:
		return true
	}
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64Value(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
