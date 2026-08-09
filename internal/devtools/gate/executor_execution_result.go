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
	goFlags, err := executionGoFlagsForProfile(id, program)
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile.GoFlags = goFlags
	profile.TotalMS = measuredExecutorPhaseMilliseconds(started, completed)
	if err := validateExecutorExecutionTiming(timing); err != nil {
		return ExecutionProfile{}, err
	}
	profile.TotalMS = max(profile.TotalMS, timing.totalMS)
	// 执行器在实际 workload 进程两侧记录区间，协调器回执必须保留实测值。
	profile.StartupMS, profile.TestBodyMS = timing.setupMS, timing.bodyMS
	if err := validateExactExecutionProfileIfNeeded(id, timings, profile); err != nil {
		return profile, err
	}
	attachFrontendExecutionProfile(&profile, program, timing)
	return profile, nil
}

func executionGoFlagsForProfile(id GateID, program ExecutorProgram) (string, error) {
	goFlags, err := ExecutorProgramGoFlags(program)
	if err != nil {
		return "", fmt.Errorf("derive workload GoFlags: %w", err)
	}
	// Tests and report reconstruction may pass an empty program; when the workload
	// itself is canonical, project the same immutable executor mapping rather than
	// inventing a second profile source.
	if goFlags != "" {
		return goFlags, nil
	}
	_, canonical, lookupErr := executorProgramForWorkload(id)
	if lookupErr != nil {
		return goFlags, nil
	}
	goFlags, err = ExecutorProgramGoFlags(canonical)
	if err != nil {
		return "", fmt.Errorf("derive canonical workload GoFlags: %w", err)
	}
	return goFlags, nil
}

func validateExecutorExecutionTiming(timing *executorExecutionTiming) error {
	if timing == nil || timing.setupMS <= 0 || timing.bodyMS <= 0 || timing.totalMS != timing.setupMS+timing.bodyMS {
		return errors.New("workload startup and test-body timing is missing or invalid")
	}
	return nil
}

func validateExactExecutionProfileIfNeeded(id GateID, timings []GoTestTiming, profile ExecutionProfile) error {
	if !isExactGoTestWorkload(id) {
		return nil
	}
	return validateExactGoTestExecutionProfile(id, timings, profile)
}

func attachFrontendExecutionProfile(profile *ExecutionProfile, program ExecutorProgram, timing *executorExecutionTiming) {
	if !program.NeedsFrontendSeed {
		return
	}
	profile.Frontend = &FrontendExecutionProfile{
		NodeModulesSeedHit: true,
		// lint/test/build 不触发 npm 的包解析路径；已验证 seed 不是 npm 缓存命中证据。
		NPMCacheNotApplicableReason:          "npm_cache_lookup_not_observed",
		PlaywrightBrowserNotApplicableReason: "browser_cache_lookup_not_observed",
		ViteCacheHit:                         timing.viteCacheSeedHit,
	}
	profile.Frontend.SetupMS, profile.Frontend.BodyMS, profile.Frontend.TotalMS = timing.setupMS, timing.bodyMS, timing.totalMS
}

// executionProfileOrFailedStartup 在正文未开始时保留启动失败区间，并传递后续精确计时错误。
func executionProfileOrFailedStartup(id GateID, program ExecutorProgram, timings []GoTestTiming, started, completed time.Time, timing *executorExecutionTiming) (ExecutionProfile, error) {
	profile, err := executionProfileForGate(id, program, timings, started, completed, timing)
	if err != nil && (timing == nil || timing.setupMS <= 0 || timing.bodyMS <= 0 || timing.totalMS != timing.setupMS+timing.bodyMS) {
		return measuredFailedStartupExecutionProfile(started, completed), err
	}
	return profile, err
}

// canonicalExactGoTestProcessCompletion 用唯一的顶层 test2json 终态截断进程尾部，
// 避免测试已结束后等待子进程/管道清理的时间被记入 TestBodyMS。
func canonicalExactGoTestProcessCompletion(id GateID, timings []GoTestTiming, started time.Time, timing *executorExecutionTiming) time.Time {
	if timing == nil || timing.setupMS <= 0 || started.IsZero() {
		return time.Time{}
	}
	bodyMS, ok := exactGoTestBodyDuration(id, timings)
	if !ok {
		return time.Time{}
	}
	totalMS, ok := sumExecutionTimingMS(timing.setupMS, bodyMS)
	if !ok {
		return time.Time{}
	}
	timing.bodyMS = bodyMS
	timing.totalMS = totalMS
	bodyStarted := started.Add(time.Duration(timing.setupMS) * time.Millisecond)
	return bodyStarted.Add(time.Duration(timing.bodyMS) * time.Millisecond)
}

func exactGoTestBodyDuration(id GateID, timings []GoTestTiming) (int64, bool) {
	if !isExactGoTestWorkload(id) {
		return 0, false
	}
	_, _, target, targeted, parseErr := parseTargetWorkloadID(string(id))
	if parseErr != nil || !targeted {
		return 0, false
	}
	testTarget, parseErr := ParseGoTestTarget(target)
	if parseErr != nil {
		return 0, false
	}
	matched := exactGoTestTimings(timings, testTarget.Name)
	if len(matched) != 1 || matched[0].DurationMS <= 0 {
		return 0, false
	}
	return matched[0].DurationMS, true
}

