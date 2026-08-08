package gate

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

// validPlanGateResult 仅接受有界文本证据、单调时钟和有效退出状态。
func validPlanGateResult(result PlanGateExecution, schemaVersion uint32) bool {
	return schemaVersion == ExecutorPlanReportSchemaVersion &&
		validPlanGateTestTimings(result.TestTimings, schemaVersion) &&
		ValidatePlanGateTimingEvidence(result) == nil &&
		result.ExecutionProfile.Validate() == nil &&
		validPlanGateLog(result) &&
		validPlanGateTimeRange(result) &&
		validPlanGateExit(result.Status, result.ExitCode)
}

// ValidatePlanGateTimingEvidence 校验 exact Go selector 的 test2json 时长不超过同一 execution 的实测墙钟与正文区间。
// 无终态 timing 的 cancelled/未启动结果保留为可诊断观察；一旦存在 timing，必须与同一 execution profile 一致。
func ValidatePlanGateTimingEvidence(result PlanGateExecution) error {
	if !isExactGoTestWorkload(result.GateID) || len(result.TestTimings) == 0 {
		return nil
	}
	if err := validateExactGoTestExecutionProfile(result.GateID, result.TestTimings, result.ExecutionProfile); err != nil {
		return fmt.Errorf("exact Go test timing evidence: %w", err)
	}
	return nil
}

// Validate 校验执行画像的缓存计数和时长，拒绝无法与报告记录互证的数据。
func (profile ExecutionProfile) Validate() error {
	if err := validateExecutionProfileCache(profile); err != nil {
		return err
	}
	if !validExecutionProfileTiming(profile) {
		return errors.New("execution profile timing is invalid")
	}
	if profile.Frontend != nil {
		if err := profile.Frontend.Validate(); err != nil {
			return fmt.Errorf("frontend execution profile: %w", err)
		}
	}
	return nil
}

// ValidateAggregate 校验 coordinator 从多个 workload 区间聚合出的父 gate 执行画像。
func (profile ExecutionProfile) ValidateAggregate() error {
	if err := validateExecutionProfileCache(profile); err != nil {
		return err
	}
	if !validAggregateExecutionProfileTiming(profile) {
		return errors.New("aggregate execution profile timing is invalid")
	}
	if profile.Frontend != nil {
		return errors.New("aggregate execution profile must keep frontend evidence workload-scoped")
	}
	return nil
}

// validateExecutionProfileCache 校验缓存枚举及“不适用”状态与观测计数的互斥关系。
func validateExecutionProfileCache(profile ExecutionProfile) error {
	if !validExecutionProfileCacheSource(profile.CacheSource) {
		return errors.New("execution profile cache source is invalid")
	}
	if !validExecutionProfileCacheStatus(profile.CacheStatus) {
		return errors.New("execution profile cache status is invalid")
	}
	if !validExecutionProfileCacheMeasurement(profile.CacheMeasurement) {
		return errors.New("execution profile cache measurement is invalid")
	}
	if profile.CacheSource == "none" && profile.CacheStatus != CacheObservationNotApplicable {
		return errors.New("execution profile absent cache is not applicable")
	}
	if profile.CacheStatus == CacheObservationNotApplicable && hasExecutionProfileCacheObservations(profile) {
		return errors.New("execution profile zero-lookup cache status has observations")
	}
	return nil
}

func validExecutionProfileCacheSource(source string) bool {
	return source == "none" || source == "go_build_cache"
}

func validExecutionProfileCacheStatus(status CacheObservationStatus) bool {
	return status == CacheObservationNotApplicable || status == CacheObservationHit || status == CacheObservationMiss || status == CacheObservationPut
}

func validExecutionProfileCacheMeasurement(measurement string) bool {
	return measurement == "measured"
}

func hasExecutionProfileCacheObservations(profile ExecutionProfile) bool {
	return profile.PrivateHitCount != 0 || profile.BaselineHitCount != 0 || profile.CacheMissCount != 0 ||
		profile.CachePutCount != 0
}

// validExecutionProfileTiming 要求各阶段非负且总时长覆盖所有串行阶段。
func validExecutionProfileTiming(profile ExecutionProfile) bool {
	return profile.MaterializeMS >= 0 && profile.DownloadMS >= 0 && profile.VerifyMS >= 0 &&
		profile.StartupMS >= 0 && profile.TestBodyMS >= 0 && profile.TotalMS >= 0 &&
		profile.TotalMS >= profile.MaterializeMS+profile.DownloadMS+profile.VerifyMS+profile.StartupMS+profile.TestBodyMS
}

// validAggregateExecutionProfileTiming 允许 startup/test-body 各自按区间并集聚合，同时要求二者均受 total 关键路径约束。
func validAggregateExecutionProfileTiming(profile ExecutionProfile) bool {
	return profile.MaterializeMS == 0 && profile.DownloadMS == 0 && profile.VerifyMS == 0 &&
		profile.StartupMS > 0 && profile.TestBodyMS > 0 && profile.TotalMS > 0 &&
		profile.StartupMS <= profile.TotalMS && profile.TestBodyMS <= profile.TotalMS
}

