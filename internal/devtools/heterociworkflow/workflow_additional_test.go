package heterociworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

func TestWorkflowNewRejectsEveryMissingOrTypedNilDependency(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Dependencies)
	}{
		{name: "owner", mutate: func(deps *Dependencies) { deps.Owner = nil }},
		{name: "typed nil owner", mutate: func(deps *Dependencies) { deps.Owner = (*fakeOwner)(nil) }},
		{name: "probe transport", mutate: func(deps *Dependencies) { deps.ProbeTransport = nil }},
		{name: "typed nil probe transport", mutate: func(deps *Dependencies) { deps.ProbeTransport = (*fakeProbeTransport)(nil) }},
		{name: "executor binder", mutate: func(deps *Dependencies) { deps.Executors = nil }},
		{name: "typed nil executor binder", mutate: func(deps *Dependencies) { deps.Executors = (*fakeBinder)(nil) }},
		{name: "manifest loader", mutate: func(deps *Dependencies) { deps.LoadManifest = nil }},
		{name: "snapshot acquirer", mutate: func(deps *Dependencies) { deps.AcquireSnapshot = nil }},
		{name: "clock", mutate: func(deps *Dependencies) { deps.Now = nil }},
		{name: "cleanup timeout", mutate: func(deps *Dependencies) { deps.CleanupTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(t, &fakeExecutor{})
			deps := fixture.deps
			test.mutate(&deps)
			if _, err := New(deps); !errors.Is(err, ErrInvalidDependencies) {
				t.Fatalf("New error = %v, want ErrInvalidDependencies", err)
			}
			if len(fixture.events) != 0 {
				t.Fatalf("dependency validation caused side effects: %v", fixture.events)
			}
		})
	}
}

