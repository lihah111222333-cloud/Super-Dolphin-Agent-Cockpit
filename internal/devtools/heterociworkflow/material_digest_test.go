package heterociworkflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestExecutionMaterialDigestBindsEveryBatchField(t *testing.T) {
	batch := materialDigestTestBatch(t)
	want, err := DeriveExecutionMaterialDigest(batch)
	if err != nil {
		t.Fatalf("DeriveExecutionMaterialDigest: %v", err)
	}
	if !strings.HasPrefix(want, "sha256:") {
		t.Fatalf("material digest = %q, want sha256 prefix", want)
	}
	again, err := DeriveExecutionMaterialDigest(batch)
	if err != nil || again != want {
		t.Fatalf("material digest is not deterministic: first=%q second=%q err=%v", want, again, err)
	}
	mutations := []struct {
		name   string
		mutate func(*ExecutionBatch)
	}{
		{name: "batch id", mutate: func(value *ExecutionBatch) { value.BatchID = BatchID("sha256:" + strings.Repeat("a", 64)) }},
		{name: "run id", mutate: func(value *ExecutionBatch) { value.RunID = "workflow-run-2" }},
		{name: "executor key", mutate: func(value *ExecutionBatch) { value.Key.NodeID = "workflow-node-2" }},
		{name: "wave", mutate: func(value *ExecutionBatch) { value.Wave++ }},
		{name: "unit resource", mutate: func(value *ExecutionBatch) { value.Units[0].CPUMilli++ }},
		{name: "assignment endpoint", mutate: func(value *ExecutionBatch) { value.Assignments[0].Endpoint = "https://drifted.invalid/runner" }},
		{name: "started fence", mutate: func(value *ExecutionBatch) { value.Started[0].Fence.Revision++ }},
		{name: "started lease", mutate: func(value *ExecutionBatch) {
			value.Started[0].Lease.DecisionDigest = "sha256:" + strings.Repeat("f", 64)
		}},
		{name: "started occupancy", mutate: func(value *ExecutionBatch) {
			value.Started[0].Occupancy.DecisionDigest = "sha256:" + strings.Repeat("e", 64)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := cloneExecutionBatch(batch)
			mutation.mutate(&candidate)
			got, digestErr := DeriveExecutionMaterialDigest(candidate)
			if digestErr == nil && got == want {
				t.Fatalf("material digest ignored %s drift: %q", mutation.name, got)
			}
		})
	}
}

func TestNormalizeBatchResultBindsMaterialAndCommandDigest(t *testing.T) {
	batch := materialDigestTestBatch(t)
	materialDigest, err := DeriveExecutionMaterialDigest(batch)
	if err != nil {
		t.Fatalf("DeriveExecutionMaterialDigest: %v", err)
	}
	batch.MaterialDigest = materialDigest
	result := BatchResult{
		BatchID:        batch.BatchID,
		RunID:          batch.RunID,
		Executor:       batch.Key,
		Authority:      heteroci.AuthorityLocalNonAuthoritative,
		Status:         ResultStatusNotVerified,
		MaterialDigest: materialDigest,
		CommandDigest:  "sha256:" + strings.Repeat("c", 64),
		Outcomes:       []BatchOutcome{{UnitID: batch.Units[0].UnitID, State: heteroci.LeaseStateSucceeded}},
	}
	if _, err := normalizeBatchResult(batch, result); err != nil {
		t.Fatalf("normalizeBatchResult canonical material: %v", err)
	}
	for _, mutation := range []struct {
		name   string
		mutate func(*BatchResult)
	}{
		{name: "material digest", mutate: func(value *BatchResult) { value.MaterialDigest = "sha256:" + strings.Repeat("d", 64) }},
		{name: "missing material digest", mutate: func(value *BatchResult) { value.MaterialDigest = "" }},
		{name: "missing command digest", mutate: func(value *BatchResult) { value.CommandDigest = "" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := result
			mutation.mutate(&candidate)
			if _, err := normalizeBatchResult(batch, candidate); !errors.Is(err, ErrBatchResult) {
				t.Fatalf("normalizeBatchResult(%s) error = %v, want ErrBatchResult", mutation.name, err)
			}
		})
	}
}

func TestBatchReceiptReaderReceivesFullExpectedExecutionBatch(t *testing.T) {
	reader := &materialDigestReceiptReader{}
	var contract BatchReceiptReader = reader
	batch := materialDigestTestBatch(t)
	if _, _, err := contract.ReadBatchReceipt(context.Background(), batch); err != nil {
		t.Fatalf("ReadBatchReceipt: %v", err)
	}
	if reader.batch.MaterialDigest != batch.MaterialDigest || reader.batch.Started[0] != batch.Started[0] {
		t.Fatalf("receipt reader did not receive full expected batch: %#v", reader.batch)
	}
}

type materialDigestReceiptReader struct {
	batch ExecutionBatch
}

func (reader *materialDigestReceiptReader) ReadBatchReceipt(_ context.Context, batch ExecutionBatch) (BatchResult, bool, error) {
	reader.batch = cloneExecutionBatch(batch)
	return BatchResult{BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, MaterialDigest: batch.MaterialDigest, CommandDigest: "sha256:" + strings.Repeat("c", 64), Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, Outcomes: []BatchOutcome{{UnitID: batch.Units[0].UnitID, State: heteroci.LeaseStateSucceeded}}}, true, nil
}

func materialDigestTestBatch(t *testing.T) ExecutionBatch {
	t.Helper()
	fixture := newWorkflowFixture(&fakeExecutor{})
	fence, err := fixture.owner.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	runIdentity := workflowRunIdentity(t, fixture)
	planID, err := DeriveOperationID(runIdentity.DurableRunID, OperationPhasePlan, "")
	if err != nil {
		t.Fatalf("DeriveOperationID: %v", err)
	}
	planned, err := fixture.owner.PlanAndReserve(t.Context(), fence, workflowPlanRequest(t, fixture, planID, fixture.units))
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	batches, err := BuildBatches(string(runIdentity.DurableRunID), fixture.manifest, planned, fixture.units, fixture.now, fixture.request.HeartbeatTTL)
	if err != nil {
		t.Fatalf("BuildBatches: %v", err)
	}
	startID, err := DeriveOperationID(runIdentity.DurableRunID, OperationPhaseStart, fixture.units[0].UnitID)
	if err != nil {
		t.Fatalf("DeriveOperationID(start): %v", err)
	}
	started, err := fixture.owner.Start(t.Context(), planned.Fence, startID, runIdentity.DurableRunID, fixture.units[0].UnitID, fixture.snapshot)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	batch := batches[0].Batch
	batch.Started = []heteroci.StartedRoutingLease{started}
	return batch
}
