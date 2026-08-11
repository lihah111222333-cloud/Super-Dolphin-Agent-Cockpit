package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestFinalizeRemoteRunEvidenceAuthoritativeSubset exercises the production
// finalizer from a persisted full catalog and a one-workload subset receipt.
func TestFinalizeRemoteRunEvidenceAuthoritativeSubset(t *testing.T) {
	plan, _, selected, _, result, store := remoteRunReceiptTestProductionShapedSubset(t)
	input := remoteRunReceiptTestInput(plan, store)
	if err := finalizeRemoteRunEvidence(input, &result, nil); err != nil {
		t.Fatalf("finalizeRemoteRunEvidence() error = %v", err)
	}
	if !result.Authoritative {
		t.Fatal("finalizeRemoteRunEvidence() did not mark the subset result authoritative")
	}
	reloaded, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	if !reloaded.Authoritative {
		t.Fatal("persisted subset run is not authoritative")
	}
	if reloaded.Scope == nil || !reloaded.Scope.IsSubset() {
		t.Fatalf("reloaded scope = %#v, want persisted subset", reloaded.Scope)
	}
	if !slices.Equal(reloaded.Scope.SelectedGateIDs(), result.Scope.SelectedGateIDs()) {
		t.Fatalf("reloaded selected workloads = %v, want %v", reloaded.Scope.SelectedGateIDs(), result.Scope.SelectedGateIDs())
	}
	if !slices.Contains(reloaded.Scope.SelectedGateIDs(), selected) {
		t.Fatalf("reloaded subset does not contain selected workload %q", selected)
	}
}

// TestFinalizeRemoteRunEvidenceAuthoritativeSubsetAllHit keeps the persisted
// subset through a reused-only finalization and authoritative reload.
func TestFinalizeRemoteRunEvidenceAuthoritativeSubsetAllHit(t *testing.T) {
	plan, _, _, _, origin, store := remoteRunReceiptTestProductionShapedSubset(t)
	input := remoteRunReceiptTestInput(plan, store)
	if err := finalizeRemoteRunEvidence(input, &origin, nil); err != nil {
		t.Fatalf("finalize subset origin: %v", err)
	}
	originRecord, reused := remoteRunPromotedReuseEvidence(t, store, origin.JobID)
	result := origin
	result.JobID = "remote-subset-all-hit"
	result.Authoritative = false
	result.CandidateGateSourceSHA256 = ""
	result.CandidateGateToolchainSHA256 = ""
	result.FreshWorkloadExecutions = nil
	result.ReusedWorkloads = reused
	input.CandidateGateSourceSHA256 = ""
	input.CandidateGateToolchainSHA256 = ""
	recordRemoteRunAllReuseFixture(t, store, originRecord, result)
	if err := finalizeRemoteRunEvidence(input, &result, nil); err != nil {
		t.Fatalf("finalize subset all-hit: %v", err)
	}
	reloaded, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	assertRemoteRunSubsetAllHitReload(t, reloaded, origin)
}

func assertRemoteRunSubsetAllHitReload(t *testing.T, reloaded gatecontract.RemoteCIRunRecord, origin remoteci.RunResult) {
	t.Helper()
	if !reloaded.Authoritative || reloaded.Scope == nil || !reloaded.Scope.IsSubset() {
		t.Fatalf("reloaded all-hit subset = %#v, want authoritative subset", reloaded)
	}
	if !slices.Equal(reloaded.Scope.SelectedGateIDs(), origin.Scope.SelectedGateIDs()) {
		t.Fatalf("reloaded all-hit selected workloads = %v, want %v", reloaded.Scope.SelectedGateIDs(), origin.Scope.SelectedGateIDs())
	}
	if len(reloaded.Shards) != 0 || len(reloaded.WorkloadExecutions) != 0 || len(reloaded.TimingObservations) != 0 {
		t.Fatalf("all-hit subset wrote fresh execution artifacts: shards=%d workloads=%d timings=%d", len(reloaded.Shards), len(reloaded.WorkloadExecutions), len(reloaded.TimingObservations))
	}
}

