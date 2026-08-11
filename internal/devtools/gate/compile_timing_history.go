package gate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// CompileTimingIdentity 是可复用测试二进制编译时长样本的完整身份。
// 源树、产物和共享输入摘要属于运行审计或编译器事实，不是历史查询键。
type CompileTimingIdentity struct {
	PackageTarget        string  `json:"package_target"`
	SemanticKey          string  `json:"semantic_key"`
	Platform             string  `json:"platform"`
	RunnerIdentityDigest string  `json:"runner_identity_digest"`
	ToolchainDigest      string  `json:"toolchain_digest"`
	ExecutionMode        string  `json:"execution_mode"`
	ResourceClassID      string  `json:"resource_class_id"`
	ResourceCPU          float64 `json:"resource_cpu"`
	ResourceMemoryGiB    float64 `json:"resource_memory_gib"`
}

// Validate 拒绝不完整或有歧义的编译时长查询键。
func (identity CompileTimingIdentity) Validate() error {
	if err := validateCompileTimingIdentitySyntax(identity); err != nil {
		return err
	}
	return validateCompileTimingIdentityResources(identity)
}

// validateCompileTimingIdentitySyntax 校验编译查询键的结构和运行环境身份。
func validateCompileTimingIdentitySyntax(identity CompileTimingIdentity) error {
	if err := validateCompileTimingPackageIdentity(identity); err != nil {
		return err
	}
	if err := validateCompileTimingEnvironmentIdentity(identity); err != nil {
		return err
	}
	return nil
}

func validateCompileTimingPackageIdentity(identity CompileTimingIdentity) error {
	if !isCanonicalCompileGroupPackageTarget(identity.PackageTarget) {
		return errors.New("compile timing package target is invalid")
	}
	if err := ValidateCompileGroupSemanticKey(identity.SemanticKey); err != nil {
		return fmt.Errorf("compile timing semantic key: %w", err)
	}
	return nil
}

// validateCompileTimingEnvironmentIdentity 校验平台、runner 和 toolchain 身份。
func validateCompileTimingEnvironmentIdentity(identity CompileTimingIdentity) error {
	if err := validateDurationEnvironment(identity.Platform, identity.RunnerIdentityDigest, identity.ToolchainDigest); err != nil {
		return fmt.Errorf("compile timing environment: %w", err)
	}
	if identity.ExecutionMode != DurationExecutionModeNormal && identity.ExecutionMode != DurationExecutionModeCalibration {
		return errors.New("compile timing execution mode is invalid")
	}
	if strings.TrimSpace(identity.ResourceClassID) == "" {
		return errors.New("compile timing resource class is required")
	}
	if identity.ResourceCPU <= 0 || identity.ResourceMemoryGiB <= 0 {
		return errors.New("compile timing resource CPU and memory must be positive")
	}
	return nil
}

func validateCompileTimingIdentityResources(identity CompileTimingIdentity) error {
	if identity.ExecutionMode == DurationExecutionModeCalibration {
		if identity.ResourceCPU != cicontract.CalibrationResourceCPU || identity.ResourceMemoryGiB != cicontract.CalibrationResourceMemoryGiB {
			return errors.New("compile timing calibration resources must be 4C/8GiB")
		}
	} else if err := cicontract.ValidateNormalResources(identity.ResourceCPU, identity.ResourceMemoryGiB); err != nil {
		return fmt.Errorf("compile timing normal resources: %w", err)
	}
	return nil
}

// CompileTimingSample 是带 accepted generation 与运行证据的实测编译时长。
// 它只用于读取侧；运行事实来自 ci_runs，而不是观测输入。
type CompileTimingSample struct {
	Identity           CompileTimingIdentity `json:"identity"`
	DurationMS         int64                 `json:"duration_ms"`
	AcceptedGeneration uint64                `json:"accepted_generation"`
	JobID              string                `json:"job_id"`
	StartedAt          time.Time             `json:"started_at"`
	CompletedAt        time.Time             `json:"completed_at"`
}

