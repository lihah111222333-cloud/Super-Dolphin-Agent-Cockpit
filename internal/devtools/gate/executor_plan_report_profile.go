package gate

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

// validPlanGateResult 仅接受有界文本证据、单调时钟和有效退出状态。
func validPlanGateResult(result PlanGateExecution, schemaVersion uint32) bool {
	return schemaVersion == ExecutorPlanReportSchemaVersion &&
		validPlanGateTestTimings(result.TestTimings, schemaVersion) &&
		result.ExecutionProfile.Validate() == nil &&
		validPlanGateLog(result) &&
		validPlanGateTimeRange(result) &&
		validPlanGateExit(result.Status, result.ExitCode)
}

// Validate 校验执行画像的缓存计数、代际归属和时长，拒绝无法与报告记录互证的数据。
func (profile ExecutionProfile) Validate() error {
	if err := validateExecutionProfileCache(profile); err != nil {
		return err
	}
	if err := validateExecutionProfileBaselineGenerations(profile); err != nil {
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
	if err := validateExecutionProfileBaselineGenerations(profile); err != nil {
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
		profile.CachePutCount != 0 || len(profile.BaselineHitByGeneration) != 0
}

func validateExecutionProfileBaselineGenerations(profile ExecutionProfile) error {
	var baselineTotal uint64
	for generation, count := range profile.BaselineHitByGeneration {
		if !validExecutorGoBuildCacheGeneration(generation) || count == 0 {
			return errors.New("execution profile baseline generations are invalid")
		}
		if ^uint64(0)-baselineTotal < count {
			return errors.New("execution profile baseline generation counts overflow")
		}
		baselineTotal += count
	}
	if baselineTotal != profile.BaselineHitCount {
		return errors.New("execution profile baseline hit total does not match generations")
	}
	return nil
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

func encodeExecutionProfileRecord(index int, profile ExecutionProfile) (string, error) {
	if err := profile.Validate(); err != nil {
		return "", err
	}
	generations, err := encodeBaselineGenerationMap(profile.BaselineHitByGeneration)
	if err != nil {
		return "", err
	}
	frontend, err := encodeFrontendExecutionProfile(profile.Frontend)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %06d %s %s %s %d %d %d %d %s %d %d %d %d %d %d %s", planReportProfileRecord, index, profile.CacheSource, profile.CacheStatus, profile.CacheMeasurement, profile.PrivateHitCount, profile.BaselineHitCount, profile.CacheMissCount, profile.CachePutCount, generations, profile.MaterializeMS, profile.DownloadMS, profile.VerifyMS, profile.StartupMS, profile.TestBodyMS, profile.TotalMS, frontend), nil
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

// decodeExecutionProfileRecord 严格读取固定字段数和规范空白，防止非规范画像绕过摘要与代际校验。
func decodeExecutionProfileRecord(payload string, expectedIndex int) (ExecutionProfile, error) {
	fields := strings.Fields(payload)
	if len(fields) != 16 || strings.Join(fields, " ") != payload {
		return ExecutionProfile{}, errors.New("plan report execution profile is invalid")
	}
	if err := validatePlanGateRecordIndex(fields[0], expectedIndex); err != nil {
		return ExecutionProfile{}, err
	}
	durations, err := decodeExecutionProfileDurations(fields)
	if err != nil {
		return ExecutionProfile{}, err
	}
	privateHits, baselineHits, misses, puts, err := decodeExecutionProfileCacheCounts(fields)
	if err != nil {
		return ExecutionProfile{}, err
	}
	return buildExecutionProfile(fields, durations, privateHits, baselineHits, misses, puts)
}

func decodeExecutionProfileDurations(fields []string) ([6]int64, error) {
	var durations [6]int64
	for index := range durations {
		value, err := strconv.ParseInt(fields[index+9], 10, 64)
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
	generations, err := decodeBaselineGenerationMap(fields[8])
	if err != nil {
		return ExecutionProfile{}, err
	}
	frontend, err := decodeFrontendExecutionProfile(fields[15])
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile := ExecutionProfile{Frontend: frontend, CacheSource: fields[1], CacheStatus: CacheObservationStatus(fields[2]), CacheMeasurement: fields[3], PrivateHitCount: privateHits, BaselineHitCount: baselineHits, CacheMissCount: misses, CachePutCount: puts, BaselineHitByGeneration: generations, MaterializeMS: durations[0], DownloadMS: durations[1], VerifyMS: durations[2], StartupMS: durations[3], TestBodyMS: durations[4], TotalMS: durations[5]}
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

// encodeBaselineGenerationMap 将代际命中数排序编码，确保摘要输入与传输字段稳定。
func encodeBaselineGenerationMap(generations map[string]uint64) (string, error) {
	if len(generations) == 0 {
		return "-", nil
	}
	keys := make([]string, 0, len(generations))
	for generation, count := range generations {
		if !validExecutorGoBuildCacheGeneration(generation) || count == 0 {
			return "", errors.New("execution profile baseline generation is invalid")
		}
		keys = append(keys, generation)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, generation := range keys {
		values = append(values, generation+":"+strconv.FormatUint(generations[generation], 10))
	}
	return strings.Join(values, ","), nil
}

// decodeBaselineGenerationMap 拒绝重复或非规范字段，确保传入字符串可唯一重编码。
func decodeBaselineGenerationMap(value string) (map[string]uint64, error) {
	if value == "-" {
		return nil, nil
	}
	generations := make(map[string]uint64)
	for pair := range strings.SplitSeq(value, ",") {
		generation, countText, ok := strings.Cut(pair, ":")
		if !ok || !validExecutorGoBuildCacheGeneration(generation) {
			return nil, errors.New("plan report baseline generation is invalid")
		}
		count, err := strconv.ParseUint(countText, 10, 64)
		if err != nil || count == 0 {
			return nil, errors.New("plan report baseline generation count is invalid")
		}
		if _, duplicate := generations[generation]; duplicate {
			return nil, errors.New("plan report baseline generation is duplicated")
		}
		generations[generation] = count
	}
	canonical, err := encodeBaselineGenerationMap(generations)
	if err != nil || canonical != value {
		return nil, errors.New("plan report baseline generations are not canonical")
	}
	return generations, nil
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
