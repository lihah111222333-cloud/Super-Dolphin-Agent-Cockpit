package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type shardOverheadObservationKey struct {
	jobID string
	shard string
}

// DeriveShardOrchestrationOverhead 从权威 timing observation 派生每分片
// total interval 减 accounted interval union，并按 nearest-rank P95 聚合。
// accounted union 只接受 workload total、shard eci_wait/source/candidate_compile
// 以及 compile-group test_binary_compile 的 measured raw interval；重叠只计一次，
// 间隙保留为 orchestration overhead。
func DeriveShardOrchestrationOverhead(observations []TimingObservation) (int64, int, string, []ShardOrchestrationOverheadSample, error) {
	groups, err := collectShardOverheadObservationGroups(observations)
	if err != nil {
		return 0, 0, "", nil, err
	}
	samples, err := deriveShardOverheadSamples(groups)
	if err != nil {
		return 0, 0, "", nil, err
	}
	digest, err := shardOverheadSamplesDigest(samples)
	if err != nil {
		return 0, 0, "", nil, err
	}
	bindShardOverheadSampleDigests(samples, digest)
	return shardOverheadP95(samples), len(samples), digest, samples, nil
}

// deriveShardOverheadSamples 校验每个分片具备 total、workload 和三段 shard
// accounted phase，并生成可审计样本。
func deriveShardOverheadSamples(groups map[shardOverheadObservationKey]*shardOverheadObservationGroup) ([]ShardOrchestrationOverheadSample, error) {
	if len(groups) == 0 {
		return nil, errors.New("no measured shard/workload total timing samples")
	}
	samples := make([]ShardOrchestrationOverheadSample, 0, len(groups))
	for key, group := range groups {
		if err := validateShardOverheadGroup(key, group); err != nil {
			return nil, err
		}
		sample, err := shardOverheadSampleFromTiming(key, *group.total, group.workloads, group.accounted)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(left, right int) bool {
		if samples[left].JobID != samples[right].JobID {
			return samples[left].JobID < samples[right].JobID
		}
		return samples[left].ShardIdentity < samples[right].ShardIdentity
	})
	return samples, nil
}

// validateShardOverheadGroup 拒绝缺少 workload total 或 shard 级三段 accounted
// phase 的样本组，避免以不完整账本计算 overhead。
func validateShardOverheadGroup(key shardOverheadObservationKey, group *shardOverheadObservationGroup) error {
	if group.total == nil || len(group.workloads) == 0 {
		return fmt.Errorf("incomplete shard overhead timing coverage for %s/%s", key.jobID, key.shard)
	}
	for _, phase := range []cicontract.TimingPhase{cicontract.TimingECIWait, cicontract.TimingSourceMaterialize, cicontract.TimingCandidateCompile} {
		if _, exists := group.shardPhases[phase]; !exists {
			return fmt.Errorf("incomplete shard accounted timing coverage for %s/%s phase=%s", key.jobID, key.shard, phase)
		}
	}
	return nil
}

// bindShardOverheadSampleDigests 将同一批样本绑定同一 provenance digest。
func bindShardOverheadSampleDigests(samples []ShardOrchestrationOverheadSample, digest string) {
	for index := range samples {
		samples[index].ProvenanceDigest = digest
	}
}

// shardOverheadP95 从已排序样本提取 overhead 值并计算 nearest-rank P95。
func shardOverheadP95(samples []ShardOrchestrationOverheadSample) int64 {
	durations := make([]int64, len(samples))
	for index, sample := range samples {
		durations[index] = sample.OverheadMS
	}
	return nearestRankP95(durations)
}