// Validate 强制样本使用正数时长和真实时间区间。
func (sample CompileTimingSample) Validate() error {
	if err := sample.Identity.Validate(); err != nil {
		return err
	}
	if sample.DurationMS <= 0 {
		return errors.New("compile timing duration_ms must be positive")
	}
	if sample.AcceptedGeneration == 0 {
		return errors.New("compile timing accepted generation is required")
	}
	if strings.TrimSpace(sample.JobID) == "" {
		return errors.New("compile timing job ID is required")
	}
	if sample.StartedAt.IsZero() || !sample.CompletedAt.After(sample.StartedAt) {
		return errors.New("compile timing interval is invalid")
	}
	if err := validateCompileTimingUTCTimingInterval(sample.StartedAt, sample.CompletedAt); err != nil {
		return err
	}
	if sample.CompletedAt.Sub(sample.StartedAt).Milliseconds() != sample.DurationMS {
		return errors.New("compile timing duration_ms must equal its real interval")
	}
	return nil
}

// CompileTimingEstimate 分离稳健规划估值与一条不可变的原始实测证据。
type CompileTimingEstimate struct {
	DurationMS     int64
	Representative CompileTimingSample
}

// Validate 校验估值为正并保持代表样本的真实时间区间不变。
func (estimate CompileTimingEstimate) Validate() error {
	if estimate.DurationMS <= 0 {
		return errors.New("compile timing estimate duration_ms must be positive")
	}
	if err := estimate.Representative.Validate(); err != nil {
		return fmt.Errorf("compile timing representative: %w", err)
	}
	return nil
}

// CompileTimingObservation 是写入侧观测行。
// ci_runs 是 job、generation、状态、authority 和 cleanup 的唯一来源，
// projection writer 从 RemoteCIRunRecord 上下文提供这些运行事实。
type CompileTimingObservation struct {
	Identity    CompileTimingIdentity        `json:"identity"`
	DurationMS  int64                        `json:"duration_ms"`
	StartedAt   time.Time                    `json:"started_at"`
	CompletedAt time.Time                    `json:"completed_at"`
	Measurement cicontract.ObservationState  `json:"measurement"`
	Aggregation cicontract.TimingAggregation `json:"aggregation"`
}

// Validate 校验 measured/raw 观测契约；authority 在 SQLite 投影时通过
// 关联的 ci_runs 校验，不在输入结构中重复保存。
func (observation CompileTimingObservation) Validate() error {
	if err := observation.Identity.Validate(); err != nil {
		return err
	}
	if observation.DurationMS <= 0 {
		return errors.New("compile timing duration_ms must be positive")
	}
	if observation.StartedAt.IsZero() || !observation.CompletedAt.After(observation.StartedAt) {
		return errors.New("compile timing interval is invalid")
	}
	if err := validateCompileTimingUTCTimingInterval(observation.StartedAt, observation.CompletedAt); err != nil {
		return err
	}
	if observation.CompletedAt.Sub(observation.StartedAt).Milliseconds() != observation.DurationMS {
		return errors.New("compile timing duration_ms must equal its real interval")
	}
	if observation.Measurement != cicontract.ObservationMeasured {
		return errors.New("compile timing observation must be measured")
	}
	if observation.Aggregation != cicontract.TimingAggregationRaw {
		return errors.New("compile timing observation must be raw")
	}
	return nil
}

// validateCompileTimingUTCTimingInterval 确保编译时长区间使用 UTC。
func validateCompileTimingUTCTimingInterval(startedAt, completedAt time.Time) error {
	if startedAt.Location() != time.UTC || completedAt.Location() != time.UTC {
		return errors.New("compile timing timestamps must use UTC")
	}
	return nil
}

// CompileTimingIndex 是单次 SQLite 读事务中权威编译时长行的只读聚合。
type CompileTimingIndex struct {
	// Samples 保存三代窗口内每一条 accepted 行，并按 generation、身份和区间排序。
	Samples             []CompileTimingSample
	AcceptedGenerations []uint64
	aggregates          map[CompileTimingIdentity]compileTimingAggregate
}