// Validate 要求每种前端缓存明确命中或给出不适用原因，避免把缺失证据推断为命中。
func (profile FrontendExecutionProfile) Validate() error {
	for _, evidence := range []struct {
		hit    bool
		reason string
	}{
		{profile.NodeModulesSeedHit, profile.NodeModulesSeedNotApplicableReason},
		{profile.NPMCacheHit, profile.NPMCacheNotApplicableReason},
		{profile.PlaywrightBrowserHit, profile.PlaywrightBrowserNotApplicableReason},
		{profile.ViteCacheHit, profile.ViteCacheNotApplicableReason},
	} {
		if evidence.hit == (evidence.reason != "") {
			return errors.New("frontend cache evidence must be a hit or have a not applicable reason")
		}
	}
	if profile.SetupMS < 0 || profile.BodyMS < 0 || profile.TotalMS < 0 || profile.TotalMS != profile.SetupMS+profile.BodyMS {
		return errors.New("frontend execution profile timing is invalid")
	}
	return nil
}

func encodeFrontendExecutionProfile(profile *FrontendExecutionProfile) (string, error) {
	if profile == nil {
		return "-", nil
	}
	if err := profile.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func decodeExecutionProfileDurations(fields []string) ([6]int64, error) {
	var durations [6]int64
	for index := range durations {
		value, err := strconv.ParseInt(fields[index+8], 10, 64)
		if err != nil {
			return [6]int64{}, errors.New("plan report execution profile duration is invalid")
		}
		durations[index] = value
	}
	return durations, nil
}

func decodeExecutionProfileCacheCounts(fields []string) (uint64, uint64, uint64, uint64, error) {
	privateHits, privateErr := strconv.ParseUint(fields[4], 10, 64)
	baselineHits, baselineErr := strconv.ParseUint(fields[5], 10, 64)
	misses, missErr := strconv.ParseUint(fields[6], 10, 64)
	puts, putErr := strconv.ParseUint(fields[7], 10, 64)
	if privateErr != nil || baselineErr != nil || missErr != nil || putErr != nil {
		return 0, 0, 0, 0, errors.New("plan report execution profile cache count is invalid")
	}
	return privateHits, baselineHits, misses, puts, nil
}

func buildExecutionProfile(fields []string, durations [6]int64, privateHits uint64, baselineHits uint64, misses uint64, puts uint64) (ExecutionProfile, error) {
	frontend, err := decodeFrontendExecutionProfile(fields[14])
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile := ExecutionProfile{Frontend: frontend, CacheSource: fields[1], CacheStatus: CacheObservationStatus(fields[2]), CacheMeasurement: fields[3], PrivateHitCount: privateHits, BaselineHitCount: baselineHits, CacheMissCount: misses, CachePutCount: puts, MaterializeMS: durations[0], DownloadMS: durations[1], VerifyMS: durations[2], StartupMS: durations[3], TestBodyMS: durations[4], TotalMS: durations[5]}
	if err := profile.Validate(); err != nil {
		return ExecutionProfile{}, err
	}
	return profile, nil
}

func decodeFrontendExecutionProfile(value string) (*FrontendExecutionProfile, error) {
	if value == "-" {
		return nil, nil
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return nil, errors.New("plan report frontend execution profile is invalid")
	}
	var profile FrontendExecutionProfile
	if json.Unmarshal(data, &profile) != nil || profile.Validate() != nil {
		return nil, errors.New("plan report frontend execution profile is invalid")
	}
	return &profile, nil
}

// validPlanGateLog 校验日志边界、文本编码和内容摘要。
func validPlanGateLog(result PlanGateExecution) bool {
	return len(result.Log) <= executorPlanMaxLogBytes &&
		utf8.Valid(result.Log) &&
		bytes.IndexByte(result.Log, 0) < 0 &&
		result.LogDigest == digestPlanLog(result.Log) &&
		(result.ArgvDigest == "" || digestPattern.MatchString(result.ArgvDigest))
}

func validPlanGateTimeRange(result PlanGateExecution) bool {
	return !result.StartedAt.IsZero() && !result.CompletedAt.IsZero() && !result.CompletedAt.Before(result.StartedAt)
}

func validPlanGateTestTimings(timings []GoTestTiming, schemaVersion uint32) bool {
	return schemaVersion == ExecutorPlanReportSchemaVersion &&
		testtiming.ValidateList(timings, executorPlanMaxTimingRecords) == nil
}

// validPlanGateExit 校验状态与执行器退出码的稳定组合。
func validPlanGateExit(status ResultStatus, exitCode int) bool {
	switch status {
	case ResultStatusPassed:
		return exitCode == 0
	case ResultStatusFailed:
		return exitCode > 0
	case ResultStatusCancelled, ResultStatusTimeout:
		return exitCode == -1
	default:
		return false
	}
}
