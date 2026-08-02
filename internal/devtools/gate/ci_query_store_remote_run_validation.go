package gate

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

func validateRemoteCIRunRecord(record RemoteCIRunRecord) error {
	if err := validateRemoteCIRunIdentity(record); err != nil {
		return err
	}
	workloads, err := validateRemoteCIRunWorkloads(record)
	if err != nil {
		return err
	}
	shardWorkloads, err := validateRemoteCIRunShards(record)
	if err != nil {
		return err
	}
	if err := validateRemoteCIPhaseTimings(record.PhaseTimings); err != nil {
		return err
	}
	if err := validateRemoteCIRunWorkloadExecutions(record.WorkloadExecutions); err != nil {
		return err
	}
	hasPreBindingBuild := false
	for _, build := range record.CandidateTestBinaryBuilds {
		if build.CandidateTree == "" {
			// Pre-binding ledger rows are audit-only and cannot satisfy a
			// checkpoint reuse comparison because they lack artifact identity.
			hasPreBindingBuild = true
			continue
		}
		if err := validateCandidateTestBinaryBuildRecord(build); err != nil {
			return err
		}
	}
	if record.Authoritative && !isPrefixedSHA256Digest(record.CandidateTestBinaryReceiptBindingDigest) {
		return errors.New("candidate test binary receipt binding digest is invalid")
	}
	if record.Authoritative && len(record.CandidateTestBinaryBuilds) != 0 {
		if hasPreBindingBuild {
			return errors.New("authoritative candidate test binary builds contain pre-binding audit rows")
		}
		digest, err := CandidateTestBinaryReceiptBindingDigest(record.CandidateTestBinaryBuilds, record.SourceTreeSHA)
		if err != nil || digest != record.CandidateTestBinaryReceiptBindingDigest {
			return errors.New("candidate test binary receipt binding does not match persisted builds")
		}
	}
	return validatePassedRemoteCIRunWorkloads(record.Status, workloads, shardWorkloads)
}

func validateCandidateTestBinaryBuildRecord(build CandidateTestBinaryBuildRecord) error {
	if strings.TrimSpace(build.CandidateTree) == "" || strings.TrimSpace(build.Package) == "" || build.Mode != "test" || build.Platform != "linux/amd64" || build.GoToolchain != RequiredGoToolchain || !build.CGOEnabled || !isPrefixedSHA256Digest(build.ToolchainSHA256) || !isPrefixedSHA256Digest(build.CompileClosureSHA256) || len(build.ManifestSHA256) != 64 || !isPrefixedSHA256Digest(build.ArtifactSHA256) || build.BinarySize <= 0 {
		return errors.New("candidate test binary build identity is invalid")
	}
	if !isPrefixedSHA256Digest(build.GOCachePrivateRootIdentity) {
		return errors.New("candidate test binary private cache identity is invalid")
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
	for field, value := range map[string]string{
		"job ID":         record.JobID,
		"entrypoint":     string(record.Entrypoint),
		"profile":        string(record.Profile),
		"plan digest":    record.PlanDigest,
		"catalog digest": record.CatalogDigest,
		"source tree":    record.SourceTreeSHA,
		"runner image":   record.RunnerImage,
		"status":         string(record.Status),
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("remote CI run %s is required", field)
		}
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
	if record.Status == ResultStatusPassed && !isCanonicalBareSHA256(record.CandidateCLIManifestSHA256) {
		return errors.New("passed remote CI run candidate CLI manifest SHA-256 is required and canonical")
	}
	return nil
}

func isCanonicalBareSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateRemoteCIRunWorkloads(record RemoteCIRunRecord) (map[GateID]string, error) {
	seenWorkloads := make(map[GateID]string)
	for disposition, workloads := range map[string][]GateID{
		"reused": record.ReusedWorkloads, "cache_miss": record.CacheMisses,
	} {
		for _, workloadID := range workloads {
			if strings.TrimSpace(string(workloadID)) == "" {
				return nil, errors.New("remote CI run workload ID is required")
			}
			if previous, duplicate := seenWorkloads[workloadID]; duplicate {
				return nil, fmt.Errorf(
					"remote CI run workload %q is both %s and %s", workloadID, previous, disposition,
				)
			}
			seenWorkloads[workloadID] = disposition
		}
	}
	for _, warning := range record.Warnings {
		if strings.TrimSpace(warning) == "" {
			return nil, errors.New("remote CI run warning is empty")
		}
	}
	return seenWorkloads, nil
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

func validateRemoteCIRunShards(record RemoteCIRunRecord) (map[GateID]string, error) {
	seenWorkloads := make(map[GateID]string)
	for _, shard := range record.Shards {
		if strings.TrimSpace(shard.ShardIdentity) == "" || strings.TrimSpace(shard.ContainerStatus) == "" {
			return nil, errors.New("remote CI shard identity and status are required")
		}
		if err := validateRemoteCIShardMaterializationTiming(shard); err != nil {
			return nil, errors.New("remote CI shard materialization timing is invalid")
		}
		shardWorkloads := make(map[GateID]struct{}, len(shard.Workloads))
		for _, workloadID := range shard.Workloads {
			if strings.TrimSpace(string(workloadID)) == "" {
				return nil, errors.New("remote CI shard workload ID is required")
			}
			if _, duplicate := shardWorkloads[workloadID]; duplicate {
				return nil, fmt.Errorf("remote CI shard workload %q is duplicated", workloadID)
			}
			if previousShard, duplicate := seenWorkloads[workloadID]; duplicate {
				return nil, fmt.Errorf(
					"remote CI shard workload %q is duplicated across shards %q and %q",
					workloadID, previousShard, shard.ShardIdentity,
				)
			}
			shardWorkloads[workloadID] = struct{}{}
			seenWorkloads[workloadID] = shard.ShardIdentity
		}
	}
	return seenWorkloads, nil
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

func validatePassedRemoteCIRunWorkloads(
	status ResultStatus,
	workloads map[GateID]string,
	shardWorkloads map[GateID]string,
) error {
	if status != ResultStatusPassed {
		return nil
	}
	for workloadID, disposition := range workloads {
		_, executed := shardWorkloads[workloadID]
		if disposition == "cache_miss" && !executed {
			return fmt.Errorf("passed remote CI cache miss %q is absent from all shards", workloadID)
		}
		if disposition == "reused" && executed {
			return fmt.Errorf("passed remote CI reused workload %q was executed in a shard", workloadID)
		}
	}
	for workloadID := range shardWorkloads {
		if workloads[workloadID] != "cache_miss" {
			return fmt.Errorf("passed remote CI shard workload %q is not a declared cache miss", workloadID)
		}
	}
	return nil
}
