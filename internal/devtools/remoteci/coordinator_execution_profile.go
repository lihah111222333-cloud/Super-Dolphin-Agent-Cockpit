package remoteci

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// aggregateWorkloadExecutionProfile 从 current child profile 构造父 gate 的可证明区间与缓存汇总。
func aggregateWorkloadExecutionProfile(gateID gate.GateID, workloads []gate.PlanGateExecution) (gate.ExecutionProfile, time.Time, time.Time, error) {
	if len(workloads) == 0 {
		return gate.ExecutionProfile{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q has no workload profiles", gateID)
	}
	startedAt, completedAt, err := validateAggregateWorkloadProfiles(gateID, workloads)
	if err != nil {
		return gate.ExecutionProfile{}, time.Time{}, time.Time{}, err
	}
	startedAt, completedAt, totalMS, err := gate.CanonicalExecutionInterval(startedAt, completedAt)
	if err != nil {
		return gate.ExecutionProfile{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q aggregate interval: %w", gateID, err)
	}
	startup, body, err := shardWorkloadIntervals(workloads)
	if err != nil {
		return gate.ExecutionProfile{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q workload intervals: %w", gateID, err)
	}
	profile := gate.ExecutionProfile{
		CacheSource:      aggregateWorkloadCacheSource(workloads),
		CacheMeasurement: "measured",
		StartupMS:        startup.durationMS,
		TestBodyMS:       body.durationMS,
		TotalMS:          totalMS,
	}
	goFlags, err := gate.WorkloadExecutionGoFlags(string(gateID))
	if err != nil {
		return gate.ExecutionProfile{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q GoFlags: %w", gateID, err)
	}
	profile.GoFlags = goFlags
	for _, execution := range workloads {
		if err := mergeAggregateCacheEvidence(&profile, execution.ExecutionProfile); err != nil {
			return gate.ExecutionProfile{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q workload %q cache evidence: %w", gateID, execution.GateID, err)
		}
	}
	profile.CacheStatus = aggregateExecutionCacheStatus(profile)
	if err := profile.ValidateAggregate(); err != nil {
		return gate.ExecutionProfile{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q aggregate execution profile: %w", gateID, err)
	}
	return profile, startedAt, completedAt, nil
}

// validateAggregateWorkloadProfiles 校验 child profile 与真实 workload 总区间逐项一致。
func validateAggregateWorkloadProfiles(gateID gate.GateID, workloads []gate.PlanGateExecution) (time.Time, time.Time, error) {
	startedAt, completedAt := workloads[0].StartedAt, workloads[0].CompletedAt
	seen := make(map[gate.GateID]struct{}, len(workloads))
	for _, execution := range workloads {
		if execution.GateID == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q has workload without identity", gateID)
		}
		if _, duplicate := seen[execution.GateID]; duplicate {
			return time.Time{}, time.Time{}, fmt.Errorf("remote CI gate %q repeats workload %q", gateID, execution.GateID)
		}
		seen[execution.GateID] = struct{}{}
		if err := validateAggregateWorkloadProfile(execution); err != nil {
			return time.Time{}, time.Time{}, err
		}
		startedAt, completedAt = expandAggregateWorkloadInterval(startedAt, completedAt, execution)
	}
	return startedAt, completedAt, nil
}

// aggregateWorkloadCacheSource 只要任一 child 实测 Go 缓存，就把父 gate 标记为 Go 缓存汇总；其余 child 仍保持 workload 级 none 证据。
func aggregateWorkloadCacheSource(workloads []gate.PlanGateExecution) string {
	for _, execution := range workloads {
		if execution.ExecutionProfile.CacheSource == "go_build_cache" {
			return "go_build_cache"
		}
	}
	return "none"
}

// validateAggregateWorkloadProfile 校验单个 child profile 的测量来源和时间包络。
func validateAggregateWorkloadProfile(execution gate.PlanGateExecution) error {
	if err := execution.ExecutionProfile.Validate(); err != nil {
		return fmt.Errorf("remote CI workload %q execution profile: %w", execution.GateID, err)
	}
	if execution.ExecutionProfile.CacheMeasurement != "measured" {
		return fmt.Errorf("remote CI workload %q cache measurement/source is inconsistent", execution.GateID)
	}
	if err := validateAggregateWorkloadInterval(execution); err != nil {
		return fmt.Errorf("remote CI workload %q %w", execution.GateID, err)
	}
	return nil
}

// validateAggregateWorkloadInterval 拒绝无法由真实边界证明的 child 时间数据。
func validateAggregateWorkloadInterval(execution gate.PlanGateExecution) error {
	if execution.StartedAt.IsZero() {
		return errors.New("total interval is missing or invalid")
	}
	if !execution.CompletedAt.After(execution.StartedAt) {
		return errors.New("total interval is missing or invalid")
	}
	if execution.ExecutionProfile.TotalMS != execution.CompletedAt.Sub(execution.StartedAt).Milliseconds() {
		return errors.New("total profile does not match its interval")
	}
	if execution.ExecutionProfile.MaterializeMS != 0 {
		return errors.New("has phase durations without aggregate interval boundaries")
	}
	if execution.ExecutionProfile.DownloadMS != 0 {
		return errors.New("has phase durations without aggregate interval boundaries")
	}
	if execution.ExecutionProfile.VerifyMS != 0 {
		return errors.New("has phase durations without aggregate interval boundaries")
	}
	return nil
}

// expandAggregateWorkloadInterval 扩展父 gate 的最早开始与最晚完成边界。
func expandAggregateWorkloadInterval(startedAt, completedAt time.Time, execution gate.PlanGateExecution) (time.Time, time.Time) {
	if execution.StartedAt.Before(startedAt) {
		startedAt = execution.StartedAt
	}
	if execution.CompletedAt.After(completedAt) {
		completedAt = execution.CompletedAt
	}
	return startedAt, completedAt
}

// mergeAggregateCacheEvidence 汇总 child 的全部缓存计数并拒绝溢出。
func mergeAggregateCacheEvidence(aggregate *gate.ExecutionProfile, child gate.ExecutionProfile) error {
	for _, item := range []struct {
		name        string
		destination *uint64
		value       uint64
	}{
		{name: "private hits", destination: &aggregate.PrivateHitCount, value: child.PrivateHitCount},
		{name: "baseline hits", destination: &aggregate.BaselineHitCount, value: child.BaselineHitCount},
		{name: "misses", destination: &aggregate.CacheMissCount, value: child.CacheMissCount},
		{name: "puts", destination: &aggregate.CachePutCount, value: child.CachePutCount},
	} {
		if err := addAggregateCacheCount(item.destination, item.value); err != nil {
			return fmt.Errorf("%s: %w", item.name, err)
		}
	}
	return nil
}

// addAggregateCacheCount 在拒绝 uint64 溢出的前提下累加缓存计数。
func addAggregateCacheCount(destination *uint64, value uint64) error {
	if ^uint64(0)-*destination < value {
		return errors.New("cache evidence count overflows uint64")
	}
	*destination += value
	return nil
}

// aggregateExecutionCacheStatus 从完整汇总计数派生展示状态，计数仍是实际证据。
func aggregateExecutionCacheStatus(profile gate.ExecutionProfile) gate.CacheObservationStatus {
	if profile.CacheSource == "none" {
		return gate.CacheObservationNotApplicable
	}
	if profile.PrivateHitCount > 0 || profile.BaselineHitCount > 0 {
		return gate.CacheObservationHit
	}
	if profile.CacheMissCount > 0 {
		return gate.CacheObservationMiss
	}
	if profile.CachePutCount > 0 {
		return gate.CacheObservationPut
	}
	return gate.CacheObservationNotApplicable
}