// TestFinalizeRemoteRunEvidenceAuthoritativeFullRegression retains six-check
// full execution behavior; full scope reloads as the legacy nil representation.
func TestFinalizeRemoteRunEvidenceAuthoritativeFullRegression(t *testing.T) {
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	result := remoteRunReceiptTestResult(t, plan, catalog)
	store := remoteRunReceiptTestAuthority(t, catalog, result)
	result = remoteRunReceiptTestResultFromStoredExecution(t, store, result)
	if err := finalizeRemoteRunEvidence(remoteRunReceiptTestInput(plan, store), &result, nil); err != nil {
		t.Fatalf("finalize full result: %v", err)
	}
	reloaded, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	if !reloaded.Authoritative {
		t.Fatal("reloaded full result is not authoritative")
	}
	if reloaded.Scope != nil {
		t.Fatalf("reloaded full scope = %#v, want nil legacy/full representation", reloaded.Scope)
	}
}

// remoteRunReceiptTestResultFromStoredExecution models the production result
// after SQLite canonicalizes fresh workload execution timing. Aggregate gate
// executions retain their original attestation logs: the readback comparator
// intentionally excludes those bounded log bytes from the SQLite projection.
func remoteRunReceiptTestResultFromStoredExecution(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult) remoteci.RunResult {
	t.Helper()
	recorded, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() canonical execution projection: %v", err)
	}
	result.WorkloadExecutions = recorded.WorkloadExecutions
	result.FreshWorkloadExecutions = recorded.WorkloadExecutions
	return result
}

// TestRemoteRunAuthorityIdentityScopeFieldGuard dynamically verifies the
// producer and authority identity have the same typed Scope field, then proves
// the real mapper preserves its persisted subset value.
func TestRemoteRunAuthorityIdentityScopeFieldGuard(t *testing.T) {
	producerField, ok := reflect.TypeFor[remoteci.RunResult]().FieldByName("Scope")
	if !ok {
		t.Fatal("RunResult Scope field is missing")
	}
	consumerField, ok := reflect.TypeFor[gatecontract.RemoteCIRunAuthorityIdentity]().FieldByName("Scope")
	if !ok {
		t.Fatal("RemoteCIRunAuthorityIdentity Scope field is missing")
	}
	storedField, ok := reflect.TypeFor[gatecontract.RemoteCIRunRecord]().FieldByName("Scope")
	if !ok {
		t.Fatal("RemoteCIRunRecord Scope field is missing")
	}
	if producerField.Type != consumerField.Type {
		t.Fatalf("Scope field types: producer=%v consumer=%v", producerField.Type, consumerField.Type)
	}
	if producerField.Type != storedField.Type {
		t.Fatalf("Scope field types: producer=%v stored=%v", producerField.Type, storedField.Type)
	}
	_, _, _, _, result, _ := remoteRunReceiptTestProductionShapedSubset(t)
	identity := remoteRunAuthorityIdentity(result)
	if identity.Scope == nil || result.Scope == nil {
		t.Fatalf("mapped Scope = %#v, result Scope = %#v; want non-nil subset", identity.Scope, result.Scope)
	}
	if !slices.Equal(identity.Scope.SelectedGateIDs(), result.Scope.SelectedGateIDs()) {
		t.Fatalf("mapped Scope selected workloads = %v, want %v", identity.Scope.SelectedGateIDs(), result.Scope.SelectedGateIDs())
	}
}