func TestWorkflowExecutorErrorRecoversFromSingleDurableReceipt(t *testing.T) {
	executor := &fakeExecutor{
		executeErr:   errors.New("transport acknowledgement lost"),
		receiptFound: true,
		receipt:      successfulBatchResult("unit-1"),
	}
	fixture := newWorkflowFixture(t, executor)
	executor.events = &fixture.events

	result, err := Execute(t.Context(), fixture.deps, fixture.request)
	if err != nil {
		t.Fatalf("Execute with receipt recovery: %v", err)
	}
	if executor.executeCount != 1 || executor.receiptCount != 1 {
		t.Fatalf("execute/receipt count = %d/%d", executor.executeCount, executor.receiptCount)
	}
	if fixture.owner.finishCount != 1 || fixture.owner.cancelCount != 0 || len(result.Units) != 1 || result.Units[0].State != heteroci.LeaseStateSucceeded {
		t.Fatalf("receipt recovery result=%+v finish/cancel=%d/%d", result.Units, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
	if err := assertWorkflowEvents(fixture.events, "load", "acquire", "locate", "snapshot", "plan", "read", "bind", "start", "execute", "receipt", "finish"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowNilBoundExecutorCancelsEveryPlannedLease(t *testing.T) {
	fixture := newWorkflowFixture(t, nil)

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutorBinding) {
		t.Fatalf("Execute error = %v, want ErrExecutorBinding", err)
	}
	if fixture.owner.cancelCount != len(fixture.units) || fixture.owner.startCount != 0 {
		t.Fatalf("cancel/start count = %d/%d", fixture.owner.cancelCount, fixture.owner.startCount)
	}
	if err := assertWorkflowEvents(fixture.events, "load", "acquire", "locate", "snapshot", "plan", "read", "bind", "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBoundExecutorsRequiresExactKeySet(t *testing.T) {
	keyA := ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"}
	keyB := ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-b"}
	keyExtra := ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-extra"}
	plan := ExecutorBindingPlan{Bindings: []ExecutorBinding{
		{NodeID: keyA.NodeID, ExecutorKind: keyA.Kind, BatchIDs: []BatchID{"batch-a"}},
		{NodeID: keyB.NodeID, ExecutorKind: keyB.Kind, BatchIDs: []BatchID{"batch-b"}},
	}}
	if err := plan.Validate(); err != nil {
		t.Fatalf("binding plan validation: %v", err)
	}
	executor := &fakeExecutor{}
	for _, test := range []struct {
		name  string
		bound map[ExecutorKey]BatchExecutor
	}{
		{name: "missing", bound: map[ExecutorKey]BatchExecutor{keyA: executor}},
		{name: "extra", bound: map[ExecutorKey]BatchExecutor{keyA: executor, keyB: executor, keyExtra: executor}},
		{name: "nil", bound: map[ExecutorKey]BatchExecutor{keyA: executor, keyB: nil}},
		{name: "key drift", bound: map[ExecutorKey]BatchExecutor{keyA: executor, keyExtra: executor}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBoundExecutors(plan, test.bound); !errors.Is(err, ErrExecutorBinding) {
				t.Fatalf("validateBoundExecutors error = %v, want ErrExecutorBinding", err)
			}
		})
	}
}

func TestWorkflowPostPlanReadValidationCancelsEveryPlannedLease(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	fixture.deps.Owner = &readDriftOwner{fakeOwner: fixture.owner}

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutionState) {
		t.Fatalf("Execute error = %v, want ErrExecutionState", err)
	}
	if fixture.owner.cancelCount != len(fixture.units) || fixture.owner.startCount != 0 {
		t.Fatalf("cancel/start count = %d/%d", fixture.owner.cancelCount, fixture.owner.startCount)
	}
	if err := assertWorkflowEvents(fixture.events, "load", "acquire", "locate", "snapshot", "plan", "read", "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRejectsBatchStatusOutsideNotVerified(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    "PASS",
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}}
	fixture := newWorkflowFixture(t, executor)
	executor.events = &fixture.events

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrBatchResult) || !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("Execute error = %v, want status and cleanup blockers", err)
	}
	if executor.executeCount != 1 || fixture.owner.finishCount != 0 || fixture.owner.cancelCount != 0 {
		t.Fatalf("execute/finish/cancel count = %d/%d/%d", executor.executeCount, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
}

func TestWorkflowOperationIDsAreDeterministicAndPhaseSeparated(t *testing.T) {
	units := []heteroci.ExecutionUnit{{UnitID: "unit-1"}, {UnitID: "unit-2"}}
	runIdentity, err := heteroci.DeriveTopologyRunIdentity("workflow-repository", strings.Repeat("a", 40), "workflow-run-ids")
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity: %v", err)
	}
	first, err := deriveRunOperationIDs(runIdentity.DurableRunID, units)
	if err != nil {
		t.Fatalf("deriveRunOperationIDs first: %v", err)
	}
	second, err := deriveRunOperationIDs(runIdentity.DurableRunID, units)
	if err != nil {
		t.Fatalf("deriveRunOperationIDs second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("operation IDs are nondeterministic: first=%#v second=%#v", first, second)
	}
	seen := map[heteroci.OperationID]struct{}{first.plan: {}}
	for _, unit := range units {
		for _, id := range []heteroci.OperationID{first.start[unit.UnitID], first.finish[unit.UnitID], first.cancel[unit.UnitID]} {
			if id == "" {
				t.Fatalf("operation ID for %s is empty", unit.UnitID)
			}
			if _, duplicate := seen[id]; duplicate {
				t.Fatalf("operation ID %s is reused across phase or unit", id)
			}
			seen[id] = struct{}{}
		}
	}
}

func TestWorkflowOperationIDsSeparateRepositoriesWithSameCallerRunID(t *testing.T) {
	units := []heteroci.ExecutionUnit{{UnitID: "unit-1"}}
	firstIdentity, err := heteroci.DeriveTopologyRunIdentity("workflow-repository", strings.Repeat("a", 40), "shared-run")
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity first: %v", err)
	}
	secondIdentity, err := heteroci.DeriveTopologyRunIdentity("other-repository", strings.Repeat("a", 40), "shared-run")
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity second: %v", err)
	}
	first, err := deriveRunOperationIDs(firstIdentity.DurableRunID, units)
	if err != nil {
		t.Fatalf("deriveRunOperationIDs first: %v", err)
	}
	second, err := deriveRunOperationIDs(secondIdentity.DurableRunID, units)
	if err != nil {
		t.Fatalf("deriveRunOperationIDs second: %v", err)
	}
	if first.plan == second.plan {
		t.Fatal("same caller RunID across repositories produced the same plan operation identity")
	}
}

