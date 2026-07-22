package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestCoordinatorContainerExitedAtSchemaMigratesLegacyJobTable(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/container-exit-migration.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TABLE coordinator_jobs (container_phase TEXT NOT NULL DEFAULT '')"); err != nil {
		t.Fatal(err)
	}
	if err := ensureCoordinatorContainerExitedAtSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	columns, err := coordinatorJobColumns(context.Background(), db)
	if err != nil || !columns["container_exited_at"] {
		t.Fatalf("migrated coordinator columns=%v err=%v", columns, err)
	}
}

func TestCoordinatorContainerExitedAtRoundTripsAcrossRemoved(t *testing.T) {
	store, record, startedAt, deadline := coordinatorLegacyExitTestStore(t)
	exitedAt := startedAt.Add(45 * time.Second)
	persistLegacyLifecycle(t, store, record, startedAt, deadline, localci.FreshContainerPhaseExited)
	persistLegacyLifecycle(t, store, record, startedAt, deadline, localci.FreshContainerPhaseRemoved)
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil || loaded.ContainerExitedAt == nil || !loaded.ContainerExitedAt.Equal(exitedAt) {
		t.Fatalf("loaded container exited_at=%v err=%v", loaded.ContainerExitedAt, err)
	}
}

func TestCoordinatorRemovalPendingJournalPrecedesDurableRemovalProof(t *testing.T) {
	store, record, startedAt, deadline := coordinatorLegacyExitTestStore(t)
	persistLegacyLifecycle(t, store, record, startedAt, deadline, localci.FreshContainerPhaseExited)
	persistLegacyLifecycle(t, store, record, startedAt, deadline, localci.FreshContainerPhaseRemovalPending)
	pending, err := store.job(context.Background(), record.JobID)
	if err != nil || pending.ContainerPhase != localci.FreshContainerPhaseRemovalPending || pending.RemovalProofDigest != "" {
		t.Fatalf("pending removal journal=%#v err=%v", pending, err)
	}
	persistLegacyLifecycle(t, store, record, startedAt, deadline, localci.FreshContainerPhaseRemoved)
	removed, err := store.job(context.Background(), record.JobID)
	if err != nil || removed.ContainerPhase != localci.FreshContainerPhaseRemoved || removed.RemovalProofDigest == "" {
		t.Fatalf("durable removal proof=%#v err=%v", removed, err)
	}
}

func TestCoordinatorContainerExitedAtRejectsMissingAndDriftedTerminalEvents(t *testing.T) {
	t.Run("exited missing", func(t *testing.T) {
		store, record, startedAt, deadline := coordinatorLegacyExitTestStore(t)
		event := legacyLifecycleEvent(startedAt, deadline, localci.FreshContainerPhaseExited)
		event.ExitedAt = time.Time{}
		if err := store.recordContainerLifecycle(context.Background(), record.JobID, record.Plan.Gates[0].ID, legacyLifecycleLabels(record), event); err == nil {
			t.Fatal("exited lifecycle accepted missing exited_at")
		}
	})
	t.Run("exited after completion", func(t *testing.T) {
		store, record, startedAt, deadline := coordinatorLegacyExitTestStore(t)
		event := legacyLifecycleEvent(startedAt, deadline, localci.FreshContainerPhaseExited)
		event.ExitedAt = event.CompletedAt.Add(time.Nanosecond)
		if err := store.recordContainerLifecycle(context.Background(), record.JobID, record.Plan.Gates[0].ID, legacyLifecycleLabels(record), event); err == nil {
			t.Fatal("exited lifecycle accepted inverted clock")
		}
	})
	t.Run("removed drift", func(t *testing.T) {
		store, record, startedAt, deadline := coordinatorLegacyExitTestStore(t)
		persistLegacyLifecycle(t, store, record, startedAt, deadline, localci.FreshContainerPhaseExited)
		event := legacyLifecycleEvent(startedAt, deadline, localci.FreshContainerPhaseRemoved)
		event.ExitedAt = event.ExitedAt.Add(time.Nanosecond)
		if err := store.recordContainerLifecycle(context.Background(), record.JobID, record.Plan.Gates[0].ID, legacyLifecycleLabels(record), event); err == nil {
			t.Fatal("removed lifecycle accepted exited_at drift")
		}
	})
}