// TestRemoteRunCheckObservationsFollowExecutionScope ensures a subset receipt
// covers exactly its selected shardable workloads and cannot carry a release
// owner attestation from the full catalog.
func TestRemoteRunCheckObservationsFollowExecutionScope(t *testing.T) {
	plan, catalog, selected, unexpected, result, store := remoteRunReceiptTestProductionShapedSubset(t)

	observations, err := remoteRunCheckObservations(plan, catalog, result.ImageCacheSnapshotID, result)
	if err != nil {
		t.Fatalf("remoteRunCheckObservations() error = %v", err)
	}
	if len(observations) != 1 || observations[0].Check != mustRemoteRunReceiptTestCheck(t, selected) {
		t.Fatalf("subset observations = %#v, want exactly selected workload check", observations)
	}
	input := remoteRunReceiptTestInput(plan, store)
	if _, _, err := validateRemoteRunContract(input, 7, result); err != nil {
		t.Fatalf("validateRemoteRunContract() accepted subset parent aggregate error = %v", err)
	}

	t.Run("invalid aggregate profile", func(t *testing.T) { assertRemoteRunSubsetInvalidAggregateProfile(t, store, result) })
	t.Run("missing selected workload", func(t *testing.T) { assertRemoteRunSubsetMissingWorkload(t, plan, catalog, result) })
	t.Run("extra workload", func(t *testing.T) { assertRemoteRunSubsetExtraWorkload(t, plan, catalog, unexpected, result) })
	t.Run("release owner", func(t *testing.T) { assertRemoteRunSubsetRejectsReleaseOwner(t, plan, catalog, result) })
	t.Run("unknown aggregate", func(t *testing.T) { assertRemoteRunSubsetRejectsUnknownAggregate(t, plan, catalog, result) })
	t.Run("unselected workload parent", func(t *testing.T) { assertRemoteRunSubsetRejectsOtherParent(t, plan, catalog, unexpected, result) })
	t.Run("duplicate aggregate", func(t *testing.T) { assertRemoteRunSubsetRejectsDuplicateAggregate(t, plan, catalog, result) })
}

func assertRemoteRunSubsetInvalidAggregateProfile(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult) {
	t.Helper()
	forged, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	forged.Executions = append([]gatecontract.PlanGateExecution(nil), forged.Executions...)
	forged.Executions[0].ExecutionProfile = gatecontract.ExecutionProfile{}
	if err := store.RecordProvisionalRemoteCIRun(forged); err == nil {
		t.Fatal("RecordProvisionalRemoteCIRun() accepted invalid subset parent aggregate profile")
	}
}

func assertRemoteRunSubsetMissingWorkload(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, result remoteci.RunResult) {
	t.Helper()
	result.WorkloadExecutions = nil
	result.FreshWorkloadExecutions = nil
	remoteRunReceiptTestObservationRejected(t, plan, catalog, result)
}

func assertRemoteRunSubsetExtraWorkload(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, unexpected gatecontract.GateID, result remoteci.RunResult) {
	t.Helper()
	complete := remoteRunReceiptTestResult(t, plan, catalog)
	result.WorkloadExecutions = append(result.WorkloadExecutions, remoteRunReceiptTestExecutionsForWorkload(complete.WorkloadExecutions, unexpected)...)
	result.FreshWorkloadExecutions = append(result.FreshWorkloadExecutions, remoteRunReceiptTestExecutionsForWorkload(complete.FreshWorkloadExecutions, unexpected)...)
	remoteRunReceiptTestObservationRejected(t, plan, catalog, result)
}

func assertRemoteRunSubsetRejectsReleaseOwner(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, result remoteci.RunResult) {
	t.Helper()
	result.GateExecutions = append(result.GateExecutions, gatecontract.PlanGateExecution{GateID: gatecontract.GateIDReleaseLayeredCheck})
	remoteRunReceiptTestObservationRejected(t, plan, catalog, result)
}

func assertRemoteRunSubsetRejectsUnknownAggregate(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, result remoteci.RunResult) {
	t.Helper()
	result.GateExecutions = append([]gatecontract.PlanGateExecution(nil), result.GateExecutions...)
	result.GateExecutions[0].GateID = "unknown-aggregate"
	remoteRunReceiptTestObservationRejected(t, plan, catalog, result)
}

func assertRemoteRunSubsetRejectsOtherParent(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, unexpected gatecontract.GateID, result remoteci.RunResult) {
	t.Helper()
	otherScope, err := gatecontract.NewRemoteCISubsetExecutionScope(catalog, []gatecontract.GateID{unexpected})
	if err != nil {
		t.Fatal(err)
	}
	complete := remoteRunReceiptTestResult(t, plan, catalog)
	result.GateExecutions = append([]gatecontract.PlanGateExecution(nil), result.GateExecutions...)
	result.GateExecutions[0] = remoteRunReceiptTestSubsetParentExecutions(t, catalog, otherScope, complete.WorkloadExecutions)[0]
	remoteRunReceiptTestObservationRejected(t, plan, catalog, result)
}