type compileTimingAggregate struct {
	totalMS int64
	count   int64
	latest  CompileTimingSample
	samples []compileTimingValue
}

type compileTimingValue struct {
	durationMS         int64
	acceptedGeneration uint64
	sample             CompileTimingSample
}

// BuildCompileTimingIndex 校验并聚合样本，不引入第二权威来源。
func BuildCompileTimingIndex(samples []CompileTimingSample) (CompileTimingIndex, error) {
	index := CompileTimingIndex{Samples: make([]CompileTimingSample, 0, len(samples)), aggregates: make(map[CompileTimingIdentity]compileTimingAggregate)}
	for _, sample := range samples {
		if err := sample.Validate(); err != nil {
			return CompileTimingIndex{}, err
		}
		if existing := index.aggregates[sample.Identity]; existing.count != 0 {
			if sample.DurationMS > int64(^uint64(0)>>1)-existing.totalMS {
				return CompileTimingIndex{}, errors.New("compile timing aggregate overflows int64")
			}
			existing.totalMS += sample.DurationMS
			existing.count++
			existing.samples = append(existing.samples, compileTimingValue{durationMS: sample.DurationMS, acceptedGeneration: sample.AcceptedGeneration, sample: sample})
			if newerCompileTimingSample(sample, existing.latest) {
				existing.latest = sample
			}
			index.aggregates[sample.Identity] = existing
		} else {
			index.aggregates[sample.Identity] = compileTimingAggregate{totalMS: sample.DurationMS, count: 1, latest: sample, samples: []compileTimingValue{{durationMS: sample.DurationMS, acceptedGeneration: sample.AcceptedGeneration, sample: sample}}}
		}
		index.Samples = append(index.Samples, sample)
	}
	sort.Slice(index.Samples, func(left, right int) bool {
		return compileTimingSampleLess(index.Samples[left], index.Samples[right])
	})
	if err := index.retainLatestGenerations(); err != nil {
		return CompileTimingIndex{}, err
	}
	return index, nil
}

// retainLatestGenerations 保留最新三代编译时长并重建对应聚合。
func (index *CompileTimingIndex) retainLatestGenerations() error {
	seen := make(map[uint64]struct{})
	for _, sample := range index.Samples {
		seen[sample.AcceptedGeneration] = struct{}{}
	}
	generations := make([]uint64, 0, len(seen))
	for generation := range seen {
		generations = append(generations, generation)
	}
	sort.Slice(generations, func(left, right int) bool { return generations[left] > generations[right] })
	if len(generations) > 3 {
		generations = generations[:3]
	}
	index.AcceptedGenerations = generations
	if len(generations) == 0 {
		return nil
	}
	keep := make(map[uint64]struct{}, len(generations))
	for _, generation := range generations {
		keep[generation] = struct{}{}
	}
	filtered := filterCompileTimingSamples(index.Samples, keep)
	index.Samples = filtered
	return index.rebuildCompileTimingAggregates(filtered)
}

func filterCompileTimingSamples(samples []CompileTimingSample, keep map[uint64]struct{}) []CompileTimingSample {
	filtered := make([]CompileTimingSample, 0, len(samples))
	for _, sample := range samples {
		if _, ok := keep[sample.AcceptedGeneration]; ok {
			filtered = append(filtered, sample)
		}
	}
	return filtered
}

func (index *CompileTimingIndex) rebuildCompileTimingAggregates(samples []CompileTimingSample) error {
	index.aggregates = make(map[CompileTimingIdentity]compileTimingAggregate, len(samples))
	for _, sample := range samples {
		aggregate := index.aggregates[sample.Identity]
		if aggregate.count == 0 {
			aggregate.latest = sample
		}
		if sample.DurationMS > mathMaxInt64-aggregate.totalMS {
			return errors.New("compile timing retained aggregate overflows int64")
		}
		aggregate.totalMS += sample.DurationMS
		aggregate.count++
		aggregate.samples = append(aggregate.samples, compileTimingValue{durationMS: sample.DurationMS, acceptedGeneration: sample.AcceptedGeneration, sample: sample})
		if newerCompileTimingSample(sample, aggregate.latest) {
			aggregate.latest = sample
		}
		index.aggregates[sample.Identity] = aggregate
	}
	return nil
}

