package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestCoordinatorShardSetCreationIsAtomicAndIdempotent(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	if err := store.createContainerShardSet(context.Background(), record.JobID, set); err != nil {
		t.Fatalf("exact shard set replay failed: %v", err)
	}
	drifted, err := gatecontract.BuildContainerShardSet(record.Plan, coordinatorDigest("8"), coordinatorDigest("9"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.createContainerShardSet(context.Background(), record.JobID, drifted); err == nil {
		t.Fatal("conflicting shard set replay was accepted")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM coordinator_job_shards WHERE job_id = ?", record.JobID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(set.Shards) {
		t.Fatalf("persisted shard count = %d, want %d", count, len(set.Shards))
	}
}

func TestCoordinatorShardStorePersistsExactLifecycleEvidence(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	startedAt := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(coordinatorTimeout(record.Profile))
	witness, witnessDigest := testContainerResourceWitness()
	persistExactShardLifecycleEvidence(t, store, record, set, startedAt, deadline, witness, witnessDigest)
	assertExactShardLifecycleEvidence(t, store, record, set, startedAt, deadline, witnessDigest)
}

func TestCoordinatorShardNormalTerminalRequiresEveryExitedAt(t *testing.T) {
	t.Run("missing shard exit rejects atomic completion and recovery load", testCoordinatorShardCompletionRejectsMissingExitedAt)
	t.Run("all shard exits allow atomic completion", testCoordinatorShardCompletionAcceptsCompleteExitedAt)
}

func testCoordinatorShardCompletionRejectsMissingExitedAt(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	persistCoordinatorShardLifecycles(t, store, record, set)
	if _, err := store.db.Exec(`UPDATE coordinator_job_shards
SET exited_at = NULL, completed_at = NULL, exit_code = NULL
WHERE job_id = ? AND shard_index = ?`, record.JobID, 1); err != nil {
		t.Fatal(err)
	}
	result, err := store.persistCoordinatorCompletion(
		context.Background(), record.JobID, jobStateTimeout, nil, time.Now().UTC(), nil, nil, "terminal",
	)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 0 {
		t.Fatalf("missing shard exit completion affected=%d err=%v", affected, err)
	}
	if _, err := store.db.Exec("UPDATE coordinator_jobs SET state = ? WHERE job_id = ?", jobStateTimeout, record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.job(context.Background(), record.JobID); err == nil {
		t.Fatal("normal terminal shard job accepted a missing exited_at")
	}
}

func testCoordinatorShardCompletionAcceptsCompleteExitedAt(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	persistCoordinatorShardLifecycles(t, store, record, set)
	result, err := store.persistCoordinatorCompletion(
		context.Background(), record.JobID, jobStateTimeout, nil, time.Now().UTC(), nil, nil, "terminal",
	)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("complete shard exits affected=%d err=%v", affected, err)
	}
	if _, err := store.job(context.Background(), record.JobID); err != nil {
		t.Fatalf("normal terminal shard job rejected after complete exits: %v", err)
	}
}

func persistExactShardLifecycleEvidence(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
	startedAt, deadline time.Time,
	witness gatecontract.ContainerResourceWitness,
	witnessDigest string,
) {
	t.Helper()
	for index, shard := range set.Shards {
		shardStart := startedAt.Add(time.Duration(index) * time.Second)
		sourceSnapshotDir := t.TempDir()
		labels := coordinatorShardTestLabels(record.JobID, shard)
		for _, phase := range []localci.FreshContainerLifecyclePhase{
			localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating,
			localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting,
			localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited,
			localci.FreshContainerPhaseRemoved,
		} {
			event := coordinatorShardLifecycleEvent(shard, phase, shardStart, deadline, sourceSnapshotDir, witness, witnessDigest)
			if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest,
				labels, event); err != nil {
				t.Fatalf("recordContainerShardLifecycle(%d, %s): %v", index, phase, err)
			}
		}
	}
}

func assertExactShardLifecycleEvidence(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
	startedAt, deadline time.Time,
	witnessDigest string,
) {
	t.Helper()
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertLoadedShardInvocationClock(t, loaded, set, startedAt, deadline)
	for index, shard := range loaded.ContainerShards {
		assertLoadedShardEvidence(t, shard, set.Shards[index], index, startedAt.Add(time.Duration(index)*time.Second), witnessDigest)
	}
}

func assertLoadedShardInvocationClock(
	t *testing.T,
	loaded coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
	startedAt, deadline time.Time,
) {
	t.Helper()
	if len(loaded.ContainerShards) != len(set.Shards) || loaded.StartedAt == nil || !loaded.StartedAt.Equal(startedAt) ||
		loaded.Deadline == nil || !loaded.Deadline.Equal(deadline) {
		t.Fatalf("loaded shard invocation clock/set = %+v", loaded)
	}
}

func assertLoadedShardEvidence(
	t *testing.T,
	shard coordinatorShardRecord,
	want gatecontract.ContainerShard,
	index int,
	startedAt time.Time,
	witnessDigest string,
) {
	t.Helper()
	if !reflect.DeepEqual(shard.Shard, want) {
		t.Fatalf("loaded shard %d evidence = %+v", index, shard)
	}
	assertLoadedShardLifecycleEvidence(t, shard, index, startedAt, witnessDigest)
}

// assertLoadedShardLifecycleEvidence 验证已持久化分片的终态与资源见证。
func assertLoadedShardLifecycleEvidence(
	t *testing.T,
	shard coordinatorShardRecord,
	index int,
	startedAt time.Time,
	witnessDigest string,
) {
	t.Helper()
	if shard.ContainerPhase != localci.FreshContainerPhaseRemoved ||
		shard.ExitedAt == nil || !shard.ExitedAt.Equal(startedAt.Add(45*time.Second)) || shard.CompletedAt == nil ||
		shard.ExitCode == nil || *shard.ExitCode != 0 || shard.RemovalProofDigest == "" ||
		!shard.ContainerResourceWitnessVerified || shard.ContainerResourceWitnessDigest != witnessDigest {
		t.Fatalf("loaded shard %d evidence = %+v", index, shard)
	}
}

func TestCoordinatorShardRecoveryRejectsLifecycleEvidenceTampering(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "deadline", query: "UPDATE coordinator_job_shards SET deadline_at = '2026-07-19T08:29:59Z' WHERE shard_index = 1"},
		{name: "resource witness", query: "UPDATE coordinator_job_shards SET container_resource_witness_digest = 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE shard_index = 0"},
		{name: "source snapshot", query: "UPDATE coordinator_job_shards SET source_snapshot_dir = '' WHERE shard_index = 0"},
		{name: "removal proof", query: "UPDATE coordinator_job_shards SET removal_proof_digest = '' WHERE shard_index = 0"},
		{name: "completion", query: "UPDATE coordinator_job_shards SET completed_at = '2026-07-19T07:59:59Z' WHERE shard_index = 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, record, set := coordinatorShardTestStore(t)
			persistCoordinatorShardLifecycles(t, store, record, set)
			if _, err := store.db.Exec(test.query); err != nil {
				t.Fatal(err)
			}
			if _, err := store.job(context.Background(), record.JobID); err == nil {
				t.Fatal("tampered shard lifecycle evidence was accepted")
			}
		})
	}
}