func assertRemoteRunSubsetRejectsDuplicateAggregate(t *testing.T, plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog, result remoteci.RunResult) {
	t.Helper()
	result.GateExecutions = append([]gatecontract.PlanGateExecution(nil), result.GateExecutions...)
	result.GateExecutions = append(result.GateExecutions, result.GateExecutions[0])
	remoteRunReceiptTestObservationRejected(t, plan, catalog, result)
}

func remoteRunReceiptTestProductionShapedSubset(t *testing.T) (gatecontract.GatePlan, gatecontract.WorkloadCatalog, gatecontract.GateID, gatecontract.GateID, remoteci.RunResult, *gatecontract.DurationLedgerStore) {
	t.Helper()
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	selected, unexpected := remoteRunReceiptTestSubsetWorkloads(t, catalog)
	scope, err := gatecontract.NewRemoteCISubsetExecutionScope(catalog, []gatecontract.GateID{selected})
	if err != nil {
		t.Fatal(err)
	}
	result := remoteRunReceiptTestResult(t, plan, catalog)
	result.Scope = &scope
	result.WorkloadExecutions = remoteRunReceiptTestExecutionsForWorkload(result.WorkloadExecutions, selected)
	result.FreshWorkloadExecutions = remoteRunReceiptTestExecutionsForWorkload(result.FreshWorkloadExecutions, selected)
	result.WorkloadPassIdentities = remoteRunReceiptTestIdentitiesForWorkload(result.WorkloadPassIdentities, selected)
	result.GateExecutions = remoteRunReceiptTestSubsetParentExecutions(t, catalog, scope, result.WorkloadExecutions)
	store := remoteRunReceiptTestAuthority(t, catalog, result)
	recorded, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	result.WorkloadExecutions = recorded.WorkloadExecutions
	result.FreshWorkloadExecutions = recorded.WorkloadExecutions
	result.GateExecutions = remoteRunReceiptTestSubsetParentExecutions(t, catalog, scope, result.WorkloadExecutions)
	recorded.Executions = append([]gatecontract.PlanGateExecution(nil), result.GateExecutions...)
	if err := store.RecordProvisionalRemoteCIRun(recorded); err != nil {
		t.Fatalf("RecordProvisionalRemoteCIRun() production-shaped subset aggregate: %v", err)
	}
	return plan, catalog, selected, unexpected, result, store
}

func TestRemoteRunContractExecutionScopeAuthority(t *testing.T) {
	_, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	selected, other := remoteRunReceiptTestSubsetWorkloads(t, catalog)
	subset, err := gatecontract.NewRemoteCISubsetExecutionScope(catalog, []gatecontract.GateID{selected})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRemoteRunRecordedExecutionScope(catalog, &subset, &subset); err != nil {
		t.Fatalf("validate matching subset scope: %v", err)
	}
	if err := validateRemoteRunRecordedExecutionScope(catalog, nil, &subset); err == nil {
		t.Fatal("missing persisted subset scope was accepted")
	}
	otherSubset, err := gatecontract.NewRemoteCISubsetExecutionScope(catalog, []gatecontract.GateID{other})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRemoteRunRecordedExecutionScope(catalog, &otherSubset, &subset); err == nil {
		t.Fatal("mismatched persisted subset scope was accepted")
	}
	full, err := gatecontract.NewRemoteCIFullExecutionScope(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRemoteRunRecordedExecutionScope(catalog, nil, &full); err != nil {
		t.Fatalf("full scope without a side-table row: %v", err)
	}
	if err := validateRemoteRunRecordedExecutionScope(catalog, &subset, &full); err == nil {
		t.Fatal("full scope accepted a persisted subset row")
	}
}

func TestRemoteRunContractExecutionCatalogKeepsFullCatalog(t *testing.T) {
	_, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	full, err := gatecontract.NewRemoteCIFullExecutionScope(catalog)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := remoteRunContractExecutionCatalog(catalog, &full)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected.Workloads) != len(catalog.Workloads) {
		t.Fatalf("full execution catalog workloads = %d, want %d", len(projected.Workloads), len(catalog.Workloads))
	}
}

