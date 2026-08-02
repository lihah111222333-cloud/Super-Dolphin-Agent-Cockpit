package gate

import (
	"errors"
	"fmt"
	"strings"
)

func validateRemoteCIRunRecord(record RemoteCIRunRecord) error {
	if err := validateRemoteCIRunIdentity(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunShards(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunWorkloadExecutions(record.WorkloadExecutions); err != nil {
		return err
	}
	if record.Authoritative {
		if len(record.TimingObservations) == 0 {
			return errors.New("authoritative remote CI run requires complete timing observations")
		}
		if err := ValidateAuthoritativeTimingObservations(record.JobID, record.TimingObservations, record.WorkloadExecutions, record.Shards); err != nil {
			return fmt.Errorf("remote CI authoritative timing observations: %w", err)
		}
	} else {
		for _, observation := range record.TimingObservations {
			if err := observation.Validate(); err != nil {
				return fmt.Errorf("remote CI timing observation: %w", err)
			}
			if observation.JobID != record.JobID {
				return errors.New("remote CI timing observation job binding is invalid")
			}
		}
	}
	if err := validateRemoteCIRunWarnings(record); err != nil {
		return err
	}
	return nil
}

func validateRemoteCIRunIdentity(record RemoteCIRunRecord) error {
	for _, validate := range []func(RemoteCIRunRecord) error{
		validateRemoteCIRunRequiredFields,
		validateRemoteCIRunEntrypoint,
		validateRemoteCIRunTiming,
		validateRemoteCIRunRequester,
		validateRemoteCIRunCatalogDigest,
		validateRemoteCIRunStatus,
	} {
		if err := validate(record); err != nil {
			return err
		}
	}
	return nil
}

func validateRemoteCIRunRequiredFields(record RemoteCIRunRecord) error {
	if record.AcceptedGeneration == 0 {
		return errors.New("remote CI run accepted baseline generation is required")
	}
	for field, value := range map[string]string{
		"job ID":                          record.JobID,
		"entrypoint":                      string(record.Entrypoint),
		"profile":                         string(record.Profile),
		"plan digest":                     record.PlanDigest,
		"catalog digest":                  record.CatalogDigest,
		"source tree":                     record.SourceTreeSHA,
		"candidate gate source digest":    record.CandidateGateSourceSHA256,
		"candidate gate toolchain digest": record.CandidateGateToolchainSHA256,
		"runner image":                    record.RunnerImage,
		"status":                          string(record.Status),
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("remote CI run %s is required", field)
		}
	}
	if !isPrefixedSHA256Digest(record.CandidateGateSourceSHA256) || !isPrefixedSHA256Digest(record.CandidateGateToolchainSHA256) {
		return errors.New("remote CI run candidate gate compile identity is invalid")
	}
	return nil
}

func validateRemoteCIRunEntrypoint(record RemoteCIRunRecord) error {
	entrypoint, ok := canonicalCIEntrypoint(record.Entrypoint)
	if !ok {
		return fmt.Errorf("remote CI run entrypoint %q is not canonical", record.Entrypoint)
	}
	if record.Authoritative != entrypoint.Authoritative {
		return fmt.Errorf(
			"remote CI run authority %t does not match entrypoint %q authority %t",
			record.Authoritative, record.Entrypoint, entrypoint.Authoritative,
		)
	}
	return nil
}

func validateRemoteCIRunTiming(record RemoteCIRunRecord) error {
	if record.StartedAt.IsZero() || record.CompletedAt.IsZero() {
		return errors.New("remote CI run timestamps are required")
	}
	if record.CompletedAt.Before(record.StartedAt) {
		return errors.New("remote CI run completion precedes start")
	}
	return nil
}

func validateRemoteCIRunRequester(record RemoteCIRunRecord) error {
	if record.RequesterFingerprint == "" {
		return nil
	}
	if err := record.RequesterFingerprint.Validate(); err != nil {
		return fmt.Errorf("remote CI run requester fingerprint: %w", err)
	}
	return nil
}

func validateRemoteCIRunCatalogDigest(record RemoteCIRunRecord) error {
	if !isPrefixedSHA256Digest(record.CatalogDigest) {
		return errors.New("remote CI run catalog digest is invalid")
	}
	return nil
}

func validateRemoteCIRunStatus(record RemoteCIRunRecord) error {
	switch record.Status {
	case ResultStatusPassed, ResultStatusFailed, ResultStatusCancelled, ResultStatusTimeout,
		ResultStatusInfraFailed, ResultStatusPassedStalePolicy:
	default:
		return fmt.Errorf("remote CI run status %q is not supported", record.Status)
	}
	return nil
}

func validateRemoteCIRunWarnings(record RemoteCIRunRecord) error {
	for _, warning := range record.Warnings {
		if strings.TrimSpace(warning) == "" {
			return errors.New("remote CI run warning is empty")
		}
	}
	return nil
}

func validateRemoteCIRunWorkloadExecutions(executions []PlanGateExecution) error {
	seen := make(map[GateID]struct{}, len(executions))
	for _, execution := range executions {
		if strings.TrimSpace(string(execution.GateID)) == "" {
			return errors.New("remote CI workload execution ID is required")
		}
		if _, duplicate := seen[execution.GateID]; duplicate {
			return fmt.Errorf("remote CI workload execution %q is duplicated", execution.GateID)
		}
		seen[execution.GateID] = struct{}{}
		if execution.StartedAt.IsZero() || execution.CompletedAt.Before(execution.StartedAt) {
			return fmt.Errorf("remote CI workload execution %q timing is invalid", execution.GateID)
		}
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return fmt.Errorf("remote CI workload execution %q profile: %w", execution.GateID, err)
		}
	}
	return nil
}