// collectShardOverheadObservationGroups 按 job/shard 聚合权威 measured interval。
func collectShardOverheadObservationGroups(observations []TimingObservation) (map[shardOverheadObservationKey]*shardOverheadObservationGroup, error) {
	groups := make(map[shardOverheadObservationKey]*shardOverheadObservationGroup)
	for _, observation := range observations {
		if err := observation.Validate(); err != nil {
			return nil, fmt.Errorf("validate shard overhead timing observation: %w", err)
		}
		if observation.Measurement != cicontract.ObservationMeasured || !isShardOverheadAccountedObservation(observation) {
			continue
		}
		key := shardOverheadObservationKey{jobID: observation.JobID, shard: observation.ShardIdentity}
		group := groups[key]
		if group == nil {
			group = &shardOverheadObservationGroup{
				shardPhases:   make(map[cicontract.TimingPhase]struct{}),
				workloadIDs:   make(map[GateID]struct{}),
				compileGroups: make(map[shardOverheadCompileGroupKey]struct{}),
			}
			groups[key] = group
		}
		if err := appendShardOverheadObservation(group, observation); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

// appendShardOverheadObservation 将一个 measured raw observation 放入对应
// shard 账本类别，并保持每个可计入身份唯一。
func appendShardOverheadObservation(group *shardOverheadObservationGroup, observation TimingObservation) error {
	switch observation.Scope {
	case cicontract.TimingScopeShard:
		return appendShardScopedOverheadObservation(group, observation)
	case cicontract.TimingScopeWorkload:
		return appendWorkloadOverheadObservation(group, observation)
	case cicontract.TimingScopeCompileGroup:
		return appendCompileGroupOverheadObservation(group, observation)
	}
	return nil
}

// appendShardScopedOverheadObservation 保存 shard total 或三段 raw accounted phase。
func appendShardScopedOverheadObservation(group *shardOverheadObservationGroup, observation TimingObservation) error {
	if observation.Phase == cicontract.TimingTotal {
		if observation.Aggregation != cicontract.TimingAggregationCriticalPath || group.total != nil {
			return fmt.Errorf("duplicate or invalid shard total timing for %s/%s", observation.JobID, observation.ShardIdentity)
		}
		copy := observation
		group.total = &copy
		return nil
	}
	if observation.Aggregation != cicontract.TimingAggregationRaw {
		return fmt.Errorf("invalid shard accounted timing aggregation for %s/%s phase=%s", observation.JobID, observation.ShardIdentity, observation.Phase)
	}
	if _, duplicate := group.shardPhases[observation.Phase]; duplicate {
		return fmt.Errorf("duplicate shard accounted timing for %s/%s phase=%s", observation.JobID, observation.ShardIdentity, observation.Phase)
	}
	group.shardPhases[observation.Phase] = struct{}{}
	group.accounted = append(group.accounted, observation)
	return nil
}

// appendWorkloadOverheadObservation 保存唯一 workload total raw interval。
func appendWorkloadOverheadObservation(group *shardOverheadObservationGroup, observation TimingObservation) error {
	if observation.Phase != cicontract.TimingTotal || observation.Aggregation != cicontract.TimingAggregationRaw || observation.WorkloadID == "" {
		return fmt.Errorf("invalid workload total timing for %s/%s", observation.JobID, observation.ShardIdentity)
	}
	if _, duplicate := group.workloadIDs[observation.WorkloadID]; duplicate {
		return fmt.Errorf("duplicate workload total timing for %s/%s workload=%s", observation.JobID, observation.ShardIdentity, observation.WorkloadID)
	}
	group.workloadIDs[observation.WorkloadID] = struct{}{}
	group.workloads = append(group.workloads, observation)
	group.accounted = append(group.accounted, observation)
	return nil
}

// appendCompileGroupOverheadObservation 保存唯一 compile-group test binary raw interval。
func appendCompileGroupOverheadObservation(group *shardOverheadObservationGroup, observation TimingObservation) error {
	if observation.Phase != cicontract.TimingTestBinaryCompile || observation.Aggregation != cicontract.TimingAggregationRaw {
		return fmt.Errorf("invalid compile-group accounted timing for %s/%s", observation.JobID, observation.ShardIdentity)
	}
	compileKey := shardOverheadCompileGroupKey{group: observation.CompileGroupID, artifact: observation.CompileArtifactKey}
	if _, duplicate := group.compileGroups[compileKey]; duplicate {
		return fmt.Errorf("duplicate compile-group accounted timing for %s/%s group=%s artifact=%s", observation.JobID, observation.ShardIdentity, observation.CompileGroupID, observation.CompileArtifactKey)
	}
	group.compileGroups[compileKey] = struct{}{}
	group.accounted = append(group.accounted, observation)
	return nil
}

type shardOverheadObservationGroup struct {
	total         *TimingObservation
	workloads     []TimingObservation
	accounted     []TimingObservation
	shardPhases   map[cicontract.TimingPhase]struct{}
	workloadIDs   map[GateID]struct{}
	compileGroups map[shardOverheadCompileGroupKey]struct{}
}

type shardOverheadCompileGroupKey struct {
	group    string
	artifact string
}

// isShardOverheadAccountedObservation 判断 observation 是否属于 v2 accounted union。
func isShardOverheadAccountedObservation(observation TimingObservation) bool {
	switch observation.Scope {
	case cicontract.TimingScopeShard:
		return observation.Phase == cicontract.TimingTotal ||
			observation.Phase == cicontract.TimingECIWait ||
			observation.Phase == cicontract.TimingSourceMaterialize ||
			observation.Phase == cicontract.TimingCandidateCompile
	case cicontract.TimingScopeWorkload:
		return observation.Phase == cicontract.TimingTotal
	case cicontract.TimingScopeCompileGroup:
		return observation.Phase == cicontract.TimingTestBinaryCompile
	default:
		return false
	}
}

// shardOverheadSampleFromTiming 保存 workload envelope，并由 union 计算 v2 overhead。
func shardOverheadSampleFromTiming(key shardOverheadObservationKey, total TimingObservation, workloads, accounted []TimingObservation) (ShardOrchestrationOverheadSample, error) {
	start, end := workloads[0].StartedAt, workloads[0].CompletedAt
	for _, workload := range workloads[1:] {
		if workload.StartedAt.Before(start) {
			start = workload.StartedAt
		}
		if workload.CompletedAt.After(end) {
			end = workload.CompletedAt
		}
	}
	sample := ShardOrchestrationOverheadSample{
		JobID: key.jobID, ShardIdentity: key.shard,
		TotalStartedAt: total.StartedAt, TotalCompletedAt: total.CompletedAt,
		WorkloadEnvelopeStart: start, WorkloadEnvelopeEnd: end,
	}
	accountedDurationMS, err := shardOverheadAccountedIntervalDuration(total, accounted)
	if err != nil {
		return ShardOrchestrationOverheadSample{}, fmt.Errorf("derive shard overhead sample %s/%s: %w", key.jobID, key.shard, err)
	}
	sample.AccountedDurationMS = accountedDurationMS
	sample.AccountedIntervalCount = len(accounted)
	sample.OverheadMS = total.DurationMS - accountedDurationMS
	if err := ValidateShardOrchestrationOverheadSampleIntervals(sample); err != nil {
		return ShardOrchestrationOverheadSample{}, fmt.Errorf("derive shard overhead sample %s/%s: %w", key.jobID, key.shard, err)
	}
	return sample, nil
}

// shardOverheadAccountedIntervalDuration 计算 measured accounted interval 的精确 union。
func shardOverheadAccountedIntervalDuration(total TimingObservation, observations []TimingObservation) (int64, error) {
	if len(observations) == 0 {
		return 0, errors.New("shard overhead accounted interval union is empty")
	}
	intervals := append([]TimingObservation(nil), observations...)
	sort.Slice(intervals, func(left, right int) bool {
		if intervals[left].StartedAt.Equal(intervals[right].StartedAt) {
			return intervals[left].CompletedAt.Before(intervals[right].CompletedAt)
		}
		return intervals[left].StartedAt.Before(intervals[right].StartedAt)
	})
	if err := validateShardOverheadAccountedIntervals(total, intervals); err != nil {
		return 0, err
	}
	return mergeShardOverheadIntervals(intervals), nil
}

// validateShardOverheadAccountedIntervals 确保每个被计入 union 的 interval
// 完整落在 shard total 内。
func validateShardOverheadAccountedIntervals(total TimingObservation, intervals []TimingObservation) error {
	for _, interval := range intervals {
		if interval.StartedAt.Before(total.StartedAt) || interval.CompletedAt.After(total.CompletedAt) {
			return errors.New("shard overhead accounted interval is outside shard total")
		}
	}
	return nil
}

// mergeShardOverheadIntervals 合并排序后的重叠或相邻区间并返回 union 毫秒数。
func mergeShardOverheadIntervals(intervals []TimingObservation) int64 {
	var unionStart, unionEnd time.Time
	var unionMS int64
	for _, interval := range intervals {
		if unionStart.IsZero() {
			unionStart, unionEnd = interval.StartedAt, interval.CompletedAt
			continue
		}
		if interval.StartedAt.After(unionEnd) {
			unionMS += unionEnd.Sub(unionStart).Milliseconds()
			unionStart, unionEnd = interval.StartedAt, interval.CompletedAt
			continue
		}
		if interval.CompletedAt.After(unionEnd) {
			unionEnd = interval.CompletedAt
		}
	}
	unionMS += unionEnd.Sub(unionStart).Milliseconds()
	return unionMS
}

// ValidateShardOrchestrationOverheadSampleIntervals 校验尚未绑定 generation/digest 的计时样本。
func ValidateShardOrchestrationOverheadSampleIntervals(sample ShardOrchestrationOverheadSample) error {
	if err := validateShardOverheadSampleIdentity(sample); err != nil {
		return err
	}
	if err := validateShardOverheadWorkloadEnvelope(sample); err != nil {
		return err
	}
	if err := validateShardOverheadAccountedSummary(sample); err != nil {
		return err
	}
	return validateShardOverheadFormula(sample)
}

// validateShardOverheadSampleIdentity 校验样本主键及 total interval。
func validateShardOverheadSampleIdentity(sample ShardOrchestrationOverheadSample) error {
	if strings.TrimSpace(sample.JobID) == "" || strings.TrimSpace(sample.ShardIdentity) == "" || sample.TotalStartedAt.IsZero() || !sample.TotalCompletedAt.After(sample.TotalStartedAt) {
		return errors.New("shard overhead timing intervals are invalid")
	}
	if err := validateUTCTimingInterval(sample.TotalStartedAt, sample.TotalCompletedAt, "shard overhead total"); err != nil {
		return err
	}
	return nil
}

// validateShardOverheadWorkloadEnvelope 保留并校验 workload 的真实 envelope，
// 但不再用 envelope 替代 accounted interval union。
func validateShardOverheadWorkloadEnvelope(sample ShardOrchestrationOverheadSample) error {
	if sample.WorkloadEnvelopeStart.IsZero() || !sample.WorkloadEnvelopeEnd.After(sample.WorkloadEnvelopeStart) || sample.WorkloadEnvelopeStart.Before(sample.TotalStartedAt) || sample.WorkloadEnvelopeEnd.After(sample.TotalCompletedAt) {
		return errors.New("shard overhead workload envelope is invalid")
	}
	if err := validateUTCTimingInterval(sample.WorkloadEnvelopeStart, sample.WorkloadEnvelopeEnd, "shard overhead workload envelope"); err != nil {
		return err
	}
	return nil
}

// validateShardOverheadAccountedSummary 校验 union 的可审计时长、计数和总时长边界。
func validateShardOverheadAccountedSummary(sample ShardOrchestrationOverheadSample) error {
	if sample.AccountedDurationMS <= 0 || sample.AccountedIntervalCount <= 0 {
		return errors.New("shard overhead accounted interval summary is invalid")
	}
	if sample.AccountedDurationMS > sample.TotalCompletedAt.Sub(sample.TotalStartedAt).Milliseconds() {
		return errors.New("shard overhead accounted interval union exceeds total interval")
	}
	return nil
}

// validateShardOverheadFormula 锁定 total 减 accounted union 的唯一公式。
func validateShardOverheadFormula(sample ShardOrchestrationOverheadSample) error {
	want := sample.TotalCompletedAt.Sub(sample.TotalStartedAt).Milliseconds() - sample.AccountedDurationMS
	if sample.OverheadMS != want {
		return errors.New("shard overhead timing does not equal total minus accounted interval union")
	}
	return nil
}

func shardOverheadSamplesDigest(samples []ShardOrchestrationOverheadSample) (string, error) {
	payload := struct {
		Policy  string                             `json:"policy"`
		Samples []ShardOrchestrationOverheadSample `json:"samples"`
	}{Policy: ShardOverheadPolicyVersion, Samples: samples}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode shard overhead provenance: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func nearestRankP95(values []int64) int64 {
	sorted := append([]int64(nil), values...)
	slices.Sort(sorted)
	rank := (len(sorted)*95 + 99) / 100
	return sorted[rank-1]
}

// CacheObservationStatus 是单个缓存输入的已观察状态。
type CacheObservationStatus string

const (
	CacheObservationHit           CacheObservationStatus = "hit"
	CacheObservationMiss          CacheObservationStatus = "miss"
	CacheObservationPut           CacheObservationStatus = "put"
	CacheObservationNotApplicable CacheObservationStatus = "not_applicable"
)

// GoCacheEvidence 记录 Go cache 的来源、状态和精确计数。
type GoCacheEvidence struct {
	Source              string                      `json:"source"`
	Status              CacheObservationStatus      `json:"status"`
	Measurement         cicontract.ObservationState `json:"measurement"`
	PrivateHits         uint64                      `json:"private_hits"`
	BaselineHits        uint64                      `json:"baseline_hits"`
	Misses              uint64                      `json:"misses"`
	Puts                uint64                      `json:"puts"`
	NotApplicableReason string                      `json:"not_applicable_reason,omitempty"`
}

// FrontendCacheEvidence 记录前端 cache seed 的逐项观察状态。
type FrontendCacheEvidence struct {
	NodeModulesSeed FrontendCacheObservation `json:"node_modules_seed"`
	NPM             FrontendCacheObservation `json:"npm"`
	Vite            FrontendCacheObservation `json:"vite"`
	Playwright      FrontendCacheObservation `json:"playwright"`
}

// FrontendCacheObservation 为每一项前端 cache 保留独立状态和 N/A 原因。
type FrontendCacheObservation struct {
	Status              CacheObservationStatus `json:"status"`
	NotApplicableReason string                 `json:"not_applicable_reason,omitempty"`
}

// CacheEvidence 是写入 SQLite 的结构化 cache 证据，禁止使用不透明文本。
type CacheEvidence struct {
	Go       GoCacheEvidence       `json:"go"`
	Frontend FrontendCacheEvidence `json:"frontend"`
}

// NewNotApplicableCacheEvidence 为不关联 workload cache 的阶段创建显式 N/A 证据。
func NewNotApplicableCacheEvidence(reason string) CacheEvidence {
	return CacheEvidence{
		Go:       GoCacheEvidence{Source: "not_applicable", Status: CacheObservationNotApplicable, Measurement: cicontract.ObservationNotApplicable, NotApplicableReason: reason},
		Frontend: FrontendCacheEvidence{NodeModulesSeed: FrontendCacheObservation{Status: CacheObservationNotApplicable, NotApplicableReason: reason}, NPM: FrontendCacheObservation{Status: CacheObservationNotApplicable, NotApplicableReason: reason}, Vite: FrontendCacheObservation{Status: CacheObservationNotApplicable, NotApplicableReason: reason}, Playwright: FrontendCacheObservation{Status: CacheObservationNotApplicable, NotApplicableReason: reason}},
	}
}

// NewTimingCacheEvidenceFromProfile 将执行器已验证的 profile 绑定为 workload cache 证据。
func NewTimingCacheEvidenceFromProfile(profile ExecutionProfile) CacheEvidence {
	if profile.CacheMeasurement != string(cicontract.ObservationMeasured) {
		return NewNotApplicableCacheEvidence("workload_cache_not_measured")
	}
	evidence := CacheEvidence{Go: GoCacheEvidence{
		Source: profile.CacheSource, Status: profile.CacheStatus, Measurement: cicontract.ObservationMeasured,
		PrivateHits: profile.PrivateHitCount, BaselineHits: profile.BaselineHitCount, Misses: profile.CacheMissCount, Puts: profile.CachePutCount,
	}}
	if profile.Frontend == nil {
		evidence.Frontend = NewNotApplicableCacheEvidence("not_frontend_workload").Frontend
		return evidence
	}
	frontend := profile.Frontend
	evidence.Frontend = FrontendCacheEvidence{
		NodeModulesSeed: frontendCacheObservationFromProfile(frontend.NodeModulesSeedHit, frontend.NodeModulesSeedNotApplicableReason),
		NPM:             frontendCacheObservationFromProfile(frontend.NPMCacheHit, frontend.NPMCacheNotApplicableReason),
		Vite:            frontendCacheObservationFromProfile(frontend.ViteCacheHit, frontend.ViteCacheNotApplicableReason),
		Playwright:      frontendCacheObservationFromProfile(frontend.PlaywrightBrowserHit, frontend.PlaywrightBrowserNotApplicableReason),
	}
	return evidence
}

// NewCompileGroupCacheEvidence 将 worker 聚合 Go cache 计数投影为与 workload
// 相同的结构化证据；计数是权威事实，Status 仅是规范摘要。
func NewCompileGroupCacheEvidence(execution CompileGroupExecution) CacheEvidence {
	status := CacheObservationNotApplicable
	switch {
	case execution.CacheMisses > 0:
		status = CacheObservationMiss
	case execution.CachePuts > 0:
		status = CacheObservationPut
	case execution.CacheHits > 0:
		status = CacheObservationHit
	}
	return CacheEvidence{
		Go:       GoCacheEvidence{Source: "go_build_cache", Status: status, Measurement: cicontract.ObservationMeasured, PrivateHits: execution.CacheHits, Misses: execution.CacheMisses, Puts: execution.CachePuts},
		Frontend: NewNotApplicableCacheEvidence("compile_group_not_frontend").Frontend,
	}
}

func frontendCacheObservationFromProfile(hit bool, notApplicableReason string) FrontendCacheObservation {
	if hit {
		return FrontendCacheObservation{Status: CacheObservationHit}
	}
	if notApplicableReason != "" {
		return FrontendCacheObservation{Status: CacheObservationNotApplicable, NotApplicableReason: notApplicableReason}
	}
	return FrontendCacheObservation{Status: CacheObservationMiss}
}

// Validate 验证严格 cache evidence，拒绝未测量与隐式 N/A。
func (evidence CacheEvidence) Validate() error {
	goEvidence := evidence.Go
	if goEvidence.Measurement == cicontract.ObservationNotApplicable {
		if goEvidence.Source != "not_applicable" || goEvidence.Status != CacheObservationNotApplicable || strings.TrimSpace(goEvidence.NotApplicableReason) == "" || goEvidence.PrivateHits != 0 || goEvidence.BaselineHits != 0 || goEvidence.Misses != 0 || goEvidence.Puts != 0 {
			return errors.New("not_applicable Go cache evidence is invalid")
		}
	} else if goEvidence.Measurement != cicontract.ObservationMeasured || goEvidence.NotApplicableReason != "" {
		return errors.New("measured Go cache evidence is invalid")
	} else if goEvidence.Source == "none" {
		if goEvidence.Status != CacheObservationNotApplicable || goEvidence.PrivateHits != 0 || goEvidence.BaselineHits != 0 || goEvidence.Misses != 0 || goEvidence.Puts != 0 {
			return errors.New("measured zero-lookup Go cache evidence is invalid")
		}
	} else if goEvidence.Source != "go_build_cache" {
		return errors.New("measured Go cache evidence source is invalid")
	} else {
		switch goEvidence.Status {
		case CacheObservationHit, CacheObservationMiss, CacheObservationPut:
		case CacheObservationNotApplicable:
			if goEvidence.PrivateHits != 0 || goEvidence.BaselineHits != 0 || goEvidence.Misses != 0 || goEvidence.Puts != 0 {
				return errors.New("measured Go cache zero-lookup evidence has observations")
			}
		default:
			return errors.New("measured Go cache status is invalid")
		}
	}
	frontend := evidence.Frontend
	for _, observation := range []FrontendCacheObservation{frontend.NodeModulesSeed, frontend.NPM, frontend.Vite, frontend.Playwright} {
		if observation.Status != CacheObservationHit && observation.Status != CacheObservationMiss && observation.Status != CacheObservationNotApplicable {
			return errors.New("frontend cache status is invalid")
		}
		if observation.Status == CacheObservationNotApplicable && strings.TrimSpace(observation.NotApplicableReason) == "" {
			return errors.New("frontend cache N/A status needs a reason")
		}
		if observation.Status != CacheObservationNotApplicable && observation.NotApplicableReason != "" {
			return errors.New("frontend cache N/A reason conflicts with observed status")
		}
	}
	return nil
}

// TimingObservation is one authority-bound raw or derived remote-CI interval.
type TimingObservation struct {
	JobID         string                       `json:"job_id"`
	Scope         cicontract.TimingScope       `json:"scope"`
	ShardIdentity string                       `json:"shard_identity,omitempty"`
	WorkloadID    GateID                       `json:"workload_id,omitempty"`
	Phase         cicontract.TimingPhase       `json:"phase"`
	StartedAt     time.Time                    `json:"started_at"`
	CompletedAt   time.Time                    `json:"completed_at"`
	DurationMS    int64                        `json:"duration_ms"`
	Measurement   cicontract.ObservationState  `json:"measurement"`
	Reason        string                       `json:"reason,omitempty"`
	Aggregation   cicontract.TimingAggregation `json:"aggregation"`
	CacheEvidence CacheEvidence                `json:"cache_evidence"`
	// CompileGroup* 字段仅用于 scope=compile_group；保留在同一行可维持单一 SQLite 权威账本。
	CompileGroupID           string   `json:"compile_group_id,omitempty"`
	CompileArtifactKey       string   `json:"compile_artifact_key,omitempty"`
	CompilePackageTarget     string   `json:"compile_package_target,omitempty"`
	CompileWorkloadIDs       []GateID `json:"compile_workload_ids,omitempty"`
	CompileArtifactSHA256    string   `json:"compile_artifact_sha256,omitempty"`
	CompileArtifactSize      int64    `json:"compile_artifact_size,omitempty"`
	CompileCacheHits         uint64   `json:"compile_cache_hits,omitempty"`
	CompileCacheMisses       uint64   `json:"compile_cache_misses,omitempty"`
	CompileCachePuts         uint64   `json:"compile_cache_puts,omitempty"`
	CompileCacheStatus       string   `json:"compile_cache_status,omitempty"`
	CompileStatus            string   `json:"compile_status,omitempty"`
	CompileExitCode          int      `json:"compile_exit_code,omitempty"`
	CompileErrorText         string   `json:"compile_error_text,omitempty"`
	CompileCommandDigest     string   `json:"compile_command_digest,omitempty"`
	CompileProfileDigest     string   `json:"compile_profile_digest,omitempty"`
	CompileResourceClassID   string   `json:"compile_resource_class_id,omitempty"`
	CompileResourceCPU       float64  `json:"compile_resource_cpu,omitempty"`
	CompileResourceMemoryGiB float64  `json:"compile_resource_memory_gib,omitempty"`
	CompileExecutionMode     string   `json:"compile_execution_mode,omitempty"`
}

type observationKey struct {
	scope           cicontract.TimingScope
	shard           string
	workload        GateID
	phase           cicontract.TimingPhase
	compileGroup    string
	compileArtifact string
}

func validateUTCTimingInterval(startedAt, completedAt time.Time, label string) error {
	if startedAt.Location() != time.UTC || completedAt.Location() != time.UTC {
		return fmt.Errorf("%s timestamps must use UTC", label)
	}
	return nil
}

// Validate rejects missing authority evidence and unreasoned non-applicability.
func (observation TimingObservation) Validate() error {
	if strings.TrimSpace(observation.JobID) == "" || strings.TrimSpace(string(observation.Phase)) == "" {
		return errors.New("timing observation identity, phase, and cache evidence are required")
	}
	if err := observation.CacheEvidence.Validate(); err != nil {
		return fmt.Errorf("timing observation cache evidence: %w", err)
	}
	if observation.Measurement == "not_measured" || observation.Measurement == "" {
		return errors.New("timing observation must not claim not_measured authority")
	}
	if err := observation.validateScopeBinding(); err != nil {
		return err
	}
	switch observation.Aggregation {
	case cicontract.TimingAggregationRaw, cicontract.TimingAggregationIntervalUnion, cicontract.TimingAggregationCriticalPath:
	default:
		return errors.New("timing observation aggregation is invalid")
	}
	if observation.Measurement == cicontract.ObservationNotApplicable {
		if strings.TrimSpace(observation.Reason) == "" || !observation.StartedAt.IsZero() || !observation.CompletedAt.IsZero() || observation.DurationMS != 0 {
			return errors.New("not_applicable timing observation needs only a reason")
		}
		return nil
	}
	if observation.Measurement != cicontract.ObservationMeasured || strings.TrimSpace(observation.Reason) != "" || observation.StartedAt.IsZero() || !observation.CompletedAt.After(observation.StartedAt) {
		return fmt.Errorf("measured timing observation interval is invalid")
	}
	if err := validateUTCTimingInterval(observation.StartedAt, observation.CompletedAt, "timing observation"); err != nil {
		return err
	}
	envelopeMS := observation.CompletedAt.Sub(observation.StartedAt).Milliseconds()
	if observation.DurationMS <= 0 {
		return errors.New("measured timing observation duration_ms must be positive")
	}
	switch observation.Aggregation {
	case cicontract.TimingAggregationRaw, cicontract.TimingAggregationCriticalPath:
		if observation.DurationMS != envelopeMS {
			return errors.New("raw or critical_path timing duration_ms must equal its real interval")
		}
	case cicontract.TimingAggregationIntervalUnion:
		if observation.DurationMS > envelopeMS {
			return errors.New("interval_union timing duration_ms exceeds its envelope")
		}
	}
	return nil
}

// ValidateAuthoritativeTimingObservations requires the exact shard/workload phase
// projection before a receipt may claim authority.
func ValidateAuthoritativeTimingObservations(jobID string, observations []TimingObservation, executions []PlanGateExecution, shards []RemoteCIShardRecord) error {
	expected := map[observationKey]struct{}{{scope: cicontract.TimingScopeRun, phase: cicontract.TimingTotal}: {}}
	shardByWorkload := make(map[GateID]string)
	shardSet := make(map[string]struct{}, len(shards))
	for _, shard := range shards {
		shardSet[shard.ShardIdentity] = struct{}{}
		for _, phase := range cicontract.TimingPhases() {
			expected[observationKey{scope: cicontract.TimingScopeShard, shard: shard.ShardIdentity, phase: phase}] = struct{}{}
		}
		for _, workloadID := range shard.Workloads {
			shardByWorkload[workloadID] = shard.ShardIdentity
		}
	}
	executionByWorkload := make(map[GateID]PlanGateExecution, len(executions))
	for _, execution := range executions {
		if expectedShard, exists := shardByWorkload[execution.GateID]; !exists || execution.ShardIdentity != expectedShard {
			return fmt.Errorf("authoritative workload execution %q shard binding is invalid", execution.GateID)
		}
		if _, duplicate := executionByWorkload[execution.GateID]; duplicate {
			return fmt.Errorf("authoritative workload execution %q is duplicated", execution.GateID)
		}
		executionByWorkload[execution.GateID] = execution
		for _, phase := range cicontract.TimingPhases() {
			expected[observationKey{scope: cicontract.TimingScopeWorkload, shard: execution.ShardIdentity, workload: execution.GateID, phase: phase}] = struct{}{}
		}
	}
	if len(executionByWorkload) != len(shardByWorkload) {
		return errors.New("authoritative workload executions do not exactly cover shard workloads")
	}
	observed := make(map[observationKey]TimingObservation, len(observations))
	for _, observation := range observations {
		key, err := validateAuthoritativeTimingObservation(jobID, observation, expected, shardSet, shardByWorkload)
		if err != nil {
			return err
		}
		if _, duplicate := observed[key]; duplicate {
			return fmt.Errorf("authoritative timing phase is duplicated for scope=%q shard=%q workload=%q phase=%q", observation.Scope, observation.ShardIdentity, observation.WorkloadID, observation.Phase)
		}
		observed[key] = observation
	}
	for key := range expected {
		if _, exists := observed[key]; !exists {
			return fmt.Errorf("authoritative timing is missing scope=%q shard=%q workload=%q phase=%q", key.scope, key.shard, key.workload, key.phase)
		}
	}
	for _, shard := range shards {
		phases := make(map[cicontract.TimingPhase]TimingObservation, len(cicontract.TimingPhases()))
		for _, phase := range cicontract.TimingPhases() {
			observation := observed[observationKey{scope: cicontract.TimingScopeShard, shard: shard.ShardIdentity, phase: phase}]
			phases[phase] = observation
			if observation.Measurement != cicontract.ObservationMeasured {
				return fmt.Errorf("authoritative shard %q phase %q must be measured", shard.ShardIdentity, phase)
			}
			wantAggregation := cicontract.TimingAggregationRaw
			switch phase {
			case cicontract.TimingStartup, cicontract.TimingTestBody:
				wantAggregation = cicontract.TimingAggregationIntervalUnion
			case cicontract.TimingTotal:
				wantAggregation = cicontract.TimingAggregationCriticalPath
			}
			if observation.Aggregation != wantAggregation {
				return fmt.Errorf("authoritative shard %q phase %q aggregation is invalid", shard.ShardIdentity, phase)
			}
		}
		if err := validateAuthoritativeShardTimingOrder(shard.ShardIdentity, phases); err != nil {
			return err
		}
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody} {
			if err := validateShardIntervalUnion(shard.ShardIdentity, phase, phases[phase], observed, shard.Workloads); err != nil {
				return err
			}
		}
	}
	runTotal := observed[observationKey{scope: cicontract.TimingScopeRun, phase: cicontract.TimingTotal}]
	if runTotal.Measurement != cicontract.ObservationMeasured || runTotal.Aggregation != cicontract.TimingAggregationCriticalPath {
		return errors.New("authoritative run total must be a measured critical path")
	}
	for _, shard := range shards {
		shardTotal := observed[observationKey{scope: cicontract.TimingScopeShard, shard: shard.ShardIdentity, phase: cicontract.TimingTotal}]
		if !intervalContains(runTotal, shardTotal) {
			return fmt.Errorf("authoritative run total does not contain shard %q total", shard.ShardIdentity)
		}
	}
	for workloadID, execution := range executionByWorkload {
		if execution.ExecutionProfile.CacheMeasurement != string(cicontract.ObservationMeasured) {
			return fmt.Errorf("authoritative workload %q cache evidence is not measured", workloadID)
		}
		phases := make(map[cicontract.TimingPhase]TimingObservation, len(cicontract.TimingPhases()))
		for _, phase := range cicontract.TimingPhases() {
			phases[phase] = observed[observationKey{scope: cicontract.TimingScopeWorkload, shard: execution.ShardIdentity, workload: workloadID, phase: phase}]
		}
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingECIWait, cicontract.TimingSourceMaterialize, cicontract.TimingCandidateCompile} {
			observation := phases[phase]
			if observation.Measurement != cicontract.ObservationNotApplicable || observation.Reason != "shard_scoped:"+execution.ShardIdentity {
				return fmt.Errorf("workload %q shard-scoped phase %q is not explicit not_applicable", workloadID, phase)
			}
		}
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
			if phases[phase].Measurement != cicontract.ObservationMeasured {
				return fmt.Errorf("authoritative workload %q phase %q must be measured", workloadID, phase)
			}
			if phases[phase].Aggregation != cicontract.TimingAggregationRaw {
				return fmt.Errorf("authoritative workload %q phase %q must be a raw interval", workloadID, phase)
			}
			if !reflect.DeepEqual(phases[phase].CacheEvidence, NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)) {
				return fmt.Errorf("authoritative workload %q phase %q cache evidence does not match execution", workloadID, phase)
			}
		}
		startup, body, total := phases[cicontract.TimingStartup], phases[cicontract.TimingTestBody], phases[cicontract.TimingTotal]
		if !intervalContains(total, startup) || !intervalContains(total, body) {
			return fmt.Errorf("authoritative workload %q total does not contain startup and test body", workloadID)
		}
		if startup.CompletedAt.After(body.StartedAt) {
			return fmt.Errorf("authoritative workload %q startup overlaps test body", workloadID)
		}
		if execution.ExecutionProfile.TestBodyMS > 0 && !phases[cicontract.TimingTestBody].CompletedAt.After(phases[cicontract.TimingTestBody].StartedAt) {
			return fmt.Errorf("test-bearing workload %q has empty test body", workloadID)
		}
	}
	return nil
}