func TestWorkflowBuildsOneDeterministicLocalWaveBatch(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	units := append([]heteroci.ExecutionUnit(nil), fixture.units...)
	second := units[0]
	second.UnitID = "unit-2"
	units = append(units, second)
	fence, err := fixture.owner.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	runIdentity := workflowRunIdentity(t, fixture)
	planID, err := DeriveOperationID(runIdentity.DurableRunID, OperationPhasePlan, "")
	if err != nil {
		t.Fatalf("DeriveOperationID: %v", err)
	}
	planned, err := fixture.owner.PlanAndReserve(t.Context(), fence, workflowPlanRequest(t, fixture, planID, units))
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	batches, err := BuildBatches(string(runIdentity.DurableRunID), fixture.manifest, planned, units, fixture.now, fixture.request.HeartbeatTTL)
	if err != nil {
		t.Fatalf("BuildBatches: %v", err)
	}
	assertSingleLocalWaveBatch(t, fixture, batches)
}

func assertSingleLocalWaveBatch(t *testing.T, fixture *workflowFixture, batches []BatchPlan) {
	t.Helper()
	if len(batches) != 1 {
		t.Fatalf("batch count = %d, want 1", len(batches))
	}
	batch := batches[0].Batch
	if batch.Key != (ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: fixture.manifest.Nodes[0].NodeID}) || batch.Wave != 0 {
		t.Fatalf("batch key/wave = %#v/%d", batch.Key, batch.Wave)
	}
	if len(batch.Units) != 2 || batch.Units[0].UnitID != "unit-1" || batch.Units[1].UnitID != "unit-2" || len(batch.Assignments) != 2 {
		t.Fatalf("batch units/assignments = %#v/%#v", batch.Units, batch.Assignments)
	}
	wantID, err := DeriveBatchID(batch.RunID, batch.Key, batch.Wave, []string{"unit-1", "unit-2"})
	if err != nil {
		t.Fatalf("DeriveBatchID: %v", err)
	}
	if batch.BatchID != wantID {
		t.Fatalf("batch ID = %s, want %s", batch.BatchID, wantID)
	}
}

func successfulBatchResult(unitID string) BatchResult {
	return BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: unitID, State: heteroci.LeaseStateSucceeded}},
	}
}

type readDriftOwner struct {
	*fakeOwner
}

func (owner *readDriftOwner) ReadRun(ctx context.Context, runID heteroci.TopologyRunID) (heterociowner.DurableRunState, error) {
	state, err := owner.fakeOwner.ReadRun(ctx, runID)
	if err != nil {
		return heterociowner.DurableRunState{}, err
	}
	state.LastRevision++
	return state, nil
}

var _ DurableOwner = (*readDriftOwner)(nil)