func TestCoordinatorContainerRemovedWithoutExitAllowsOnlyZeroTimestamp(t *testing.T) {
	store, record, startedAt, deadline := coordinatorLegacyCreatedExitTestStore(t)
	forged := legacyLifecycleEvent(startedAt, deadline, localci.FreshContainerPhaseRemoved)
	if err := store.recordContainerLifecycle(context.Background(), record.JobID, record.Plan.Gates[0].ID, legacyLifecycleLabels(record), forged); err == nil {
		t.Fatal("unproved removal accepted a forged exited_at")
	}
	cleanup := forged
	cleanup.ExitedAt, cleanup.CompletedAt = time.Time{}, time.Time{}
	if err := store.recordContainerLifecycle(context.Background(), record.JobID, record.Plan.Gates[0].ID, legacyLifecycleLabels(record), cleanup); err != nil {
		t.Fatalf("unproved removal with zero exited_at: %v", err)
	}
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil || loaded.ContainerExitedAt != nil {
		t.Fatalf("unproved removal exited_at=%v err=%v", loaded.ContainerExitedAt, err)
	}
}

func TestRecoveredLegacyResultUsesDurableExitedAt(t *testing.T) {
	exitedAt := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	record := coordinatorJobRecord{ContainerExitedAt: &exitedAt}
	result, err := recoveredLegacyResultWithDurableExit(record, localci.FreshContainerResult{
		Status: gatecontract.ResultStatusTimeout, ExitedAt: exitedAt,
	})
	if err != nil || !result.ExitedAt.Equal(exitedAt) {
		t.Fatalf("recovered result exited_at=%s err=%v", result.ExitedAt, err)
	}
	if _, err := recoveredLegacyResultWithDurableExit(record, localci.FreshContainerResult{
		Status: gatecontract.ResultStatusTimeout, ExitedAt: exitedAt.Add(time.Nanosecond),
	}); err == nil {
		t.Fatal("recovered result accepted exited_at drift")
	}
	if _, err := recoveredLegacyResultWithDurableExit(coordinatorJobRecord{}, localci.FreshContainerResult{
		Status: gatecontract.ResultStatusCancelled,
	}); err == nil {
		t.Fatal("cancelled recovered result accepted a missing durable exited_at")
	}
}

func TestCoordinatorFinishNormalTerminalRejectsMissingExitedAt(t *testing.T) {
	for _, state := range []jobState{jobStateTimeout, jobStateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			store, record, startedAt, deadline := missingExitTerminalStore(t)
			cleanup := missingExitLifecycleEvent(startedAt, deadline, localci.FreshContainerPhaseRemoved)
			if err := store.recordContainerLifecycle(
				context.Background(), record.JobID, record.Plan.Gates[0].ID, legacyLifecycleLabels(record), cleanup,
			); err != nil {
				t.Fatal(err)
			}
			if err := store.finishJob(context.Background(), record.JobID, state, nil, "terminal", nil); err == nil {
				t.Fatal("normal terminal job accepted without durable exited_at")
			}
			loaded, err := store.job(context.Background(), record.JobID)
			if err != nil || loaded.State != jobStateStarted {
				t.Fatalf("failed completion changed job state=%q err=%v", loaded.State, err)
			}
		})
	}
}

func coordinatorLegacyExitTestStore(t *testing.T) (*coordinatorStore, coordinatorJobRecord, time.Time, time.Time) {
	t.Helper()
	store, record, startedAt, deadline := coordinatorLegacyCreatedExitTestStore(t)
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseStarting,
		localci.FreshContainerPhaseStarted,
	} {
		persistLegacyLifecycle(t, store, record, startedAt, deadline, phase)
	}
	return store, record, startedAt, deadline
}

func coordinatorLegacyCreatedExitTestStore(t *testing.T) (*coordinatorStore, coordinatorJobRecord, time.Time, time.Time) {
	t.Helper()
	store, record := coordinatorLogTestStore(t)
	if err := store.startJob(context.Background(), record.JobID); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(coordinatorTimeout(record.Profile))
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated,
	} {
		persistLegacyLifecycle(t, store, record, startedAt, deadline, phase)
	}
	return store, record, startedAt, deadline
}

func persistLegacyLifecycle(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	startedAt, deadline time.Time,
	phase localci.FreshContainerLifecyclePhase,
) {
	t.Helper()
	event := legacyLifecycleEvent(startedAt, deadline, phase)
	labels := legacyLifecycleLabels(record)
	bindCreatingOperationIdentity(t, labels, &event)
	if err := store.recordContainerLifecycle(context.Background(), record.JobID, record.Plan.Gates[0].ID, labels, event); err != nil {
		t.Fatalf("record legacy lifecycle %q: %v", phase, err)
	}
}

