package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// validateRemoteRunContract 将本次实际 worker 回执绑定到同一 RunInput 产生的计划和目录。
func validateRemoteRunContract(
	input remoteci.RunInput,
	acceptedGeneration uint64,
	result remoteci.RunResult,
) ([]cicontract.CheckObservation, []gatecontract.CheckReceiptRecord, error) {
	if acceptedGeneration == 0 || input.AcceptedGeneration != acceptedGeneration || result.AcceptedGeneration != acceptedGeneration {
		return nil, nil, errors.New("remote CI run accepted generation is not bound to its input")
	}
	plan, catalog, err := remoteRunContractPlanAndCatalog(input)
	if err != nil {
		return nil, nil, err
	}
	observations, err := remoteRunCheckObservations(plan, catalog, input.ImageCacheSnapshotID, result)
	if err != nil {
		return nil, nil, err
	}
	if !remoteRunIsFullAuthoritativeAcceptance(plan, catalog, result) {
		return nil, nil, errors.New("remote CI run is not a full authoritative acceptance")
	}
	if input.LedgerStore == nil {
		return nil, nil, errors.New("remote CI timing authority store is required")
	}
	recorded, err := input.LedgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		return nil, nil, fmt.Errorf("read back remote CI timing authority: %w", err)
	}
	if err := gatecontract.ValidateAuthoritativeTimingObservations(recorded.JobID, recorded.TimingObservations, recorded.WorkloadExecutions, recorded.Shards); err != nil {
		return nil, nil, fmt.Errorf("authoritative timing observations: %w", err)
	}
	if recorded.AcceptedGeneration != acceptedGeneration {
		return nil, nil, errors.New("recorded remote CI run accepted generation does not match this invocation")
	}
	receipts, err := remoteRunCheckReceipts(result, acceptedGeneration, observations)
	if err != nil {
		return nil, nil, err
	}
	return observations, receipts, nil
}

// remoteRunContractPlanAndCatalog rebuilds the only plan/catalog the coordinator may execute.
func remoteRunContractPlanAndCatalog(input remoteci.RunInput) (gatecontract.GatePlan, gatecontract.WorkloadCatalog, error) {
	plan, err := gatecontract.BuildGatePlan(input.Profile, input.Source)
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, fmt.Errorf("build remote CI gate plan: %w", err)
	}
	policy := gatecontract.DefaultWorkloadBootstrapPolicy()
	var catalog gatecontract.WorkloadCatalog
	switch {
	case input.Calibration:
		catalog, err = gatecontract.BuildCalibrationWorkloadCatalog(plan, policy, input.Inventory)
	case input.SelectedTests:
		catalog, err = gatecontract.BuildSelectedTestWorkloadCatalog(plan, input.Inventory)
	default:
		catalog, err = gatecontract.BuildExpandedWorkloadCatalog(plan, policy, input.Inventory)
	}
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, fmt.Errorf("build remote CI workload catalog: %w", err)
	}
	return plan, catalog, nil
}