func validateAuthoritativeShardTimingOrder(shardIdentity string, phases map[cicontract.TimingPhase]TimingObservation) error {
	eciWait := phases[cicontract.TimingECIWait]
	source := phases[cicontract.TimingSourceMaterialize]
	compile := phases[cicontract.TimingCandidateCompile]
	startup := phases[cicontract.TimingStartup]
	body := phases[cicontract.TimingTestBody]
	total := phases[cicontract.TimingTotal]
	if eciWait.CompletedAt.After(source.StartedAt) {
		return fmt.Errorf("authoritative shard %q ECI wait overlaps source materialization", shardIdentity)
	}
	if source.CompletedAt.After(compile.StartedAt) {
		return fmt.Errorf("authoritative shard %q materialization phases overlap", shardIdentity)
	}
	if compile.CompletedAt.After(startup.StartedAt) || compile.CompletedAt.After(body.StartedAt) {
		return fmt.Errorf("authoritative shard %q compile overlaps execution", shardIdentity)
	}
	if !eciWait.StartedAt.Equal(total.StartedAt) {
		return fmt.Errorf("authoritative shard %q total does not start at ECI creation", shardIdentity)
	}
	for _, phase := range cicontract.TimingPhases() {
		if !intervalContains(total, phases[phase]) {
			return fmt.Errorf("authoritative shard %q total does not contain phase %q", shardIdentity, phase)
		}
	}
	return nil
}