func TestCoordinatorStartedShardRecoveryBlocksWithoutShardRunnerWiring(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	persistCoordinatorShardLifecycles(t, store, record, set)
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	owner := coordinatorOwner{}
	if recovered, err := owner.reconcileStartedRecord(context.Background(), &loaded, localci.WorkloadStatusStarted); err == nil || recovered {
		t.Fatalf("reconcileStartedRecord() recovered=%v err=%v", recovered, err)
	}
}

func TestShardAdmissionRecoveryReplaysStartedPrepCrashWindowOnly(t *testing.T) {
	fixture := newShardAdmissionRecoveryFixture(t)
	t.Run("queued group without shard lifecycle replays", func(t *testing.T) {
		assertShardAdmissionRecovery(t, fixture, fixture.record, fixture.snapshot(localci.WorkloadStatusQueued), localci.WorkloadStatusQueued, true)
	})
	t.Run("missing group is restored queued", func(t *testing.T) {
		assertShardAdmissionRecovery(t, fixture, fixture.record, nil, localci.WorkloadStatusQueued, true)
	})
	t.Run("started group resumes without requeue", func(t *testing.T) {
		assertShardAdmissionRecovery(t, fixture, fixture.record, fixture.snapshot(localci.WorkloadStatusStarted), localci.WorkloadStatusStarted, true)
	})
	t.Run("cancelling group preserves barrier", func(t *testing.T) {
		assertShardAdmissionRecovery(t, fixture, fixture.record, fixture.snapshot(localci.WorkloadStatusCancelling), localci.WorkloadStatusCancelling, true)
	})
	t.Run("terminal group restores matching terminal", func(t *testing.T) {
		terminal := fixture.record
		terminal.State = jobStateInfraFailed
		assertShardAdmissionRecovery(t, fixture, terminal, fixture.snapshot(localci.WorkloadStatusInfraFailed), localci.WorkloadStatusInfraFailed, false)
	})
	t.Run("observable shard is fail closed", func(t *testing.T) {
		assertObservableShardAdmissionFailsClosed(t, fixture)
	})
}

