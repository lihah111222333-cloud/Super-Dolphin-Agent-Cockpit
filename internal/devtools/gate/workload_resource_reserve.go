package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type resourceTierReserveSample struct {
	ResourceTier cicontract.WorkloadResourceTier `json:"resource_tier"`
	JobID        string                          `json:"job_id"`
	Shard        string                          `json:"shard_identity"`
	ReserveMS    int64                           `json:"reserve_ms"`
}

type normalReserveShardResource struct {
	key       shardOverheadObservationKey
	resources RemoteCIShardResources
}

func planningContextsEqual(left, right PlanningContext) bool {
	return reflect.DeepEqual(left, right)
}

// EffectiveTargetDurationForResourceTier 计算指定资源档位的有效规划目标时长。
// 校准模式沿用固定目标；normal 模式扣除该档位经实测的外部/编排 reserve，并拒绝无正向预算的输入。
func (context PlanningContext) EffectiveTargetDurationForResourceTier(tier cicontract.WorkloadResourceTier) (int64, error) {
	if err := validatePlanningContext(context); err != nil {
		return 0, err
	}
	if context.Calibration {
		if tier != 0 {
			return 0, errors.New("calibration planning has no normal resource tier")
		}
		return context.TargetDurationMS, nil
	}
	reserve, err := resourceTierReserve(context.ResourceTierReserves, tier)
	if err != nil {
		return 0, err
	}
	if reserve.Resolution != cicontract.WorkloadResourceTierReserveResolutionMeasured || reserve.Measurement == nil {
		return 0, fmt.Errorf("resource-tier %d bootstrap resolution has no effective packing target", tier)
	}
	if reserve.Measurement.P95MS >= context.TargetDurationMS {
		return 0, fmt.Errorf("resource-tier %d reserve leaves no positive planning budget", tier)
	}
	return context.TargetDurationMS - reserve.Measurement.P95MS, nil
}

func resourceTierReserve(reserves []cicontract.WorkloadResourceTierReserve, tier cicontract.WorkloadResourceTier) (cicontract.WorkloadResourceTierReserve, error) {
	if tier < cicontract.WorkloadResourceTierFast || tier > cicontract.WorkloadResourceTierSlow {
		return cicontract.WorkloadResourceTierReserve{}, fmt.Errorf("resource tier %d is invalid", tier)
	}
	if len(reserves) != int(cicontract.WorkloadResourceTierSlow) {
		return cicontract.WorkloadResourceTierReserve{}, fmt.Errorf("resource-tier reserve set is incomplete: got %d", len(reserves))
	}
	reserve := reserves[int(tier)-1]
	if reserve.ResourceTier != tier {
		return cicontract.WorkloadResourceTierReserve{}, fmt.Errorf("resource-tier reserve for tier %d is missing", tier)
	}
	return reserve, nil
}

// canonicalizeNormalResourceTierReserves 将 normal reserve 规范化为 Fast→Slow 的固定序列。
// 输入必须按资源档位严格递增且处于允许范围内，缺失档位保留 bootstrap 默认值。
func canonicalizeNormalResourceTierReserves(reserves []cicontract.WorkloadResourceTierReserve) ([]cicontract.WorkloadResourceTierReserve, error) {
	canonical := make([]cicontract.WorkloadResourceTierReserve, int(cicontract.WorkloadResourceTierSlow))
	for tier := cicontract.WorkloadResourceTierFast; tier <= cicontract.WorkloadResourceTierSlow; tier++ {
		canonical[int(tier)-1] = cicontract.WorkloadResourceTierReserve{SchemaVersion: cicontract.WorkloadResourceTierReserveSchemaVersion, ResourceTier: tier, Resolution: cicontract.WorkloadResourceTierReserveResolutionBootstrap}
	}
	previous := cicontract.WorkloadResourceTier(0)
	for index, reserve := range reserves {
		if reserve.ResourceTier <= previous || reserve.ResourceTier < cicontract.WorkloadResourceTierFast || reserve.ResourceTier > cicontract.WorkloadResourceTierSlow {
			return nil, fmt.Errorf("resource-tier reserve %d has non-canonical or duplicate tier %d", index, reserve.ResourceTier)
		}
		previous = reserve.ResourceTier
		canonical[int(reserve.ResourceTier)-1] = reserve
	}
	return canonical, nil
}

