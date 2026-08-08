package gate

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// validateAuthoritativeTimingObservation 校验单行 authority 观测及其 scope 绑定。
func validateAuthoritativeTimingObservation(jobID string, observation TimingObservation, expected map[observationKey]struct{}, shardSet map[string]struct{}, shardByWorkload map[GateID]string) (observationKey, error) {
	if observation.JobID != jobID {
		return observationKey{}, errors.New("authoritative timing observation job binding is invalid")
	}
	if err := observation.Validate(); err != nil {
		return observationKey{}, err
	}
	key := observationKey{scope: observation.Scope, shard: observation.ShardIdentity, workload: observation.WorkloadID, phase: observation.Phase, compileGroup: observation.CompileGroupID, compileArtifact: observation.CompileArtifactKey}
	if observation.Scope == cicontract.TimingScopeCompileGroup {
		if err := validateAuthoritativeCompileGroupObservation(observation, shardSet, shardByWorkload); err != nil {
			return observationKey{}, err
		}
	} else if _, exists := expected[key]; !exists {
		return observationKey{}, fmt.Errorf("authoritative timing has extra scope=%q shard=%q workload=%q phase=%q", observation.Scope, observation.ShardIdentity, observation.WorkloadID, observation.Phase)
	}
	return key, nil
}

func validateAuthoritativeCompileGroupObservation(observation TimingObservation, shardSet map[string]struct{}, shardByWorkload map[GateID]string) error {
	if _, exists := shardSet[observation.ShardIdentity]; !exists {
		return fmt.Errorf("authoritative compile group timing shard %q is not declared", observation.ShardIdentity)
	}
	for _, workloadID := range observation.CompileWorkloadIDs {
		if expectedShard, exists := shardByWorkload[workloadID]; !exists || expectedShard != observation.ShardIdentity {
			return fmt.Errorf("authoritative compile group %q workload %q shard binding is invalid", observation.CompileGroupID, workloadID)
		}
	}
	if observation.Aggregation != cicontract.TimingAggregationRaw || observation.Measurement != cicontract.ObservationMeasured {
		return fmt.Errorf("authoritative compile group %q timing must be a measured raw interval", observation.CompileGroupID)
	}
	return nil
}