type shardAdmissionRecoveryFixture struct {
	ctx       context.Context
	store     *coordinatorStore
	record    coordinatorJobRecord
	set       gatecontract.ContainerShardSet
	admission coordinatorShardAdmission
	request   localci.WorkloadRequest
	owner     *coordinatorOwner
}

func newShardAdmissionRecoveryFixture(t *testing.T) *shardAdmissionRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	store, record, set := coordinatorShardTestStore(t)
	admission, err := store.prepareShardAdmission(ctx, record, set)
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.job(ctx, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	_, request, err := shardAdmissionForSet(record, set)
	if err != nil {
		t.Fatal(err)
	}
	return &shardAdmissionRecoveryFixture{ctx: ctx, store: store, record: record, set: set,
		admission: admission, request: request, owner: &coordinatorOwner{store: store}}
}

func (fixture *shardAdmissionRecoveryFixture) snapshot(status localci.WorkloadStatus) map[string]localci.WorkloadSnapshot {
	return map[string]localci.WorkloadSnapshot{
		fixture.admission.WorkloadID: {Request: fixture.request, Status: status},
	}
}

func assertShardAdmissionRecovery(
	t *testing.T,
	fixture *shardAdmissionRecoveryFixture,
	record coordinatorJobRecord,
	snapshot map[string]localci.WorkloadSnapshot,
	wantStatus localci.WorkloadStatus,
	wantResume bool,
) {
	t.Helper()
	workloads, resume, err := fixture.owner.shardAdmissionRecoveryWorkloads(
		fixture.ctx, record, fixture.admission, snapshot,
	)
	if err != nil || resume != wantResume || len(workloads) != 2 {
		t.Fatalf("shard recovery workloads=%+v resume=%v err=%v", workloads, resume, err)
	}
	if workloads[0].Status != localci.WorkloadStatusPassed || workloads[1].Request.ID != fixture.admission.WorkloadID ||
		workloads[1].Status != wantStatus {
		t.Fatalf("shard recovery workload identity/status drifted: %+v", workloads)
	}
}

func assertObservableShardAdmissionFailsClosed(t *testing.T, fixture *shardAdmissionRecoveryFixture) {
	t.Helper()
	shard := fixture.set.Shards[0]
	event := coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhasePrepared,
		time.Now().UTC(), time.Now().UTC().Add(time.Minute), t.TempDir(), gatecontract.ContainerResourceWitness{}, "")
	if err := fixture.store.recordContainerShardLifecycle(fixture.ctx, fixture.record.JobID, shard.IdentityDigest,
		coordinatorShardTestLabels(fixture.record.JobID, shard), event); err != nil {
		t.Fatal(err)
	}
	observed, err := fixture.store.job(fixture.ctx, fixture.record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.owner.shardAdmissionRecoveryWorkloads(
		fixture.ctx, observed, fixture.admission, fixture.snapshot(localci.WorkloadStatusQueued),
	); err == nil {
		t.Fatal("observable shard lifecycle was silently requeued")
	}
}

