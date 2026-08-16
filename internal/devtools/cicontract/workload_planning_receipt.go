package cicontract

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Workload planning receipt schema and domain identities are frozen protocol inputs.
const (
	WorkloadPlanningReceiptSchemaVersion         uint32 = 1
	WorkloadPlanningReceiptSchema                       = "workload-planning-receipt/v1"
	WorkloadPlanningReceiptDomain                       = "remote-ci-workload-planning-receipt/v1"
	WorkloadPlanningMappingDomain                       = "remote-ci-workload-planning-map/v1"
	WorkloadPlanningReceiptNotApplicableEvidence        = "not_applicable"
)

// WorkloadPlanningReceiptStatus selects the strict top-level receipt union.
type WorkloadPlanningReceiptStatus string

const (
	WorkloadPlanningReceiptStatusPlanned       WorkloadPlanningReceiptStatus = "planned"
	WorkloadPlanningReceiptStatusNotApplicable WorkloadPlanningReceiptStatus = "not_applicable"
)

// WorkloadPlanningReceiptReason explains why a not_applicable receipt exists.
type WorkloadPlanningReceiptReason string

const (
	WorkloadPlanningReceiptReasonPrePlanningFailure WorkloadPlanningReceiptReason = "pre_planning_failure"
	WorkloadPlanningReceiptReasonAllHit             WorkloadPlanningReceiptReason = "all_hit"
)

// WorkloadPlanningReceiptTierResolution selects measured, bootstrap, or canonical NA.
type WorkloadPlanningReceiptTierResolution string

const (
	WorkloadPlanningReceiptTierResolutionMeasured      WorkloadPlanningReceiptTierResolution = "measured"
	WorkloadPlanningReceiptTierResolutionBootstrap     WorkloadPlanningReceiptTierResolution = "bootstrap"
	WorkloadPlanningReceiptTierResolutionNotApplicable WorkloadPlanningReceiptTierResolution = "not_applicable"
)

// ValidateWorkloadPlanningReceiptStatus rejects unknown top-level receipt statuses.
func ValidateWorkloadPlanningReceiptStatus(status WorkloadPlanningReceiptStatus) error {
	switch status {
	case WorkloadPlanningReceiptStatusPlanned, WorkloadPlanningReceiptStatusNotApplicable:
		return nil
	default:
		return fmt.Errorf("workload planning receipt status %q is not accepted", status)
	}
}

// ValidateWorkloadPlanningReceiptReason rejects unknown NA reasons.
func ValidateWorkloadPlanningReceiptReason(reason WorkloadPlanningReceiptReason) error {
	switch reason {
	case WorkloadPlanningReceiptReasonPrePlanningFailure, WorkloadPlanningReceiptReasonAllHit:
		return nil
	default:
		return fmt.Errorf("workload planning receipt reason %q is not accepted", reason)
	}
}

// ValidateWorkloadPlanningReceiptTierResolution rejects unknown tier resolutions.
func ValidateWorkloadPlanningReceiptTierResolution(resolution WorkloadPlanningReceiptTierResolution) error {
	switch resolution {
	case WorkloadPlanningReceiptTierResolutionMeasured, WorkloadPlanningReceiptTierResolutionBootstrap, WorkloadPlanningReceiptTierResolutionNotApplicable:
		return nil
	default:
		return fmt.Errorf("workload planning receipt tier resolution %q is not accepted", resolution)
	}
}

// ValidateWorkloadPlanningReceiptSchema enforces the current receipt schema version.
func ValidateWorkloadPlanningReceiptSchema(version uint32) error {
	if version != WorkloadPlanningReceiptSchemaVersion {
		return fmt.Errorf("workload planning receipt schema version %d is not accepted", version)
	}
	return nil
}

// ValidateWorkloadPlanningReceiptDigest enforces sha256:lowercase-hex encoding.
func ValidateWorkloadPlanningReceiptDigest(digest string) error {
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		return errors.New("workload planning receipt digest is not canonical sha256")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:")); err != nil || strings.ToLower(digest) != digest {
		return errors.New("workload planning receipt digest is not canonical sha256")
	}
	return nil
}

// ValidateWorkloadPlanningReceiptContract checks frozen receipt protocol identities.
func ValidateWorkloadPlanningReceiptContract() error {
	if err := ValidateWorkloadPlanningReceiptSchema(WorkloadPlanningReceiptSchemaVersion); err != nil {
		return err
	}
	if WorkloadPlanningReceiptSchema != "workload-planning-receipt/v1" || WorkloadPlanningReceiptDomain != "remote-ci-workload-planning-receipt/v1" || WorkloadPlanningMappingDomain != "remote-ci-workload-planning-map/v1" {
		return errors.New("workload planning receipt domain contract drifted")
	}
	return nil
}