func isolateBootstrapCompileUnits(units []compilePlanningUnit) ([]ShardPlan, error) {
	shards := make([]ShardPlan, len(units))
	for index, unit := range units {
		if len(unit.workloads) == 0 || unit.costMS <= 0 {
			return nil, fmt.Errorf("bootstrap compile unit %q is invalid", unit.sortID)
		}
		shards[index] = ShardPlan{Index: index, Workloads: append([]PlannedWorkload(nil), unit.workloads...), EstimatedDurationMS: unit.costMS}
		if unit.group != nil {
			shards[index].CompileGroupIDs = []string{unit.group.GroupID}
		}
	}
	return shards, nil
}

// DeriveNormalResourceTierReserves 从权威 timing observations 推导按资源档位隔离的 P95 reserve。
// 仅排除一次 workload 总时长和 test-binary 编译区间，其余 shard 时间形成 planning 必须预留的 eci/source/candidate/orchestration 并集。
func DeriveNormalResourceTierReserves(observations []TimingObservation, shards []RemoteCIShardRecord, calibrationClassID string) ([]cicontract.WorkloadResourceTierReserve, error) {
	if err := validateNormalReserveCalibrationClass(calibrationClassID); err != nil {
		return nil, err
	}
	if len(observations) == 0 {
		return nil, nil
	}
	jobsByShard, err := normalReserveObservationJobsByShard(observations)
	if err != nil {
		return nil, err
	}
	keyedResources := make([]normalReserveShardResource, 0, len(shards))
	for _, shard := range shards {
		if err := validateNormalReserveShardIdentity(shard.ShardIdentity); err != nil {
			return nil, err
		}
		jobID, exists := jobsByShard[shard.ShardIdentity]
		if !exists {
			continue
		}
		keyedResources = append(keyedResources, normalReserveShardResource{
			key:       shardOverheadObservationKey{jobID: jobID, shard: shard.ShardIdentity},
			resources: shard.Resources,
		})
	}
	return deriveNormalResourceTierReservesKeyed(observations, keyedResources, calibrationClassID)
}

func validateNormalReserveCalibrationClass(calibrationClassID string) error {
	if strings.TrimSpace(calibrationClassID) == "" || strings.TrimSpace(calibrationClassID) != calibrationClassID {
		return errors.New("normal resource-tier reserve requires canonical calibration class identity")
	}
	return nil
}

func validateNormalReserveShardIdentity(identity string) error {
	if strings.TrimSpace(identity) == "" || strings.TrimSpace(identity) != identity {
		return fmt.Errorf("validate reserve shard %q identity: identity must be non-empty and canonical", identity)
	}
	return nil
}

func validateNormalReserveObservationKey(key shardOverheadObservationKey) error {
	if strings.TrimSpace(key.jobID) == "" || strings.TrimSpace(key.jobID) != key.jobID {
		return fmt.Errorf("normal resource-tier reserve timing group %q/%q has invalid canonical job identity", key.jobID, key.shard)
	}
	if err := validateNormalReserveShardIdentity(key.shard); err != nil {
		return fmt.Errorf("normal resource-tier reserve timing group %s/%q has invalid shard identity: %w", key.jobID, key.shard, err)
	}
	return nil
}

// normalReserveObservationJobsByShard 从 timing 记录建立分片到 canonical job 的唯一映射。
func normalReserveObservationJobsByShard(observations []TimingObservation) (map[string]string, error) {
	jobsByShard := make(map[string]string)
	for index, observation := range observations {
		if strings.TrimSpace(observation.JobID) == "" || strings.TrimSpace(observation.JobID) != observation.JobID {
			return nil, fmt.Errorf("normal resource-tier reserve observation %d has invalid canonical job identity", index)
		}
		if observation.ShardIdentity == "" {
			continue
		}
		if err := validateNormalReserveShardIdentity(observation.ShardIdentity); err != nil {
			return nil, fmt.Errorf("normal resource-tier reserve observation %d: %w", index, err)
		}
		prior, exists := jobsByShard[observation.ShardIdentity]
		if exists && prior != observation.JobID {
			return nil, fmt.Errorf("normal resource-tier reserve shard %q maps to multiple jobs %q and %q; keyed SQLite path is required", observation.ShardIdentity, prior, observation.JobID)
		}
		jobsByShard[observation.ShardIdentity] = observation.JobID
	}
	return jobsByShard, nil
}