func TestShardFailureBarrierIdentityIsStableWhenShardTwoFailsFirst(t *testing.T) {
	_, record, set := coordinatorShardTestStore(t)
	admission, _, err := shardAdmissionForSet(record, set)
	if err != nil {
		t.Fatal(err)
	}
	// The worker may observe shard 2 first, but cancellation has one durable group identity.
	if set.Shards[2].IdentityDigest == admission.ShardIdentities[0] {
		t.Fatal("fixture did not select a non-primary failing shard")
	}
	got, err := shardFailureBarrierIdentity(admission)
	if err != nil || got != admission.ShardIdentities[0] {
		t.Fatalf("failure barrier identity=%q err=%v, want durable primary %q", got, err, admission.ShardIdentities[0])
	}
}

func TestCoordinatorShardRecoveryRejectsTamperedOrMixedExactSet(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "missing shard", query: "DELETE FROM coordinator_job_shards WHERE shard_index = 1"},
		{name: "identity key", query: "UPDATE coordinator_job_shards SET shard_identity_digest = 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE shard_index = 0"},
		{name: "source", query: "UPDATE coordinator_job_shards SET shard_json = replace(shard_json, 'dddddddddddddddddddddddddddddddddddddddd', 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee') WHERE shard_index = 0"},
		{name: "mixed legacy", query: "UPDATE coordinator_jobs SET container_phase = 'prepared'"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, record, _ := coordinatorShardTestStore(t)
			if _, err := store.db.Exec(test.query); err != nil {
				t.Fatal(err)
			}
			if _, err := store.job(context.Background(), record.JobID); err == nil {
				t.Fatal("tampered coordinator shard recovery record was accepted")
			}
		})
	}
}

func TestCoordinatorShardSchemaCoversDurableEvidenceAndLeavesLegacyRowsUnmigrated(t *testing.T) {
	store, record := coordinatorLogTestStore(t)
	rows, err := store.db.Query("PRAGMA table_info(coordinator_job_shards)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	for required := range strings.SplitSeq(strings.ReplaceAll(coordinatorShardSelectColumns, "\n", " "), ",") {
		name := strings.TrimSpace(required)
		if !columns[name] {
			t.Fatalf("coordinator shard schema missing %q", name)
		}
	}
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ContainerShards) != 0 {
		t.Fatal("legacy single-container row was silently backfilled into shard protocol")
	}
}

func TestCoordinatorShardSchemaMigratesExitedAtColumn(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/shard-exit-migration.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(coordinatorStoreSchema); err != nil {
		t.Fatal(err)
	}
	legacySchema := strings.Replace(coordinatorShardStoreSchema, "  exited_at TEXT,\n", "", 1)
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if err := ensureCoordinatorShardSchema(context.Background(), db); err != nil {
		t.Fatalf("migrate shard exited_at: %v", err)
	}
	columns, err := coordinatorShardColumns(context.Background(), db)
	if err != nil || !columns["exited_at"] {
		t.Fatalf("migrated shard columns=%v err=%v", columns, err)
	}
}