func intervalContains(parent, child TimingObservation) bool {
	return !child.StartedAt.Before(parent.StartedAt) && !child.CompletedAt.After(parent.CompletedAt)
}

func validateShardIntervalUnion(shardIdentity string, phase cicontract.TimingPhase, aggregate TimingObservation, observed map[observationKey]TimingObservation, workloads []GateID) error {
	intervals := make([]TimingObservation, 0, len(workloads))
	for _, workloadID := range workloads {
		interval := observed[observationKey{scope: cicontract.TimingScopeWorkload, shard: shardIdentity, workload: workloadID, phase: phase}]
		if interval.Measurement != cicontract.ObservationMeasured || interval.Aggregation != cicontract.TimingAggregationRaw {
			return fmt.Errorf("authoritative shard %q interval_union phase %q lacks a raw workload interval", shardIdentity, phase)
		}
		intervals = append(intervals, interval)
	}
	if len(intervals) == 0 {
		return fmt.Errorf("authoritative shard %q interval_union phase %q has no workload intervals", shardIdentity, phase)
	}
	sort.Slice(intervals, func(left, right int) bool { return intervals[left].StartedAt.Before(intervals[right].StartedAt) })
	start, end := intervals[0].StartedAt, intervals[0].CompletedAt
	var durationMS int64
	for _, interval := range intervals[1:] {
		if interval.StartedAt.After(end) {
			durationMS += end.Sub(start).Milliseconds()
			start, end = interval.StartedAt, interval.CompletedAt
			continue
		}
		if interval.CompletedAt.After(end) {
			end = interval.CompletedAt
		}
	}
	durationMS += end.Sub(start).Milliseconds()
	if !aggregate.StartedAt.Equal(intervals[0].StartedAt) || !aggregate.CompletedAt.Equal(end) || aggregate.DurationMS != durationMS {
		return fmt.Errorf("authoritative shard %q interval_union phase %q does not equal the exact workload union", shardIdentity, phase)
	}
	return nil
}