func TestRunRequestPlanAuditFieldRegistry(t *testing.T) {
	producer := make(map[string]bool)
	requestType := reflect.TypeFor[RunRequest]()
	for field := range requestType.Fields() {
		producer[field.Name] = true
	}
	consumer := map[string]string{
		"SchemaVersion": "strict request decoder", "ExternalOperationID": "bind operation journal", "DurableRunID": "derived topology identity",
		"RequestDigest": "canonical request digest", "ManifestDigest": "portable target content binding", "ExternalBindingDigest": "portable external engine binding", "ManifestBlobSHA256": "exact raw manifest blob", "RuntimePolicyDigest": "runtime policy binding", "TopologyManifestDigest": "topology manifest digest",
		"ManifestPath": "strict manifest loader", "RepositoryID": "topology plan audit request", "SourceTreeSHA": "topology plan audit request",
		"RunID": "topology plan and execution identity", "Units": "topology planner and executor", "HeartbeatTTL": "snapshot and lease validation",
	}
	var missing, stale []string
	for field := range producer {
		if consumer[field] == "" {
			missing = append(missing, field)
		}
	}
	for field := range consumer {
		if !producer[field] {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("RunRequest plan audit field registry drifted: missing=%v stale=%v", missing, stale)
	}
}

func TestRunRequestDigestBindsExternalPortableAndRuntimeFacts(t *testing.T) {
	base := newWorkflowFixture(t, &fakeExecutor{}).request
	for _, test := range []struct {
		name   string
		mutate func(*RunRequest)
	}{
		{name: "engine binding", mutate: func(request *RunRequest) { request.ExternalBindingDigest = "sha256:" + strings.Repeat("1", 64) }},
		{name: "raw manifest blob", mutate: func(request *RunRequest) { request.ManifestBlobSHA256 = "sha256:" + strings.Repeat("2", 64) }},
		{name: "runtime policy", mutate: func(request *RunRequest) { request.RuntimePolicyDigest = "sha256:" + strings.Repeat("3", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			derived, err := DeriveRunRequestDigest(request)
			if err != nil {
				t.Fatalf("DeriveRunRequestDigest: %v", err)
			}
			if derived == base.RequestDigest {
				t.Fatal("external binding mutation did not change request digest")
			}
			if err := validateRunRequest(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateRunRequest error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestExecutorBindingPlanFieldRegistry(t *testing.T) {
	planFields := reflect.TypeFor[ExecutorBindingPlan]()
	planConsumers := map[string]string{"SchemaVersion": "PG bind journal", "ExternalOperationID": "PG operation identity", "DurableRunID": "persisted run", "RequestDigest": "request binding", "ManifestDigest": "portable target content binding", "ExternalBindingDigest": "portable external engine binding", "ManifestBlobSHA256": "exact raw manifest blob", "RuntimePolicyDigest": "runtime policy binding", "TopologyManifestDigest": "topology manifest binding", "PlanDigest": "persisted plan", "AuthorityGeneration": "owner epoch", "Bindings": "sorted node bindings", "BindingDigest": "aggregate receipt projection"}
	var missing, stale []string
	for field := range planFields.Fields() {
		if planConsumers[field.Name] == "" {
			missing = append(missing, field.Name)
		}
	}
	for field := range planConsumers {
		if _, ok := planFields.FieldByName(field); !ok {
			stale = append(stale, field)
		}
	}
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("ExecutorBindingPlan field registry drifted: missing=%v stale=%v", missing, stale)
	}
	bindingFields := reflect.TypeFor[ExecutorBinding]()
	for _, field := range []string{"NodeID", "NodeIDDigest", "ExecutorKind", "BatchIDs", "ShardDigest", "BatchSetDigest", "UnitSetDigest", "NodeFactsDigest"} {
		if _, ok := bindingFields.FieldByName(field); !ok {
			t.Fatalf("ExecutorBinding missing producer field %q", field)
		}
	}
}

func TestWorkflowPlanRequestCarriesPerRunRepositoryAudit(t *testing.T) {
	executor := &fakeExecutor{result: successfulBatchResult("unit-1")}
	fixture := newWorkflowFixture(t, executor)
	executor.events = &fixture.events
	fixture.request = strictWorkflowTestRequest(t, fixture.manifest, "repository-b", strings.Repeat("b", 40), fixture.request.RunID, fixture.units, fixture.request.HeartbeatTTL)
	if _, err := Execute(t.Context(), fixture.deps, fixture.request); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	identity := fixture.owner.planRequest.RunIdentity
	if identity.RepositoryID != fixture.request.RepositoryID || identity.SourceTreeSHA != fixture.request.SourceTreeSHA || identity.CallerRunID != fixture.request.RunID {
		t.Fatalf("owner plan audit = %#v, want %q/%q/%q", identity, fixture.request.RepositoryID, fixture.request.SourceTreeSHA, fixture.request.RunID)
	}
	if err := identity.Validate(); err != nil {
		t.Fatalf("owner plan durable identity: %v", err)
	}
}

func TestWorkflowRejectsMissingOrNonCanonicalRepositoryAuditBeforeSideEffects(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*RunRequest)
	}{
		{name: "empty repository", mutate: func(request *RunRequest) { request.RepositoryID = "" }},
		{name: "empty tree", mutate: func(request *RunRequest) { request.SourceTreeSHA = "" }},
		{name: "uppercase tree", mutate: func(request *RunRequest) { request.SourceTreeSHA = strings.Repeat("A", 40) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(t, &fakeExecutor{})
			test.mutate(&fixture.request)
			if _, err := Execute(t.Context(), fixture.deps, fixture.request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Execute error = %v, want ErrInvalidRequest", err)
			}
			if len(fixture.events) != 0 {
				t.Fatalf("invalid audit caused side effects: %v", fixture.events)
			}
		})
	}
}

func TestAutomaticWorkflowRunRequestConsumesRepositoryAudit(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve workflow field guard source path")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "remotecicli", "execute.go"))
	if err != nil {
		t.Fatalf("read automatic workflow caller: %v", err)
	}
	for _, binding := range []string{
		"RepositoryID: portable.RepositoryID",
		"SourceTreeSHA: portable.TreeSHA",
		"ExternalBindingDigest: portable.BindingSHA256",
		"ManifestBlobSHA256: portable.ManifestBlobSHA256",
		"RuntimePolicyDigest: runtimePolicy.policyDigest",
	} {
		if !strings.Contains(string(source), binding) {
			t.Fatalf("automatic workflow caller is missing plan audit binding %q", binding)
		}
	}
}