func TestCoordinatorShardSchemaRejectsIncompleteExistingTable(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/incomplete-shards.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(coordinatorStoreSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE coordinator_job_shards (job_id TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if err := ensureCoordinatorShardSchema(context.Background(), db); err == nil {
		t.Fatal("incomplete coordinator shard schema was accepted")
	}
}

func coordinatorShardTestStore(t *testing.T) (*coordinatorStore, coordinatorJobRecord, gatecontract.ContainerShardSet) {
	t.Helper()
	store, record := coordinatorLogTestStore(t)
	ctx := context.Background()
	if err := store.startJob(ctx, record.JobID); err != nil {
		t.Fatal(err)
	}
	if err := store.recordImageProvenance(ctx, record.JobID, record.JobSourceTreeSHA); err != nil {
		t.Fatal(err)
	}
	set, err := gatecontract.BuildContainerShardSet(record.Plan, coordinatorDigest("a"), coordinatorDigest("b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.createContainerShardSet(ctx, record.JobID, set); err != nil {
		t.Fatal(err)
	}
	return store, record, set
}

func coordinatorShardLifecycleEvent(
	shard gatecontract.ContainerShard,
	phase localci.FreshContainerLifecyclePhase,
	startedAt time.Time,
	deadline time.Time,
	sourceSnapshotDir string,
	witness gatecontract.ContainerResourceWitness,
	witnessDigest string,
) localci.FreshContainerLifecycleEvent {
	containerID := strings.Repeat(string(rune('1'+shard.Index)), 64)
	event := localci.FreshContainerLifecycleEvent{
		Phase: phase, ContainerID: containerID,
		ImageReference: "test@" + shard.AcceptedManifestDigest, ConfigDigest: shard.AcceptedConfigDigest,
		SourceSnapshotDir: sourceSnapshotDir, StartedAt: startedAt, Deadline: deadline,
	}
	if slices.Contains([]localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating,
	}, phase) {
		event.ContainerID = ""
	}
	if slices.Contains([]localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted,
		localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemovalPending,
		localci.FreshContainerPhaseRemoved,
	}, phase) {
		event.HostConfigDigest = coordinatorDigest("c")
		event.ResourceWitness, event.ResourceWitnessDigest = witness, witnessDigest
	}
	if slices.Contains([]localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemovalPending,
		localci.FreshContainerPhaseRemoved,
	}, phase) {
		event.ExitedAt = startedAt.Add(45 * time.Second)
		event.CompletedAt = startedAt.Add(time.Minute)
		event.ExitCode = 0
	}
	if phase == localci.FreshContainerPhaseRemoved {
		event.RemovalProofDigest = coordinatorDigest("f")
	}
	return event
}

func TestCoordinatorShardLifecycleRejectsInvalidExitedAt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*localci.FreshContainerLifecycleEvent, time.Time)
	}{
		{name: "missing", mutate: func(event *localci.FreshContainerLifecycleEvent, _ time.Time) { event.ExitedAt = time.Time{} }},
		{name: "before start", mutate: func(event *localci.FreshContainerLifecycleEvent, started time.Time) {
			event.ExitedAt = started.Add(-time.Nanosecond)
		}},
		{name: "after completion", mutate: func(event *localci.FreshContainerLifecycleEvent, _ time.Time) {
			event.ExitedAt = event.CompletedAt.Add(time.Nanosecond)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, record, set := coordinatorShardTestStore(t)
			shard, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest := prepareStartedShard(t, store, record, set)
			event := coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhaseExited, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest)
			test.mutate(&event, startedAt)
			if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, coordinatorShardTestLabels(record.JobID, shard), event); err == nil {
				t.Fatal("invalid exited_at was accepted")
			}
		})
	}
	t.Run("removed drift", func(t *testing.T) {
		store, record, set := coordinatorShardTestStore(t)
		shard, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest := prepareStartedShard(t, store, record, set)
		labels := coordinatorShardTestLabels(record.JobID, shard)
		exited := coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhaseExited, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest)
		if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, exited); err != nil {
			t.Fatal(err)
		}
		removed := coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhaseRemoved, startedAt, deadline, exited.SourceSnapshotDir, witness, witnessDigest)
		removed.ExitedAt = removed.ExitedAt.Add(time.Nanosecond)
		if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, removed); err == nil {
			t.Fatal("removed lifecycle exit timestamp drift was accepted")
		}
	})
}

func TestCoordinatorShardLifecycleReplaysIdenticalRemovedEvent(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	shard, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest := prepareStartedShard(t, store, record, set)
	labels := coordinatorShardTestLabels(record.JobID, shard)
	exited := coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhaseExited, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest)
	if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, exited); err != nil {
		t.Fatal(err)
	}
	removed := coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhaseRemoved, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest)
	if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, removed); err != nil {
		t.Fatal(err)
	}
	if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, removed); err != nil {
		t.Fatalf("identical removed lifecycle replay failed: %v", err)
	}
	removed.RemovalProofDigest = coordinatorDigest("drifted")
	if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, removed); err == nil {
		t.Fatal("removed lifecycle proof drift replay was accepted")
	}
}