func remoteRunReceiptTestSubsetWorkloads(t *testing.T, catalog gatecontract.WorkloadCatalog) (gatecontract.GateID, gatecontract.GateID) {
	t.Helper()
	selected := gatecontract.GateID("")
	selectedParent := gatecontract.GateID("")
	for _, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		if selected == "" {
			selected = gatecontract.GateID(workload.ID)
			parent, err := gatecontract.WorkloadParentGateID(workload.ID)
			if err != nil {
				t.Fatalf("WorkloadParentGateID(%q) error = %v", workload.ID, err)
			}
			selectedParent = parent
			continue
		}
		parent, err := gatecontract.WorkloadParentGateID(workload.ID)
		if err != nil {
			t.Fatalf("WorkloadParentGateID(%q) error = %v", workload.ID, err)
		}
		if parent != selectedParent {
			return selected, gatecontract.GateID(workload.ID)
		}
	}
	t.Fatal("catalog has fewer than two shardable parents")
	return "", ""
}

// remoteRunReceiptTestSubsetParentExecutions uses the same canonical parent
// aggregate constructor as PrepareSubset/RunPrepared; fixtures must not
// hand-assemble an aggregate with an incomplete execution profile.
func remoteRunReceiptTestSubsetParentExecutions(
	t *testing.T,
	catalog gatecontract.WorkloadCatalog,
	scope gatecontract.RemoteCIExecutionScope,
	workloadExecutions []gatecontract.PlanGateExecution,
) []gatecontract.PlanGateExecution {
	t.Helper()
	if err := scope.ValidateAgainstCatalog(catalog); err != nil {
		t.Fatalf("ValidateAgainstCatalog() error = %v", err)
	}
	executionCatalog, err := remoteRunContractExecutionCatalog(catalog, &scope)
	if err != nil {
		t.Fatalf("remoteRunContractExecutionCatalog() error = %v", err)
	}
	observed := make(map[string]gatecontract.PlanGateExecution, len(executionCatalog.Workloads))
	selected := make(map[gatecontract.GateID]struct{}, len(executionCatalog.Workloads))
	for _, workload := range executionCatalog.Workloads {
		selected[gatecontract.GateID(workload.ID)] = struct{}{}
	}
	for _, execution := range workloadExecutions {
		if _, ok := selected[execution.GateID]; !ok {
			continue
		}
		observed[string(execution.GateID)] = execution
	}
	executions, status, err := remoteci.AggregateCatalogWorkloads(executionCatalog, observed)
	if err != nil {
		t.Fatalf("AggregateCatalogWorkloads() error = %v", err)
	}
	if status != gatecontract.ResultStatusPassed {
		t.Fatalf("AggregateCatalogWorkloads() status = %q, want passed", status)
	}
	if len(executions) == 0 {
		t.Fatal("subset scope has no shardable parent aggregate")
	}
	return executions
}

func remoteRunReceiptTestExecutionsForWorkload(executions []gatecontract.PlanGateExecution, workloadID gatecontract.GateID) []gatecontract.PlanGateExecution {
	filtered := make([]gatecontract.PlanGateExecution, 0, 1)
	for _, execution := range executions {
		if execution.GateID == workloadID {
			filtered = append(filtered, execution)
		}
	}
	return filtered
}

func remoteRunReceiptTestIdentitiesForWorkload(identities []gatecontract.WorkloadPassIdentity, workloadID gatecontract.GateID) []gatecontract.WorkloadPassIdentity {
	filtered := make([]gatecontract.WorkloadPassIdentity, 0, 1)
	for _, identity := range identities {
		if identity.WorkloadID == workloadID {
			filtered = append(filtered, identity)
		}
	}
	return filtered
}

func mustRemoteRunReceiptTestCheck(t *testing.T, workloadID gatecontract.GateID) cicontract.RequiredCheck {
	t.Helper()
	check, err := gatecontract.RequiredCheckForWorkloadID(string(workloadID))
	if err != nil {
		t.Fatalf("RequiredCheckForWorkloadID(%q): %v", workloadID, err)
	}
	return check
}
