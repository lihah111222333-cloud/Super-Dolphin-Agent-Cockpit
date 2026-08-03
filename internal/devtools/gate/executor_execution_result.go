package gate

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// cacheStatusFromMetrics 根据可复查的构建缓存计数归类缓存观测状态。
func cacheStatusFromMetrics(metrics GoBuildCacheProxyMetrics) CacheObservationStatus {
	if metrics.PrivateHitCount > 0 || metrics.BaselineHitCount > 0 {
		return CacheObservationHit
	}
	if metrics.MissCount > 0 {
		return CacheObservationMiss
	}
	if metrics.PutCount > 0 {
		return CacheObservationPut
	}
	return CacheObservationNotApplicable
}

// executionProfileForGate 从 worker 实测区间和测试事件构造单个 gate 的执行画像。
func executionProfileForGate(id GateID, program ExecutorProgram, timings []GoTestTiming, started, completed time.Time, timing *executorExecutionTiming) (ExecutionProfile, error) {
	profile := measuredNonCacheExecutionProfile()
	profile.TotalMS = completed.Sub(started).Milliseconds()
	profile.TotalMS = max(profile.TotalMS, 0)
	if timing == nil || timing.setupMS <= 0 || timing.bodyMS <= 0 || timing.totalMS <= 0 || timing.totalMS > profile.TotalMS {
		return ExecutionProfile{}, errors.New("workload startup and test-body timing is missing or invalid")
	}
	// 执行器在实际 workload 进程两侧记录区间，协调器回执必须保留实测值。
	profile.StartupMS, profile.TestBodyMS = timing.setupMS, timing.bodyMS
	if isGoPackageTestWorkload(id) {
		// Go 测试事件可能并行重叠，不能将其相加推断 workload 的墙钟耗时。
	}
	if isExactGoTestWorkload(id) {
		if err := validateExactGoTestExecutionProfile(id, timings, profile); err != nil {
			return ExecutionProfile{}, err
		}
	}
	if program.NeedsFrontendSeed {
		profile.Frontend = &FrontendExecutionProfile{
			NodeModulesSeedHit: true,
			// lint/test/build 不触发 npm 的包解析路径；已验证 seed 不是 npm 缓存命中证据。
			NPMCacheNotApplicableReason:          "npm_cache_lookup_not_observed",
			PlaywrightBrowserNotApplicableReason: "browser_cache_lookup_not_observed",
			ViteCacheHit:                         timing.viteCacheSeedHit,
		}
		profile.Frontend.SetupMS, profile.Frontend.BodyMS, profile.Frontend.TotalMS = timing.setupMS, timing.bodyMS, timing.totalMS
	}
	return profile, nil
}

// validateExactGoTestExecutionProfile 校验精确 Go 测试事件与 worker 实测区间的一致性。
func validateExactGoTestExecutionProfile(id GateID, timings []GoTestTiming, profile ExecutionProfile) error {
	_, _, target, _, parseErr := parseTargetWorkloadID(string(id))
	if parseErr != nil {
		return parseErr
	}
	testTarget, parseErr := ParseGoTestTarget(target)
	if parseErr != nil {
		return parseErr
	}
	matched := exactGoTestTimings(timings, testTarget.Name)
	if len(matched) != 1 || matched[0].DurationMS < 0 || matched[0].DurationMS > profile.TotalMS {
		return errors.New("exact Go test execution profile timing is missing or invalid")
	}
	if matched[0].DurationMS > profile.TestBodyMS {
		return errors.New("exact Go test event exceeds measured body interval")
	}
	return nil
}

// exactGoTestTimings 返回与精确 Go 测试名匹配的全部事件，供唯一性校验使用。
func exactGoTestTimings(timings []GoTestTiming, name string) []GoTestTiming {
	matched := make([]GoTestTiming, 0, 1)
	for _, timing := range timings {
		if timing.Name == name {
			matched = append(matched, timing)
		}
	}
	return matched
}

// measuredNonCacheExecutionProfile 构造无需缓存查询但仍具备实测时长的执行画像基线。
func measuredNonCacheExecutionProfile() ExecutionProfile {
	return ExecutionProfile{CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured"}
}

// isGoPackageTestWorkload 判断 workload 是否执行 Go 包或精确 Go 测试。
func isGoPackageTestWorkload(id GateID) bool {
	_, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && (kind == workloadTargetGoPackage || kind == workloadTargetGoTest)
}

// isExactGoTestWorkload 判断 workload 是否要求单个 Go 测试事件与实测区间一致。
func isExactGoTestWorkload(id GateID) bool {
	_, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && kind == workloadTargetGoTest
}

// planGateFailureSummary 只记录稳定分类与退出码，不回显可能含秘密或宿主路径的原始错误。
func planGateFailureSummary(gateErr error, contextErr error, status ResultStatus, exitCode int) []byte {
	if gateErr == nil {
		return nil
	}
	reason := "execution-error"
	if errors.Is(contextErr, context.DeadlineExceeded) {
		reason = "deadline"
	} else if errors.Is(contextErr, context.Canceled) {
		reason = "peer-cancellation"
	}
	return fmt.Appendf(nil, "[gate-executor] outcome status=%s exit_code=%d reason=%s\n", status, exitCode, reason)
}

// classifyPlanGateOutcome 只用 gate error 与父 context authority 生成 canonical status/exit。
func classifyPlanGateOutcome(gateErr error, contextErr error) (ResultStatus, int) {
	if gateErr == nil {
		return ResultStatusPassed, 0
	}
	if errors.Is(contextErr, context.DeadlineExceeded) {
		return ResultStatusTimeout, -1
	}
	if errors.Is(contextErr, context.Canceled) {
		return ResultStatusCancelled, -1
	}
	return ResultStatusFailed, ExecutorExitCode(gateErr)
}
