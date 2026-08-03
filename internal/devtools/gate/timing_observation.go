package gate

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

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
}

type observationKey struct {
	scope    cicontract.TimingScope
	shard    string
	workload GateID
	phase    cicontract.TimingPhase
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
	switch observation.Scope {
	case cicontract.TimingScopeRun:
		if observation.ShardIdentity != "" || observation.WorkloadID != "" {
			return errors.New("run timing observation has subject binding")
		}
	case cicontract.TimingScopeShard:
		if observation.ShardIdentity == "" || observation.WorkloadID != "" {
			return errors.New("shard timing observation binding is invalid")
		}
	case cicontract.TimingScopeWorkload:
		if observation.ShardIdentity == "" || observation.WorkloadID == "" {
			return errors.New("workload timing observation binding is invalid")
		}
	default:
		return errors.New("timing observation scope is invalid")
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
	for _, shard := range shards {
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
		if observation.JobID != jobID {
			return errors.New("authoritative timing observation job binding is invalid")
		}
		if err := observation.Validate(); err != nil {
			return err
		}
		key := observationKey{scope: observation.Scope, shard: observation.ShardIdentity, workload: observation.WorkloadID, phase: observation.Phase}
		if _, exists := expected[key]; !exists {
			return fmt.Errorf("authoritative timing has extra scope=%q shard=%q workload=%q phase=%q", observation.Scope, observation.ShardIdentity, observation.WorkloadID, observation.Phase)
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
