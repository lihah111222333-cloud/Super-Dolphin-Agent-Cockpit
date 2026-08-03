package gate

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type workloadTimingWarningSubject struct {
	shardIdentity string
	workloadID    GateID
}

type workloadTimingWarningObservationKey struct {
	workloadTimingWarningSubject
	evidenceKind cicontract.TimingWarningEvidenceKind
}

type workloadTimingWarningPhase struct {
	evidenceKind cicontract.TimingWarningEvidenceKind
	phase        cicontract.TimingPhase
	durationMS   func(ExecutionProfile) int64
}

var workloadTimingWarningPhases = [...]workloadTimingWarningPhase{
	{evidenceKind: cicontract.TimingWarningEvidenceTestBody, phase: cicontract.TimingTestBody, durationMS: func(profile ExecutionProfile) int64 { return profile.TestBodyMS }},
	{evidenceKind: cicontract.TimingWarningEvidenceTotal, phase: cicontract.TimingTotal, durationMS: func(profile ExecutionProfile) int64 { return profile.TotalMS }},
}

// BuildRemoteCIWorkloadTimingWarnings 只从同一 run 的 workload raw observation
// 及其 execution 回执构造完成后 target warning；profile 不能单独充当告警事实。
func BuildRemoteCIWorkloadTimingWarnings(
	jobID string,
	agentTokenDigest string,
	acceptedGeneration uint64,
	executions []PlanGateExecution,
	observations []TimingObservation,
) ([]RemoteCITimingWarning, error) {
	executionIndex, err := indexWorkloadTimingWarningExecutions(executions)
	if err != nil {
		return nil, err
	}
	observationIndex, err := indexWorkloadTimingWarningObservations(jobID, observations, executionIndex)
	if err != nil {
		return nil, err
	}
	warnings := make([]RemoteCITimingWarning, 0, len(executions)*len(workloadTimingWarningPhases))
	for subject, execution := range executionIndex {
		for _, phase := range workloadTimingWarningPhases {
			observation, exists := observationIndex[workloadTimingWarningObservationKey{subject, phase.evidenceKind}]
			if !exists {
				return nil, fmt.Errorf("remote CI workload %q lacks exact %s timing warning evidence", execution.GateID, phase.phase)
			}
			if err := bindWorkloadTimingWarningObservation(execution, observation, phase); err != nil {
				return nil, err
			}
			if observation.DurationMS <= cicontract.ShardTargetDuration.Milliseconds() {
				continue
			}
			warning := workloadTimingWarningFromObservation(jobID, agentTokenDigest, acceptedGeneration, observation, phase.evidenceKind)
			if err := warning.Validate(); err != nil {
				return nil, err
			}
			warnings = append(warnings, warning)
		}
	}
	return canonicalRemoteCITimingWarnings(warnings), nil
}

func indexWorkloadTimingWarningExecutions(executions []PlanGateExecution) (map[workloadTimingWarningSubject]PlanGateExecution, error) {
	indexed := make(map[workloadTimingWarningSubject]PlanGateExecution, len(executions))
	for _, execution := range executions {
		subject := workloadTimingWarningSubject{execution.ShardIdentity, execution.GateID}
		if subject.shardIdentity == "" || subject.workloadID == "" {
			return nil, errors.New("remote CI workload target warning execution identity is incomplete")
		}
		if _, duplicate := indexed[subject]; duplicate {
			return nil, fmt.Errorf("remote CI workload target warning execution %q is duplicated", execution.GateID)
		}
		indexed[subject] = execution
	}
	return indexed, nil
}

// indexWorkloadTimingWarningObservations 索引同一 run 的 workload raw timing warning 观测。
func indexWorkloadTimingWarningObservations(
	jobID string,
	observations []TimingObservation,
	executions map[workloadTimingWarningSubject]PlanGateExecution,
) (map[workloadTimingWarningObservationKey]TimingObservation, error) {
	indexed := make(map[workloadTimingWarningObservationKey]TimingObservation, len(executions)*len(workloadTimingWarningPhases))
	for _, observation := range observations {
		kind, relevant := workloadTimingWarningKind(observation)
		if !relevant {
			continue
		}
		if observation.JobID != jobID {
			return nil, errors.New("remote CI workload target warning observation job binding is invalid")
		}
		subject := workloadTimingWarningSubject{observation.ShardIdentity, observation.WorkloadID}
		if _, exists := executions[subject]; !exists {
			return nil, fmt.Errorf("remote CI workload target warning observation %q has no execution", observation.WorkloadID)
		}
		key := workloadTimingWarningObservationKey{subject, kind}
		if _, duplicate := indexed[key]; duplicate {
			return nil, fmt.Errorf("remote CI workload target warning observation %v is duplicated", key)
		}
		indexed[key] = observation
	}
	return indexed, nil
}

func workloadTimingWarningKind(observation TimingObservation) (cicontract.TimingWarningEvidenceKind, bool) {
	if observation.Scope != cicontract.TimingScopeWorkload {
		return "", false
	}
	switch observation.Phase {
	case cicontract.TimingTestBody:
		return cicontract.TimingWarningEvidenceTestBody, true
	case cicontract.TimingTotal:
		return cicontract.TimingWarningEvidenceTotal, true
	default:
		return "", false
	}
}

