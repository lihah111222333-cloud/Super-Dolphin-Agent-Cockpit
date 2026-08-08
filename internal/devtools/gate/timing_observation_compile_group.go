package gate

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// validateScopeBinding 校验 timing observation 的主体绑定和 compile-group 元数据。
func (observation TimingObservation) validateScopeBinding() error {
	switch observation.Scope {
	case cicontract.TimingScopeRun:
		return validateRunTimingBinding(observation)
	case cicontract.TimingScopeShard:
		return validateShardTimingBinding(observation)
	case cicontract.TimingScopeWorkload:
		return validateWorkloadTimingBinding(observation)
	case cicontract.TimingScopeCompileGroup:
		return validateCompileGroupTimingBinding(observation)
	default:
		return errors.New("timing observation scope is invalid")
	}
}

func validateRunTimingBinding(observation TimingObservation) error {
	if observation.ShardIdentity != "" || observation.WorkloadID != "" {
		return errors.New("run timing observation has subject binding")
	}
	return nil
}

func validateShardTimingBinding(observation TimingObservation) error {
	if observation.ShardIdentity == "" || observation.WorkloadID != "" {
		return errors.New("shard timing observation binding is invalid")
	}
	return nil
}

func validateWorkloadTimingBinding(observation TimingObservation) error {
	if observation.ShardIdentity == "" || observation.WorkloadID == "" {
		return errors.New("workload timing observation binding is invalid")
	}
	return nil
}

func validateCompileGroupTimingBinding(observation TimingObservation) error {
	if observation.ShardIdentity == "" || observation.WorkloadID != "" || observation.Phase != cicontract.TimingTestBinaryCompile {
		return errors.New("compile group timing observation binding is invalid")
	}
	return observation.validateCompileGroupMetadata()
}

func (observation TimingObservation) validateCompileGroupMetadata() error {
	for _, check := range []func() error{
		observation.validateCompileGroupIdentity,
		observation.validateCompileGroupWorkloads,
		observation.validateCompileGroupEvidence,
		observation.validateCompileGroupOutcome,
		observation.validateCompileGroupResources,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (observation TimingObservation) validateCompileGroupIdentity() error {
	if observation.CompileGroupID == "" || !digestPattern.MatchString(observation.CompileGroupID) ||
		!digestPattern.MatchString(observation.CompileArtifactKey) || !isCanonicalCompileGroupPackageTarget(observation.CompilePackageTarget) ||
		!digestPattern.MatchString(observation.CompileCommandDigest) || !digestPattern.MatchString(observation.CompileProfileDigest) || strings.TrimSpace(observation.CompileResourceClassID) == "" {
		return errors.New("compile group timing identity is invalid")
	}
	return nil
}

func (observation TimingObservation) validateCompileGroupWorkloads() error {
	if len(observation.CompileWorkloadIDs) == 0 {
		return errors.New("compile group timing workload IDs are empty")
	}
	seen := make(map[GateID]struct{}, len(observation.CompileWorkloadIDs))
	for _, workloadID := range observation.CompileWorkloadIDs {
		if workloadID == "" {
			return errors.New("compile group timing workload ID is empty")
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("compile group timing workload ID %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
	}
	return nil
}

func (observation TimingObservation) validateCompileGroupEvidence() error {
	if observation.CompileArtifactSize < 0 || observation.CompileCacheHits > ^uint64(0)-observation.CompileCacheMisses {
		return errors.New("compile group timing artifact or cache counts are invalid")
	}
	if observation.CompileArtifactSHA256 != "" && !digestPattern.MatchString(observation.CompileArtifactSHA256) {
		return errors.New("compile group timing artifact digest is invalid")
	}
	if observation.CompileCacheStatus != string(observation.CacheEvidence.Go.Status) {
		return errors.New("compile group timing cache status does not match cache evidence")
	}
	return nil
}

func (observation TimingObservation) validateCompileGroupOutcome() error {
	if !validPlanGateExit(ResultStatus(observation.CompileStatus), observation.CompileExitCode) {
		return errors.New("compile group timing status or exit code is invalid")
	}
	if ResultStatus(observation.CompileStatus) == ResultStatusPassed {
		if observation.CompileArtifactSHA256 == "" || observation.CompileArtifactSize <= 0 || observation.CompileErrorText != "" {
			return errors.New("passed compile group timing lacks artifact evidence")
		}
		return nil
	}
	if strings.TrimSpace(observation.CompileErrorText) == "" {
		return errors.New("failed compile group timing lacks error text")
	}
	return nil
}

func (observation TimingObservation) validateCompileGroupResources() error {
	if err := validateCompileExecutionMode(observation.CompileExecutionMode); err != nil {
		return err
	}
	if err := validateCompileResourceShape(observation.CompileResourceCPU, observation.CompileResourceMemoryGiB); err != nil {
		return err
	}
	return nil
}

func validateCompileExecutionMode(mode string) error {
	if mode != DurationExecutionModeNormal && mode != DurationExecutionModeCalibration {
		return errors.New("compile group timing execution mode is invalid")
	}
	return nil
}

func validateCompileResourceShape(cpu, memoryGiB float64) error {
	if cpu <= 0 || memoryGiB <= 0 || math.IsNaN(cpu) || math.IsInf(cpu, 0) || math.IsNaN(memoryGiB) || math.IsInf(memoryGiB, 0) {
		return errors.New("compile group timing resource evidence is invalid")
	}
	return nil
}