// remoteRunCheckObservations returns checks derived only from planned workloads and executed worker receipts.
func remoteRunCheckObservations(
	plan gatecontract.GatePlan,
	catalog gatecontract.WorkloadCatalog,
	acceptedSnapshotID string,
	result remoteci.RunResult,
) ([]cicontract.CheckObservation, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("validate remote CI gate plan: %w", err)
	}
	if err := gatecontract.ValidateWorkloadCatalog(catalog); err != nil {
		return nil, fmt.Errorf("validate remote CI workload catalog: %w", err)
	}
	if result.Profile != plan.Profile || result.PlanDigest != plan.PlanDigest {
		return nil, errors.New("remote CI result does not bind the executed gate plan")
	}
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		return nil, fmt.Errorf("digest remote CI workload catalog: %w", err)
	}
	if result.CatalogDigest != catalogDigest {
		return nil, errors.New("remote CI result does not bind the executed workload catalog")
	}
	if result.SourceTreeSHA != plan.Source.SourceTreeSHA {
		return nil, errors.New("remote CI result source tree does not match the executed gate plan")
	}
	if result.Status != gatecontract.ResultStatusPassed {
		return nil, fmt.Errorf("remote CI run status %q cannot accept executed checks", result.Status)
	}
	if acceptedSnapshotID == "" {
		return nil, errors.New("remote CI accepted snapshot is missing")
	}

	expected := make(map[gatecontract.GateID]int, len(catalog.Workloads))
	checks := make(map[cicontract.RequiredCheck]cicontract.CheckObservation, len(cicontract.RequiredChecks()))
	for _, workload := range catalog.Workloads {
		gateID, _, _, _, err := gatecontract.ParseWorkloadID(workload.ID)
		if err != nil {
			return nil, fmt.Errorf("parse planned remote CI workload %q: %w", workload.ID, err)
		}
		check, err := remoteRunRequiredCheck(gateID)
		if err != nil {
			return nil, err
		}
		expected[gatecontract.GateID(workload.ID)]++
		checks[check] = cicontract.CheckObservation{
			Check:              check,
			SourceTree:         result.SourceTreeSHA,
			AcceptedSnapshotID: acceptedSnapshotID,
			PlanDigest:         result.PlanDigest,
		}
	}
	for _, execution := range result.WorkloadExecutions {
		if expected[execution.GateID] == 0 {
			return nil, fmt.Errorf("remote CI observed unplanned or duplicate workload %q", execution.GateID)
		}
		if execution.Status != gatecontract.ResultStatusPassed || execution.ExitCode != 0 {
			return nil, fmt.Errorf("remote CI workload %q did not execute and pass", execution.GateID)
		}
		if execution.StartedAt.IsZero() || execution.CompletedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) {
			return nil, fmt.Errorf("remote CI workload %q has no positive actual execution time", execution.GateID)
		}
		startedAt := execution.StartedAt.UTC().Truncate(time.Millisecond)
		completedAt := execution.CompletedAt.UTC().Truncate(time.Millisecond)
		if !completedAt.After(startedAt) {
			return nil, fmt.Errorf("remote CI workload %q has no positive millisecond execution time", execution.GateID)
		}
		gateID, _, _, _, err := gatecontract.ParseWorkloadID(string(execution.GateID))
		if err != nil {
			return nil, fmt.Errorf("parse executed remote CI workload %q: %w", execution.GateID, err)
		}
		check, err := remoteRunRequiredCheck(gateID)
		if err != nil {
			return nil, err
		}
		observation := checks[check]
		if observation.StartedAtUnixMS == 0 || startedAt.UnixMilli() < observation.StartedAtUnixMS {
			observation.StartedAtUnixMS = startedAt.UnixMilli()
		}
		if observation.CompletedAtUnixMS == 0 || completedAt.UnixMilli() > observation.CompletedAtUnixMS {
			observation.CompletedAtUnixMS = completedAt.UnixMilli()
		}
		observation.Executed = true
		observation.Passed = true
		checks[check] = observation
		expected[execution.GateID]--
	}
	for workloadID, remaining := range expected {
		if remaining != 0 {
			return nil, fmt.Errorf("remote CI planned workload %q has no executed PASS receipt", workloadID)
		}
	}

	observations := make([]cicontract.CheckObservation, 0, len(checks))
	for _, check := range cicontract.RequiredChecks() {
		observation, found := checks[check]
		if !found || !observation.Executed || !observation.Passed || observation.StartedAtUnixMS <= 0 || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS {
			return nil, fmt.Errorf("remote CI required check %q has no actual execution timing", check)
		}
		observation.DurationMS = observation.CompletedAtUnixMS - observation.StartedAtUnixMS
		digest, err := cicontract.CheckObservationReceiptDigest(observation)
		if err != nil {
			return nil, fmt.Errorf("hash remote CI required check %q observation: %w", check, err)
		}
		observation.ReceiptSHA256 = digest
		observations = append(observations, observation)
	}
	return observations, nil
}