func legacyLifecycleEvent(
	startedAt, deadline time.Time,
	phase localci.FreshContainerLifecyclePhase,
) localci.FreshContainerLifecycleEvent {
	witness, witnessDigest := testContainerResourceWitness()
	event := localci.FreshContainerLifecycleEvent{
		Phase: phase, ContainerID: strings.Repeat("a", 64),
		ImageReference: "test@" + coordinatorDigest("a"), ConfigDigest: coordinatorDigest("b"),
		SourceSnapshotDir: "/tmp/coordinator-legacy-exit", StartedAt: startedAt, Deadline: deadline,
		ExitCode: 0,
	}
	if phase == localci.FreshContainerPhasePrepared || phase == localci.FreshContainerPhaseCreating {
		event.ContainerID = ""
	}
	if phase != localci.FreshContainerPhasePrepared && phase != localci.FreshContainerPhaseCreating && phase != localci.FreshContainerPhaseCreated {
		event.HostConfigDigest, event.ResourceWitness, event.ResourceWitnessDigest = coordinatorDigest("c"), witness, witnessDigest
	}
	if phase == localci.FreshContainerPhaseExited || phase == localci.FreshContainerPhaseRemovalPending || phase == localci.FreshContainerPhaseRemoved {
		event.ExitedAt, event.CompletedAt = startedAt.Add(45*time.Second), startedAt.Add(time.Minute)
	}
	if phase == localci.FreshContainerPhaseRemoved {
		event.RemovalProofDigest = coordinatorDigest("d")
	}
	return event
}

func legacyLifecycleLabels(record coordinatorJobRecord) map[string]string {
	return map[string]string{"job": record.JobID}
}

func bindCreatingOperationIdentity(
	t *testing.T,
	labels map[string]string,
	event *localci.FreshContainerLifecycleEvent,
) {
	t.Helper()
	if event.Phase != localci.FreshContainerPhaseCreating {
		return
	}
	identity, err := localci.FreshContainerOperationIdentity(labels)
	if err != nil {
		t.Fatalf("derive creating operation identity: %v", err)
	}
	event.ContainerID = identity
}

func missingExitTerminalStore(t *testing.T) (*coordinatorStore, coordinatorJobRecord, time.Time, time.Time) {
	t.Helper()
	store, record := coordinatorLogTestStore(t)
	if err := store.startJob(context.Background(), record.JobID); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(coordinatorTimeout(record.Profile))
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating,
		localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting,
		localci.FreshContainerPhaseStarted,
	} {
		event := missingExitLifecycleEvent(startedAt, deadline, phase)
		labels := legacyLifecycleLabels(record)
		bindCreatingOperationIdentity(t, labels, &event)
		if err := store.recordContainerLifecycle(
			context.Background(), record.JobID, record.Plan.Gates[0].ID, labels, event,
		); err != nil {
			t.Fatalf("record missing-exit lifecycle %q: %v", phase, err)
		}
	}
	return store, record, startedAt, deadline
}

func missingExitLifecycleEvent(
	startedAt, deadline time.Time,
	phase localci.FreshContainerLifecyclePhase,
) localci.FreshContainerLifecycleEvent {
	witness, witnessDigest := testContainerResourceWitness()
	event := localci.FreshContainerLifecycleEvent{
		Phase: phase, ContainerID: strings.Repeat("a", 64),
		ImageReference: "test@" + coordinatorDigest("a"), ConfigDigest: coordinatorDigest("b"),
		SourceSnapshotDir: "/tmp/coordinator-missing-exit", StartedAt: startedAt, Deadline: deadline,
	}
	if phase == localci.FreshContainerPhasePrepared || phase == localci.FreshContainerPhaseCreating {
		event.ContainerID = ""
	}
	if phase == localci.FreshContainerPhaseStarting || phase == localci.FreshContainerPhaseStarted ||
		phase == localci.FreshContainerPhaseExited || phase == localci.FreshContainerPhaseRemoved {
		event.HostConfigDigest = coordinatorDigest("c")
		event.ResourceWitness, event.ResourceWitnessDigest = witness, witnessDigest
	}
	if phase == localci.FreshContainerPhaseRemoved {
		event.RemovalProofDigest = coordinatorDigest("d")
	}
	return event
}
