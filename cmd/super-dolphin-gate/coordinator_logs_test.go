package main

import (
	"context"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestCoordinatorGateLogPersistsBoundedDigestBoundEvidence(t *testing.T) {
	store, record := coordinatorLogTestStore(t)
	gateID := record.Plan.Gates[0].ID
	logData := []byte("2026-07-18T00:00:00Z first actionable failure\n")
	digest := coordinatorLogDigest(logData)

	if err := store.recordGateLog(context.Background(), record.JobID, gateID, digest, logData); err != nil {
		t.Fatalf("recordGateLog() error = %v", err)
	}
	got, err := store.gateLog(context.Background(), record.JobID, gateID)
	if err != nil {
		t.Fatalf("gateLog() error = %v", err)
	}
	if got.Log != string(logData) || got.LogDigest != digest || coordinatorLogDigest([]byte(got.Log)) != got.LogDigest {
		t.Fatalf("gateLog() = %#v", got)
	}

	if _, err := store.db.Exec("UPDATE coordinator_gate_logs SET log_data = ? WHERE job_id = ? AND gate_id = ?", "tampered", record.JobID, gateID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.gateLog(context.Background(), record.JobID, gateID); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered gateLog() error = %v", err)
	}
}

func TestCoordinatorGateLogRejectsUnauthorizedOversizedAndConflictingEvidence(t *testing.T) {
	store, record := coordinatorLogTestStore(t)
	gateID := record.Plan.Gates[0].ID
	data := []byte("failure\n")
	digest := coordinatorLogDigest(data)

	unknownGate := gatecontract.GateID("unknown:gate")
	if err := store.recordGateLog(context.Background(), record.JobID, unknownGate, digest, data); err == nil {
		t.Fatal("recordGateLog() accepted a gate outside the immutable job plan")
	}
	if _, err := store.gateLog(context.Background(), "job-unknown", gateID); err == nil {
		t.Fatal("gateLog() accepted an unknown job authority")
	}
	oversized := make([]byte, localci.MaxFreshContainerLogBytes+1)
	if err := store.recordGateLog(context.Background(), record.JobID, gateID, coordinatorLogDigest(oversized), oversized); err == nil {
		t.Fatal("recordGateLog() accepted oversized evidence")
	}
	if err := store.recordGateLog(context.Background(), record.JobID, gateID, digest, data); err != nil {
		t.Fatal(err)
	}
	conflict := []byte("different failure\n")
	if err := store.recordGateLog(context.Background(), record.JobID, gateID, coordinatorLogDigest(conflict), conflict); err == nil {
		t.Fatal("recordGateLog() overwrote immutable evidence")
	}
}

func TestTerminalStatusErrorPointsToGateLogQuery(t *testing.T) {
	status := jobStatus{
		JobID: "job-test", State: jobStateFailed,
		GateResults: []gatecontract.GateResult{{GateID: string(gatecontract.GateIDBackendTestWithGuard)}},
	}
	err := terminalStatusError(status)
	want := "super-dolphin-gate logs --job job-test --gate backend:test_with_guard"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("terminalStatusError() = %v, want query %q", err, want)
	}
}

func coordinatorLogTestStore(t *testing.T) (*coordinatorStore, coordinatorJobRecord) {
	t.Helper()
	store, err := openCoordinatorStore(context.Background(), coordinatorTestCheckpoint(t))
	if err != nil {
		t.Fatalf("openCoordinatorStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.close() })
	plan := mustTestGatePlan(t, "d")
	record, err := store.createJob(
		context.Background(), "inv-log-test", "job-log-test", t.TempDir(), plan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatalf("createJob() error = %v", err)
	}
	return store, record
}