func newerCompileTimingSample(left, right CompileTimingSample) bool {
	if left.AcceptedGeneration != right.AcceptedGeneration {
		return left.AcceptedGeneration > right.AcceptedGeneration
	}
	if left.CompletedAt != right.CompletedAt {
		return left.CompletedAt.After(right.CompletedAt)
	}
	return left.JobID > right.JobID
}

// compileTimingSampleLess 按 generation、完整身份和区间给样本稳定排序。
func compileTimingSampleLess(left, right CompileTimingSample) bool {
	if left.AcceptedGeneration != right.AcceptedGeneration {
		return left.AcceptedGeneration > right.AcceptedGeneration
	}
	if order := compareCompileTimingIdentity(left.Identity, right.Identity); order != 0 {
		return order < 0
	}
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	return left.JobID < right.JobID
}

// compareCompileTimingIdentity 返回两个完整身份的确定性字典序关系。
func compareCompileTimingIdentity(left, right CompileTimingIdentity) int {
	for _, pair := range [][2]string{
		{left.PackageTarget, right.PackageTarget},
		{left.SemanticKey, right.SemanticKey},
		{left.Platform, right.Platform},
		{left.RunnerIdentityDigest, right.RunnerIdentityDigest},
		{left.ToolchainDigest, right.ToolchainDigest},
		{left.ExecutionMode, right.ExecutionMode},
		{left.ResourceClassID, right.ResourceClassID},
	} {
		if order := compareCompileTimingStrings(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	if order := compareCompileTimingFloats(left.ResourceCPU, right.ResourceCPU); order != 0 {
		return order
	}
	return compareCompileTimingFloats(left.ResourceMemoryGiB, right.ResourceMemoryGiB)
}

func compareCompileTimingStrings(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareCompileTimingFloats(left, right float64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

// EstimateMS 返回精确身份的三代稳健估值，并携带中位数来源行作为证据。
// 零值索引安全地返回未命中。
func (index CompileTimingIndex) EstimateMS(identity CompileTimingIdentity) (CompileTimingEstimate, bool, error) {
	if err := identity.Validate(); err != nil {
		return CompileTimingEstimate{}, false, err
	}
	aggregate, found := index.aggregates[identity]
	if !found || aggregate.count == 0 {
		return CompileTimingEstimate{}, false, nil
	}
	workloadValues := compileTimingDurationValues(aggregate.samples)
	estimate, representative, _, err := estimateDurationValues(workloadValues, compileParentBootstrapEstimateMS)
	if err != nil {
		return CompileTimingEstimate{}, false, err
	}
	result := CompileTimingEstimate{DurationMS: estimate, Representative: compileTimingRepresentative(aggregate, representative)}
	if err := result.Validate(); err != nil {
		return CompileTimingEstimate{}, false, err
	}
	return result, true, nil
}

func compileTimingDurationValues(samples []compileTimingValue) []durationSampleValue {
	values := make([]durationSampleValue, 0, len(samples))
	for _, value := range samples {
		values = append(values, durationSampleValue{durationMS: value.durationMS, acceptedGeneration: value.acceptedGeneration, tieKey: value.sample.JobID})
	}
	return values
}

func compileTimingRepresentative(aggregate compileTimingAggregate, representative durationSampleValue) CompileTimingSample {
	for _, value := range aggregate.samples {
		if value.acceptedGeneration == representative.acceptedGeneration && value.durationMS == representative.durationMS && value.sample.JobID == representative.tieKey {
			return value.sample
		}
	}
	return aggregate.latest
}

// IsEmpty 报告本次读事务是否没有产生权威行。
func (index CompileTimingIndex) IsEmpty() bool {
	return len(index.Samples) == 0
}