func TestCoordinatorShardRemovalPendingPreservesExitEvidenceForRemoved(t *testing.T) {
	store, record, set := coordinatorShardTestStore(t)
	shard, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest := prepareStartedShard(t, store, record, set)
	labels := coordinatorShardTestLabels(record.JobID, shard)
	pending := coordinatorShardLifecycleEvent(
		shard, localci.FreshContainerPhaseRemovalPending,
		startedAt, deadline, sourceSnapshotDir, witness, witnessDigest,
	)
	if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, pending); err != nil {
		t.Fatalf("persist removal_pending lifecycle: %v", err)
	}
	removed := coordinatorShardLifecycleEvent(
		shard, localci.FreshContainerPhaseRemoved,
		startedAt, deadline, sourceSnapshotDir, witness, witnessDigest,
	)
	if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, removed); err != nil {
		t.Fatalf("persist removed lifecycle after removal_pending: %v", err)
	}
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertLoadedShardLifecycleEvidence(t, loaded.ContainerShards[0], 0, startedAt, witnessDigest)
}

func prepareStartedShard(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
) (gatecontract.ContainerShard, time.Time, time.Time, string, gatecontract.ContainerResourceWitness, string) {
	t.Helper()
	startedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(coordinatorTimeout(record.Profile))
	witness, witnessDigest := testContainerResourceWitness()
	shard := set.Shards[0]
	labels := coordinatorShardTestLabels(record.JobID, shard)
	sourceSnapshotDir := t.TempDir()
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated,
		localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted,
	} {
		event := coordinatorShardLifecycleEvent(shard, phase, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest)
		if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, event); err != nil {
			t.Fatalf("prepare started shard phase %q: %v", phase, err)
		}
	}
	return shard, startedAt, deadline, sourceSnapshotDir, witness, witnessDigest
}

func TestRecoveredShardReceiptUsesDurableExitedAt(t *testing.T) {
	exitedAt := time.Date(2026, 7, 20, 12, 45, 0, 0, time.UTC)
	shard := coordinatorShardRecord{ExitedAt: &exitedAt}
	receipt, err := recoveredShardReceipt(shard, localci.FreshContainerResult{ExitedAt: exitedAt})
	if err != nil || !receipt.ExitedAt.Equal(exitedAt) {
		t.Fatalf("recovered receipt exited_at=%s err=%v", receipt.ExitedAt, err)
	}
	if _, err := recoveredShardReceipt(shard, localci.FreshContainerResult{ExitedAt: exitedAt.Add(time.Nanosecond)}); err == nil {
		t.Fatal("recovered receipt accepted exit timestamp drift")
	}
}

func coordinatorShardTestLabels(jobID string, shard gatecontract.ContainerShard) map[string]string {
	return map[string]string{
		coordinatorLabelJobID: jobID, coordinatorLabelShardIdentity: shard.IdentityDigest,
		coordinatorLabelShardIndex: strconv.Itoa(int(shard.Index)), coordinatorLabelPlanDigest: shard.PlanDigest,
		coordinatorLabelJobSource: shard.SourceTreeSHA, coordinatorLabelImageConfig: shard.AcceptedConfigDigest,
		coordinatorLabelImageManifest: shard.AcceptedManifestDigest,
	}
}