// validateNormalReserveObservationIdentities 拒绝 reserve 输入中的非 canonical job 或 shard 身份。
func validateNormalReserveObservationIdentities(observations []TimingObservation) error {
	for index, observation := range observations {
		if strings.TrimSpace(observation.JobID) == "" || strings.TrimSpace(observation.JobID) != observation.JobID {
			return fmt.Errorf("normal resource-tier reserve observation %d has invalid canonical job identity", index)
		}
		if observation.ShardIdentity != "" {
			if err := validateNormalReserveShardIdentity(observation.ShardIdentity); err != nil {
				return fmt.Errorf("normal resource-tier reserve observation %d: %w", index, err)
			}
		}
	}
	return nil
}

// deriveNormalResourceTierReservesKeyed 按 job/shard 资源身份聚合 fixed-front P95 reserve。
func deriveNormalResourceTierReservesKeyed(observations []TimingObservation, resources []normalReserveShardResource, calibrationClassID string) ([]cicontract.WorkloadResourceTierReserve, error) {
	if err := validateNormalReserveCalibrationClass(calibrationClassID); err != nil {
		return nil, err
	}
	if err := validateNormalReserveObservationIdentities(observations); err != nil {
		return nil, err
	}
	groups, err := collectShardOverheadObservationGroups(observations)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, nil
	}
	shardTiers, err := normalReserveShardTiers(resources, calibrationClassID)
	if err != nil {
		return nil, err
	}
	samples, err := normalReserveSamples(groups, shardTiers)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nil
	}
	sort.Slice(samples, func(left, right int) bool {
		if samples[left].ResourceTier != samples[right].ResourceTier {
			return samples[left].ResourceTier < samples[right].ResourceTier
		}
		if samples[left].JobID != samples[right].JobID {
			return samples[left].JobID < samples[right].JobID
		}
		return samples[left].Shard < samples[right].Shard
	})
	return normalReserveAggregates(samples)
}

// normalReserveShardTiers 将 keyed normal 分片身份映射到资源档位，并拒绝空白、重复或冲突身份。
func normalReserveShardTiers(shards []normalReserveShardResource, calibrationClassID string) (map[shardOverheadObservationKey]cicontract.WorkloadResourceTier, error) {
	shardTiers := make(map[shardOverheadObservationKey]cicontract.WorkloadResourceTier, len(shards))
	resourcesByKey := make(map[shardOverheadObservationKey]RemoteCIShardResources, len(shards))
	for _, shard := range shards {
		if err := validateNormalReserveObservationKey(shard.key); err != nil {
			return nil, err
		}
		if prior, duplicate := resourcesByKey[shard.key]; duplicate {
			if prior != shard.resources {
				return nil, fmt.Errorf("normal resource-tier reserve shard %s/%s has conflicting resources", shard.key.jobID, shard.key.shard)
			}
			return nil, fmt.Errorf("normal resource-tier reserve shard %s/%s is duplicated", shard.key.jobID, shard.key.shard)
		}
		if err := shard.resources.Validate(); err != nil {
			return nil, fmt.Errorf("validate reserve shard %s/%s resources: %w", shard.key.jobID, shard.key.shard, err)
		}
		resourcesByKey[shard.key] = shard.resources
		if shard.resources.ClassID == calibrationClassID {
			shardTiers[shard.key] = 0
			continue
		}
		tier, err := resourceTierForReserve(shard.resources)
		if err != nil {
			return nil, fmt.Errorf("classify reserve shard %s/%s: %w", shard.key.jobID, shard.key.shard, err)
		}
		shardTiers[shard.key] = tier
	}
	return shardTiers, nil
}