func sumExecutionTimingMS(setupMS, bodyMS int64) (int64, bool) {
	if bodyMS <= 0 || setupMS > int64(^uint64(0)>>1)-bodyMS {
		return 0, false
	}
	return setupMS + bodyMS, true
}

func canonicalExactGoTestProcessCompletionOr(current time.Time, id GateID, timings []GoTestTiming, started time.Time, timing *executorExecutionTiming) time.Time {
	if canonical := canonicalExactGoTestProcessCompletion(id, timings, started, timing); !canonical.IsZero() {
		return canonical
	}
	return current
}

// normalizedExecutionCompletedAt 让 workload 总区间覆盖按账本分辨率量化后的串行阶段。
func normalizedExecutionCompletedAt(started, completed time.Time, profile ExecutionProfile) time.Time {
	minimumCompletedAt := started.Add(time.Duration(profile.TotalMS) * time.Millisecond)
	if completed.Before(minimumCompletedAt) {
		return minimumCompletedAt
	}
	return completed
}

// CanonicalExecutionInterval 将执行区间规范化到 SQLite 账本的 UTC 毫秒分辨率。
func CanonicalExecutionInterval(started, completed time.Time) (time.Time, time.Time, int64, error) {
	if started.IsZero() || completed.IsZero() {
		return time.Time{}, time.Time{}, 0, errors.New("execution interval timestamps are required")
	}
	started = started.UTC().Truncate(time.Millisecond)
	completed = completed.UTC().Truncate(time.Millisecond)
	if completed.Before(started) {
		return time.Time{}, time.Time{}, 0, errors.New("execution interval completed time is before started time")
	}
	return started, completed, completed.Sub(started).Milliseconds(), nil
}

// CanonicalizePlanGateExecutionTiming 让 workload 的起止时间与执行画像总时长共享唯一的毫秒事实。
func CanonicalizePlanGateExecutionTiming(execution PlanGateExecution) (PlanGateExecution, error) {
	started, completed, totalMS, err := CanonicalExecutionInterval(execution.StartedAt, execution.CompletedAt)
	if err != nil {
		return execution, err
	}
	execution.StartedAt, execution.CompletedAt = started, completed
	execution.ExecutionProfile.TotalMS = totalMS
	if err := execution.ExecutionProfile.Validate(); err != nil {
		return execution, fmt.Errorf("canonical execution profile: %w", err)
	}
	return execution, nil
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
	if len(matched) != 1 || matched[0].DurationMS < 0 {
		return errors.New("exact Go test execution profile timing is missing or invalid")
	}
	if matched[0].DurationMS > profile.TotalMS {
		return errors.New("exact Go test event exceeds measured total interval")
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

// measuredExecutionProfileForWorkload binds compile-group evidence to the
// canonical workload execution profile; compile selectors must never emit an
// unbound empty GoFlags value.
func measuredExecutionProfileForWorkload(id GateID) (ExecutionProfile, error) {
	profile, err := measuredExecutionProfileForGate(id)
	if err != nil {
		return ExecutionProfile{}, err
	}
	if profile.GoFlags == "" {
		return ExecutionProfile{}, fmt.Errorf("workload %q has no canonical GoFlags", id)
	}
	return profile, nil
}

// measuredExecutionProfileForGate binds every pending result to the same
// canonical workload profile producer; non-Go gates intentionally retain empty
// GoFlags while Go workloads fail fast if their mapping is unavailable.
func measuredExecutionProfileForGate(id GateID) (ExecutionProfile, error) {
	goFlags, err := WorkloadExecutionGoFlags(string(id))
	if err != nil {
		return ExecutionProfile{}, fmt.Errorf("derive workload GoFlags: %w", err)
	}
	profile := measuredNonCacheExecutionProfile()
	profile.GoFlags = goFlags
	return profile, nil
}

// measuredFailedStartupExecutionProfile 保留 workload 正文尚未开始时的实测启动区间，不把失败伪装成零耗时正文。
func measuredFailedStartupExecutionProfile(started, completed time.Time) ExecutionProfile {
	profile := measuredNonCacheExecutionProfile()
	profile.StartupMS = max(measuredExecutorPhaseMilliseconds(started, completed), 1)
	profile.TotalMS = profile.StartupMS
	return profile
}

// isGoPackageTestWorkload 判断 workload 是否执行 Go 包或精确 Go 测试。
func isGoPackageTestWorkload(id GateID) bool {
	_, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && (kind == workloadTargetGoPackage || kind == workloadTargetGoTest)
}

// needsGoCacheObservation 根据执行程序的 Go seed 契约识别所有可能访问 GOCACHEPROG 的 workload。
// 内建 code-size worker 的 go/packages 子进程也通过该契约访问 Go cache，不能按 Go 测试事件类型漏记。
func needsGoCacheObservation(program ExecutorProgram) bool {
	return program.NeedsGoSeed
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