func remoteRunCheckReceipts(
	result remoteci.RunResult,
	acceptedGeneration uint64,
	observations []cicontract.CheckObservation,
) ([]gatecontract.CheckReceiptRecord, error) {
	if result.JobID == "" || result.SourceTreeSHA == "" {
		return nil, errors.New("remote CI check receipt identity is missing")
	}
	if acceptedGeneration == 0 {
		return nil, errors.New("remote CI accepted generation is missing")
	}
	receipts := make([]gatecontract.CheckReceiptRecord, 0, len(observations))
	for _, observation := range observations {
		receipt := gatecontract.CheckReceiptRecord{
			RunID: result.JobID, JobID: result.JobID, CandidateTreeSHA: result.SourceTreeSHA,
			AcceptedGeneration: acceptedGeneration, AcceptedSnapshotID: observation.AcceptedSnapshotID,
			RequiredCheck: observation.Check, Executed: observation.Executed, Passed: observation.Passed,
			StartedAt: time.UnixMilli(observation.StartedAtUnixMS).UTC(), CompletedAt: time.UnixMilli(observation.CompletedAtUnixMS).UTC(),
			Duration: time.Duration(observation.DurationMS) * time.Millisecond,
		}
		if receipt.AcceptedSnapshotID == "" {
			return nil, errors.New("remote CI accepted snapshot is missing")
		}
		sha256, err := gatecontract.CheckReceiptSHA256(receipt)
		if err != nil {
			return nil, fmt.Errorf("hash remote CI check receipt %q: %w", observation.Check, err)
		}
		receipt.ReceiptSHA256 = sha256
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

// validateRemoteRunStoredCheckReceipts proves the same SQLite authority holds
// exactly the six records minted from this invocation's worker observations.
func validateRemoteRunStoredCheckReceipts(
	store *gatecontract.DurationLedgerStore,
	jobID string,
	want []gatecontract.CheckReceiptRecord,
) error {
	if store == nil {
		return errors.New("remote CI duration ledger store is required")
	}
	got, err := store.LoadCheckReceipts(jobID)
	if err != nil {
		return fmt.Errorf("reload remote CI check receipts: %w", err)
	}
	if len(got) != len(want) {
		return fmt.Errorf("reloaded remote CI check receipt count = %d, want %d", len(got), len(want))
	}
	wantByCheck := make(map[cicontract.RequiredCheck]gatecontract.CheckReceiptRecord, len(want))
	for _, receipt := range want {
		if _, duplicate := wantByCheck[receipt.RequiredCheck]; duplicate {
			return fmt.Errorf("expected remote CI check receipt %q is duplicated", receipt.RequiredCheck)
		}
		wantByCheck[receipt.RequiredCheck] = receipt
	}
	for _, receipt := range got {
		expected, found := wantByCheck[receipt.RequiredCheck]
		if !found || receipt != expected {
			return fmt.Errorf("reloaded remote CI check receipt %q does not exactly match this invocation", receipt.RequiredCheck)
		}
		delete(wantByCheck, receipt.RequiredCheck)
	}
	if len(wantByCheck) != 0 {
		return errors.New("reloaded remote CI check receipt collection is incomplete")
	}
	return nil
}

// remoteRunIsFullAuthoritativeAcceptance identifies the only run shape that can claim all required checks.
func remoteRunIsFullAuthoritativeAcceptance(
	plan gatecontract.GatePlan,
	catalog gatecontract.WorkloadCatalog,
	result remoteci.RunResult,
) bool {
	return plan.Profile == gatecontract.ProfileRelease && catalog.Authoritative && result.Authoritative
}

// remoteRunRequiredCheck maps every current canonical GateID to exactly one required-check family.
func remoteRunRequiredCheck(gateID gatecontract.GateID) (cicontract.RequiredCheck, error) {
	switch gateID {
	case gatecontract.GateIDFrontendE2E:
		return cicontract.RequiredCheckE2E, nil
	case gatecontract.GateIDBackendTestGuardWithRace:
		return cicontract.RequiredCheckRace, nil
	case gatecontract.GateIDBackendTestWithGuard:
		return cicontract.RequiredCheckNormal, nil
	case gatecontract.GateIDFrontendPreflight:
		return cicontract.RequiredCheckDependency, nil
	case gatecontract.GateIDFrontendLint,
		gatecontract.GateIDFrontendTest,
		gatecontract.GateIDFrontendFullTest,
		gatecontract.GateIDFrontendBuild,
		gatecontract.GateIDFrontendEmbedVerify:
		return cicontract.RequiredCheckFrontend, nil
	case gatecontract.GateIDAIMaintenanceSelfTest,
		gatecontract.GateIDLSPChangedDiagnostics,
		gatecontract.GateIDBackendNilness,
		gatecontract.GateIDSQLCVerify,
		gatecontract.GateIDCodemapCheck,
		gatecontract.GateIDProjectMapCheck,
		gatecontract.GateIDCapabilityContractCheck,
		gatecontract.GateIDWhitespaceCheck,
		gatecontract.GateIDReleaseLayeredCheck:
		return cicontract.RequiredCheckGate, nil
	default:
		return "", fmt.Errorf("remote CI gate %q has no required-check classification", gateID)
	}
}