func bindWorkloadTimingWarningObservation(
	execution PlanGateExecution,
	observation TimingObservation,
	phase workloadTimingWarningPhase,
) error {
	if err := observation.Validate(); err != nil {
		return fmt.Errorf("remote CI workload %q target warning observation: %w", execution.GateID, err)
	}
	if err := validateWorkloadTimingWarningObservationIdentity(execution, observation, phase); err != nil {
		return err
	}
	if err := validateWorkloadTimingWarningObservationDuration(execution, observation, phase); err != nil {
		return err
	}
	if phase.phase == cicontract.TimingTotal {
		return validateWorkloadTimingWarningTotalInterval(execution, observation)
	}
	return validateWorkloadTimingWarningTestBodyInterval(execution, observation)
}

// validateWorkloadTimingWarningObservationIdentity 校验 workload 观测的作用域和身份绑定。
func validateWorkloadTimingWarningObservationIdentity(
	execution PlanGateExecution,
	observation TimingObservation,
	phase workloadTimingWarningPhase,
) error {
	if observation.Scope != cicontract.TimingScopeWorkload {
		return fmt.Errorf("remote CI workload %q target warning observation identity is invalid", execution.GateID)
	}
	if observation.Phase != phase.phase {
		return fmt.Errorf("remote CI workload %q target warning observation identity is invalid", execution.GateID)
	}
	if observation.ShardIdentity != execution.ShardIdentity {
		return fmt.Errorf("remote CI workload %q target warning observation identity is invalid", execution.GateID)
	}
	if observation.WorkloadID != execution.GateID {
		return fmt.Errorf("remote CI workload %q target warning observation identity is invalid", execution.GateID)
	}
	if observation.Measurement != cicontract.ObservationMeasured {
		return fmt.Errorf("remote CI workload %q target warning observation identity is invalid", execution.GateID)
	}
	if observation.Aggregation != cicontract.TimingAggregationRaw {
		return fmt.Errorf("remote CI workload %q target warning observation identity is invalid", execution.GateID)
	}
	return nil
}

// validateWorkloadTimingWarningObservationDuration 校验观测时长与 execution profile 一致。
func validateWorkloadTimingWarningObservationDuration(
	execution PlanGateExecution,
	observation TimingObservation,
	phase workloadTimingWarningPhase,
) error {
	if observation.DurationMS != phase.durationMS(execution.ExecutionProfile) {
		return fmt.Errorf("remote CI workload %q %s timing observation conflicts with execution profile", execution.GateID, phase.phase)
	}
	return nil
}

// validateWorkloadTimingWarningTotalInterval 校验 total 观测使用 execution 的完整区间。
func validateWorkloadTimingWarningTotalInterval(execution PlanGateExecution, observation TimingObservation) error {
	if !observation.StartedAt.Equal(execution.StartedAt) {
		return fmt.Errorf("remote CI workload %q total timing observation conflicts with execution interval", execution.GateID)
	}
	if !observation.CompletedAt.Equal(execution.CompletedAt) {
		return fmt.Errorf("remote CI workload %q total timing observation conflicts with execution interval", execution.GateID)
	}
	return nil
}

// validateWorkloadTimingWarningTestBodyInterval 校验 test_body 观测使用 execution 的尾部区间。
func validateWorkloadTimingWarningTestBodyInterval(execution PlanGateExecution, observation TimingObservation) error {
	wantStart := execution.CompletedAt.Add(-time.Duration(execution.ExecutionProfile.TestBodyMS) * time.Millisecond)
	if !observation.StartedAt.Equal(wantStart) {
		return fmt.Errorf("remote CI workload %q test_body timing observation conflicts with execution interval", execution.GateID)
	}
	if !observation.CompletedAt.Equal(execution.CompletedAt) {
		return fmt.Errorf("remote CI workload %q test_body timing observation conflicts with execution interval", execution.GateID)
	}
	return nil
}

func workloadTimingWarningFromObservation(
	jobID string,
	agentTokenDigest string,
	acceptedGeneration uint64,
	observation TimingObservation,
	evidenceKind cicontract.TimingWarningEvidenceKind,
) RemoteCITimingWarning {
	warning := RemoteCITimingWarning{
		JobID: jobID, AgentTokenDigest: agentTokenDigest, AcceptedGeneration: acceptedGeneration,
		Scope: cicontract.TimingScopeWorkload, ShardIdentity: observation.ShardIdentity,
		WorkloadID: observation.WorkloadID, EvidenceKind: evidenceKind,
		Action:             cicontract.TimingWarningWarnAndContinue,
		EvidenceStartedAt:  observation.StartedAt.UTC().Truncate(time.Millisecond),
		ObservedAt:         observation.CompletedAt.UTC().Truncate(time.Millisecond),
		EvidenceDurationMS: observation.DurationMS,
		TargetMS:           cicontract.ShardTargetDuration.Milliseconds(),
	}
	warning.WarningText = CanonicalRemoteCITimingWarningText(warning)
	return warning
}