func persistCoordinatorShardLifecycles(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
) {
	t.Helper()
	startedAt := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(coordinatorTimeout(record.Profile))
	witness, witnessDigest := testContainerResourceWitness()
	for index, shard := range set.Shards {
		shardStart := startedAt.Add(time.Duration(index) * time.Second)
		sourceSnapshotDir := t.TempDir()
		labels := coordinatorShardTestLabels(record.JobID, shard)
		for _, phase := range []localci.FreshContainerLifecyclePhase{
			localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating,
			localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting,
			localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited,
			localci.FreshContainerPhaseRemoved,
		} {
			event := coordinatorShardLifecycleEvent(shard, phase, shardStart, deadline, sourceSnapshotDir, witness, witnessDigest)
			if err := store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, event); err != nil {
				t.Fatalf("recordContainerShardLifecycle(%d, %s): %v", index, phase, err)
			}
		}
	}
}

func TestStateForShardRunFailureIsPermutationIndependent(t *testing.T) {
	assertShardFailureStatusPermutations(t, []gatecontract.ResultStatus{
		gatecontract.ResultStatusCancelled,
		gatecontract.ResultStatusFailed,
		gatecontract.ResultStatusInfraFailed,
		gatecontract.ResultStatusTimeout,
	}, jobStateTimeout)
	assertShardFailureStatusPermutations(t, []gatecontract.ResultStatus{
		gatecontract.ResultStatusCancelled,
		gatecontract.ResultStatusFailed,
		gatecontract.ResultStatusInfraFailed,
	}, jobStateInfraFailed)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := stateForShardRunFailure(ctx, []gatecontract.ContainerShardReceipt{{Status: gatecontract.ResultStatusFailed}}); got != jobStateFailed {
		t.Fatalf("worker failure lost to outer cancellation: got=%q", got)
	}
}

func TestShardReceiptPreservesVerifiedExitTimeForTimeoutState(t *testing.T) {
	startedAt := time.Date(2026, 7, 20, 19, 10, 23, 849521000, time.UTC)
	deadline := startedAt.Add(10 * time.Minute)
	exitedAt := deadline.Add(100 * time.Millisecond)
	completedAt := deadline.Add(431666 * time.Microsecond)
	receipt := shardReceipt(gatecontract.ContainerShard{}, localci.FreshContainerResult{
		Status: gatecontract.ResultStatusTimeout, StartedAt: startedAt, ExitedAt: exitedAt, CompletedAt: completedAt, Deadline: deadline,
	})
	if !receipt.ExitedAt.Equal(exitedAt) || !receipt.CompletedAt.Equal(completedAt) {
		t.Fatalf("receipt timeline=%+v", receipt)
	}
	if got := stateForShardRunFailure(context.Background(), []gatecontract.ContainerShardReceipt{receipt}); got != jobStateTimeout {
		t.Fatalf("timeout receipt state=%q", got)
	}
	if got := gateStatusForResult(gatecontract.ResultStatusTimeout); got != gatecontract.GateStatusTimeout {
		t.Fatalf("timeout gate status=%q", got)
	}
}

func assertShardFailureStatusPermutations(t *testing.T, statuses []gatecontract.ResultStatus, want jobState) {
	t.Helper()
	var visit func(int)
	visit = func(index int) {
		if index == len(statuses) {
			receipts := make([]gatecontract.ContainerShardReceipt, len(statuses))
			for receiptIndex, status := range statuses {
				receipts[receiptIndex].Status = status
			}
			if got := stateForShardRunFailure(context.Background(), receipts); got != want {
				t.Fatalf("statuses=%v got=%q want=%q", statuses, got, want)
			}
			return
		}
		for swapIndex := index; swapIndex < len(statuses); swapIndex++ {
			statuses[index], statuses[swapIndex] = statuses[swapIndex], statuses[index]
			visit(index + 1)
			statuses[index], statuses[swapIndex] = statuses[swapIndex], statuses[index]
		}
	}
	visit(0)
}

func TestCoordinatorShardJSONRejectsUnknownFields(t *testing.T) {
	store, record, _ := coordinatorShardTestStore(t)
	var encoded []byte
	if err := store.db.QueryRow("SELECT shard_json FROM coordinator_job_shards WHERE shard_index = 0").Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE coordinator_job_shards SET shard_json = ? WHERE shard_index = 0", encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := store.job(context.Background(), record.JobID); err == nil {
		t.Fatal("unknown persisted shard field was accepted")
	}
}