func validateRemoteCIRunShards(record RemoteCIRunRecord) error {
	seenWorkloads := make(map[GateID]string)
	for _, shard := range record.Shards {
		if strings.TrimSpace(shard.ShardIdentity) == "" || strings.TrimSpace(shard.ContainerStatus) == "" {
			return errors.New("remote CI shard identity and status are required")
		}
		if err := validateRemoteCIShardMaterializationTiming(shard); err != nil {
			return errors.New("remote CI shard materialization timing is invalid")
		}
		shardWorkloads := make(map[GateID]struct{}, len(shard.Workloads))
		for _, workloadID := range shard.Workloads {
			if strings.TrimSpace(string(workloadID)) == "" {
				return errors.New("remote CI shard workload ID is required")
			}
			if _, duplicate := shardWorkloads[workloadID]; duplicate {
				return fmt.Errorf("remote CI shard workload %q is duplicated", workloadID)
			}
			if previousShard, duplicate := seenWorkloads[workloadID]; duplicate {
				return fmt.Errorf(
					"remote CI shard workload %q is duplicated across shards %q and %q",
					workloadID, previousShard, shard.ShardIdentity,
				)
			}
			shardWorkloads[workloadID] = struct{}{}
			seenWorkloads[workloadID] = shard.ShardIdentity
		}
	}
	return nil
}

func validateRemoteCIShardMaterializationTiming(shard RemoteCIShardRecord) error {
	timing := shard.MaterializationTiming
	if err := timing.Validate(); err != nil {
		return err
	}
	if shard.ContainerGroup == "" {
		if shard.ContainerStatus != "Unknown" || timing.Measurement != MaterializationMeasurementNotMeasured {
			return errors.New("uncreated remote CI shard timing is invalid")
		}
		return nil
	}
	if timing.Measurement == MaterializationMeasurementMeasured && timing.ShardIdentity != shard.ShardIdentity {
		return errors.New("measured remote CI shard timing identity does not match shard")
	}
	if remoteCIShardTerminalStatus(shard.ContainerStatus) && timing.Measurement != MaterializationMeasurementMeasured {
		return errors.New("terminal remote CI shard materialization timing evidence is required")
	}
	if remoteCIShardTerminalStatus(shard.ContainerStatus) && timing.CandidateCompile.MaterializeMS <= 0 {
		return errors.New("terminal remote CI shard candidate compile timing evidence is required")
	}
	return nil
}

func remoteCIShardTerminalStatus(status string) bool {
	switch status {
	case "Succeeded", "Failed", "ScheduleFailed", "Expired":
		return true
	default:
		return false
	}
}
