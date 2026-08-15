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

func planningContextsEqual(left, right PlanningContext) bool {
	return reflect.DeepEqual(left, right)
}

// EffectiveTargetDurationForResourceTier returns the 100-second budget after
// deducting that tier's authoritative external/orchestration reserve.
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
	if reserve.P95MS >= context.TargetDurationMS {
		return 0, fmt.Errorf("resource-tier %d reserve leaves no positive planning budget", tier)
	}
	return context.TargetDurationMS - reserve.P95MS, nil
}

func resourceTierReserve(reserves []cicontract.WorkloadResourceTierReserve, tier cicontract.WorkloadResourceTier) (cicontract.WorkloadResourceTierReserve, error) {
	if tier < cicontract.WorkloadResourceTierFast || tier > cicontract.WorkloadResourceTierSlow {
		return cicontract.WorkloadResourceTierReserve{}, fmt.Errorf("resource tier %d is invalid", tier)
	}
	reserve := reserves[int(tier)-1]
	if reserve.ResourceTier != tier {
		return cicontract.WorkloadResourceTierReserve{}, fmt.Errorf("resource-tier reserve for tier %d is missing", tier)
	}
	return reserve, nil
}

// DeriveNormalResourceTierReserves derives tier-isolated P95 reserves from
// existing authoritative timing observations. Workload totals and test-binary
// compile intervals are excluded exactly once; all remaining shard time is the
// measured eci/source/candidate/orchestration union that planning must reserve.
func DeriveNormalResourceTierReserves(observations []TimingObservation, shards []RemoteCIShardRecord, calibrationClassID string) ([]cicontract.WorkloadResourceTierReserve, error) {
	if strings.TrimSpace(calibrationClassID) == "" || strings.TrimSpace(calibrationClassID) != calibrationClassID {
		return nil, errors.New("normal resource-tier reserve requires canonical calibration class identity")
	}
	groups, err := collectShardOverheadObservationGroups(observations)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, errors.New("no measured normal shard timing samples for resource-tier reserves")
	}
	shardTiers, err := normalReserveShardTiers(shards, calibrationClassID)
	if err != nil {
		return nil, err
	}
	samples, err := normalReserveSamples(groups, shardTiers)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, errors.New("normal resource-tier reserves have no eligible samples")
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

func normalReserveShardTiers(shards []RemoteCIShardRecord, calibrationClassID string) (map[string]cicontract.WorkloadResourceTier, error) {
	shardTiers := make(map[string]cicontract.WorkloadResourceTier, len(shards))
	for _, shard := range shards {
		if err := shard.Resources.Validate(); err != nil {
			return nil, fmt.Errorf("validate reserve shard %q resources: %w", shard.ShardIdentity, err)
		}
		if shard.Resources.ClassID == calibrationClassID {
			continue
		}
		tier, err := resourceTierForReserve(shard.Resources)
		if err != nil {
			return nil, fmt.Errorf("classify reserve shard %q: %w", shard.ShardIdentity, err)
		}
		if prior, duplicate := shardTiers[shard.ShardIdentity]; duplicate && prior != tier {
			return nil, fmt.Errorf("reserve shard %q has conflicting resource tiers", shard.ShardIdentity)
		}
		shardTiers[shard.ShardIdentity] = tier
	}
	return shardTiers, nil
}

func normalReserveSamples(groups map[shardOverheadObservationKey]*shardOverheadObservationGroup, shardTiers map[string]cicontract.WorkloadResourceTier) ([]resourceTierReserveSample, error) {
	samples := make([]resourceTierReserveSample, 0, len(groups))
	for key, group := range groups {
		tier, ok := shardTiers[key.shard]
		if !ok {
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
	excluded := make([]TimingObservation, 0, len(group.workloads)+len(group.compileGroups))
	for _, observation := range group.workloads {
		excluded = append(excluded, observation)
	}
	for _, observation := range group.accounted {
		if observation.Scope == cicontract.TimingScopeCompileGroup {
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
			return nil, fmt.Errorf("normal resource-tier reserve tier %d has no reliable samples", tier)
		}
		digest, err := resourceTierReserveDigest(samples, tier)
		if err != nil {
			return nil, fmt.Errorf("derive resource-tier reserve tier %d provenance: %w", tier, err)
		}
		reserves = append(reserves, cicontract.WorkloadResourceTierReserve{SchemaVersion: 1, ResourceTier: tier, P95MS: nearestRankP95(values), SampleCount: len(values), ProvenanceDigest: digest})
	}
	return reserves, nil
}

func resourceTierForReserve(resources RemoteCIShardResources) (cicontract.WorkloadResourceTier, error) {
	switch {
	case resources.ClassID == "small" && resources.CPU == 2 && resources.MemoryGiB == 4:
		return cicontract.WorkloadResourceTierFast, nil
	case resources.ClassID == "medium" && resources.CPU == 4 && resources.MemoryGiB == 8:
		return cicontract.WorkloadResourceTierMedium, nil
	case resources.ClassID == "maximum" && resources.CPU == 8 && resources.MemoryGiB == 16:
		return cicontract.WorkloadResourceTierSlow, nil
	default:
		return 0, fmt.Errorf("resource class %q does not match a canonical normal tier", resources.ClassID)
	}
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