// normalReserveSamples 将 timing group 绑定到已登记的 normal 分片，避免静默丢失样本。
func normalReserveSamples(groups map[shardOverheadObservationKey]*shardOverheadObservationGroup, shardTiers map[shardOverheadObservationKey]cicontract.WorkloadResourceTier) ([]resourceTierReserveSample, error) {
	samples := make([]resourceTierReserveSample, 0, len(groups))
	for key, group := range groups {
		if err := validateNormalReserveObservationKey(key); err != nil {
			return nil, err
		}
		tier, ok := shardTiers[key]
		if !ok {
			return nil, fmt.Errorf("normal resource-tier reserve timing group %s/%s references unregistered shard", key.jobID, key.shard)
		}
		if tier == 0 {
			continue
		}
		sample, err := normalReserveSample(key, group, tier)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

func normalReserveSample(key shardOverheadObservationKey, group *shardOverheadObservationGroup, tier cicontract.WorkloadResourceTier) (resourceTierReserveSample, error) {
	if err := validateShardOverheadGroup(key, group); err != nil {
		return resourceTierReserveSample{}, err
	}
	excluded := normalReserveExcludedIntervals(group)
	union, err := shardOverheadAccountedIntervalDuration(*group.total, excluded)
	if err != nil {
		return resourceTierReserveSample{}, fmt.Errorf("derive resource-tier reserve %s/%s: %w", key.jobID, key.shard, err)
	}
	reserveMS := group.total.DurationMS - union
	if reserveMS < 0 {
		return resourceTierReserveSample{}, fmt.Errorf("resource-tier reserve %s/%s is negative", key.jobID, key.shard)
	}
	return resourceTierReserveSample{ResourceTier: tier, JobID: key.jobID, Shard: key.shard, ReserveMS: reserveMS}, nil
}

func normalReserveExcludedIntervals(group *shardOverheadObservationGroup) []TimingObservation {
	// normalReserveExcludedIntervals 仅保留可装箱的 workload total 与 compile-group test_binary_compile；
	// shard total 减去其精确并集后，固定前置 reserve 覆盖 eci_wait、source_materialize、candidate_compile 与编排余量，
	// 重叠由 union helper 只计一次。
	excluded := append([]TimingObservation(nil), group.workloads...)
	for _, observation := range group.accounted {
		if observation.Scope == cicontract.TimingScopeCompileGroup && observation.Phase == cicontract.TimingTestBinaryCompile {
			excluded = append(excluded, observation)
		}
	}
	return excluded
}

func normalReserveAggregates(samples []resourceTierReserveSample) ([]cicontract.WorkloadResourceTierReserve, error) {
	byTier := make(map[cicontract.WorkloadResourceTier][]int64)
	for _, sample := range samples {
		byTier[sample.ResourceTier] = append(byTier[sample.ResourceTier], sample.ReserveMS)
	}
	reserves := make([]cicontract.WorkloadResourceTierReserve, 0, int(cicontract.WorkloadResourceTierSlow))
	for tier := cicontract.WorkloadResourceTierFast; tier <= cicontract.WorkloadResourceTierSlow; tier++ {
		values := byTier[tier]
		if len(values) == 0 {
			continue
		}
		digest, err := resourceTierReserveDigest(samples, tier)
		if err != nil {
			return nil, fmt.Errorf("derive resource-tier reserve tier %d provenance: %w", tier, err)
		}
		reserves = append(reserves, cicontract.WorkloadResourceTierReserve{
			SchemaVersion: cicontract.WorkloadResourceTierReserveSchemaVersion, ResourceTier: tier,
			Resolution:  cicontract.WorkloadResourceTierReserveResolutionMeasured,
			Measurement: &cicontract.WorkloadResourceTierReserveMeasurement{P95MS: nearestRankP95(values), SampleCount: len(values), ProvenanceDigest: digest},
		})
	}
	return reserves, nil
}

func resourceTierForReserve(resources RemoteCIShardResources) (cicontract.WorkloadResourceTier, error) {
	return cicontract.NormalResourceTierForIdentity(resources.ClassID, resources.CPU, resources.MemoryGiB)
}

func resourceTierReserveDigest(samples []resourceTierReserveSample, tier cicontract.WorkloadResourceTier) (string, error) {
	selected := make([]resourceTierReserveSample, 0, len(samples))
	for _, sample := range samples {
		if sample.ResourceTier == tier {
			selected = append(selected, sample)
		}
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		return "", fmt.Errorf("marshal tier reserve samples: %w", err)
	}
	digest := sha256.Sum256(append([]byte(cicontract.WorkloadPlanningRiskPolicyID+"\x00"), encoded...))
	return fmt.Sprintf("sha256:%x", digest), nil
}
