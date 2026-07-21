package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestCoordinatorOOMTerminalPersistsVerifiedResourceWitnessAfterRemoval(t *testing.T) {
	store, record := persistOOMResourceWitnessJob(t)
	loaded, err := store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatalf("job() error = %v", err)
	}
	witness, digest := testContainerResourceWitness()
	assertOOMTerminalRecord(t, loaded)
	assertStoredResourceWitness(t, loaded, witness, digest)

	encoded, err := json.Marshal(loaded.status())
	if err != nil {
		t.Fatal(err)
	}
	var transported jobStatus
	if err := decodeCoordinatorJSON(encoded, &transported); err != nil {
		t.Fatalf("decode transported status: %v", err)
	}
	assertTransportedResourceWitness(t, transported, witness, digest)
}

func TestCoordinatorShardStatusAggregatesOnlyCommonVerifiedResourceWitness(t *testing.T) {
	witness, digest := testContainerResourceWitness()
	record := coordinatorJobRecord{ContainerShards: make([]coordinatorShardRecord, 3)}
	for index := range record.ContainerShards {
		copy := witness
		record.ContainerShards[index] = coordinatorShardRecord{
			ContainerHostConfigDigest: coordinatorDigest("5"), ContainerResourceWitness: &copy,
			ContainerResourceWitnessDigest: digest, ContainerResourceWitnessVerified: true,
		}
	}
	status := record.status()
	want := resourceWitnessEvidence{Witness: &witness, Digest: digest, HostDigest: coordinatorDigest("5"), Verified: true}
	got := resourceWitnessEvidence{Witness: status.ContainerResourceWitness, Digest: status.ContainerResourceWitnessDigest,
		HostDigest: status.ContainerHostConfigDigest, Verified: status.ContainerResourceWitnessVerified}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregated shard resource evidence = %+v, want %+v", got, want)
	}

	record.ContainerShards[1].ContainerResourceWitnessVerified = false
	status = record.status()
	if status.ContainerResourceWitnessVerified || status.ContainerResourceWitness != nil ||
		status.ContainerResourceWitnessDigest != "" || status.ContainerHostConfigDigest != "" {
		t.Fatalf("status exposed partially verified shard resource evidence: %+v", status)
	}
	record.ContainerShards[1].ContainerResourceWitnessVerified = true
	record.ContainerShards[1].ContainerResourceWitness.PidsLimit++
	status = record.status()
	if status.ContainerResourceWitnessVerified || status.ContainerResourceWitness != nil {
		t.Fatalf("status aggregated inconsistent shard resource evidence: %+v", status)
	}
}

func assertOOMTerminalRecord(t *testing.T, loaded coordinatorJobRecord) {
	t.Helper()
	want := struct {
		state        jobState
		phase        localci.FreshContainerLifecyclePhase
		removalProof bool
		oomError     bool
	}{jobStateInfraFailed, localci.FreshContainerPhaseRemoved, true, true}
	got := struct {
		state        jobState
		phase        localci.FreshContainerLifecyclePhase
		removalProof bool
		oomError     bool
	}{loaded.State, loaded.ContainerPhase, loaded.RemovalProofDigest != "", strings.Contains(loaded.Error, "OOM-killed")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded terminal job = %+v", loaded)
	}
}

func assertStoredResourceWitness(
	t *testing.T,
	loaded coordinatorJobRecord,
	witness gatecontract.ContainerResourceWitness,
	digest string,
) {
	t.Helper()
	want := resourceWitnessEvidence{Witness: &witness, Digest: digest, HostDigest: coordinatorDigest("5"), Verified: true}
	got := resourceWitnessEvidence{
		Witness: loaded.ContainerResourceWitness, Digest: loaded.ContainerResourceWitnessDigest,
		HostDigest: loaded.ContainerHostConfigDigest, Verified: loaded.ContainerResourceWitnessVerified,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded resource evidence = %+v", got)
	}
}

func assertTransportedResourceWitness(
	t *testing.T,
	transported jobStatus,
	witness gatecontract.ContainerResourceWitness,
	digest string,
) {
	t.Helper()
	want := resourceWitnessEvidence{Witness: &witness, Digest: digest, Verified: true}
	got := resourceWitnessEvidence{
		Witness: transported.ContainerResourceWitness, Digest: transported.ContainerResourceWitnessDigest,
		Verified: transported.ContainerResourceWitnessVerified,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transported resource evidence = %+v", transported)
	}
}

type resourceWitnessEvidence struct {
	Witness    *gatecontract.ContainerResourceWitness
	Digest     string
	HostDigest string
	Verified   bool
}

func TestCoordinatorResourceWitnessTamperingFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "empty witness", query: "UPDATE coordinator_jobs SET container_resource_witness_json = NULL WHERE job_id = ?"},
		{name: "empty digest", query: "UPDATE coordinator_jobs SET container_resource_witness_digest = '' WHERE job_id = ?"},
		{name: "mismatched digest", query: "UPDATE coordinator_jobs SET container_resource_witness_digest = 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE job_id = ?"},
		{name: "empty host digest", query: "UPDATE coordinator_jobs SET container_host_config_digest = '' WHERE job_id = ?"},
		{name: "verification downgrade", query: "UPDATE coordinator_jobs SET container_resource_witness_verified = 0 WHERE job_id = ?"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, record := persistOOMResourceWitnessJob(t)
			if _, err := store.db.Exec(test.query, record.JobID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.job(context.Background(), record.JobID); err == nil {
				t.Fatal("job() accepted tampered resource evidence")
			}
		})
	}
}

func TestCoordinatorResourceWitnessRejectsChangedResourcesWithRecomputedDigest(t *testing.T) {
	store, record := persistOOMResourceWitnessJob(t)
	witness, _ := testContainerResourceWitness()
	witness.PidsLimit++
	digest, err := witness.Digest()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(witness)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE coordinator_jobs SET container_resource_witness_json = ?,
container_resource_witness_digest = ? WHERE job_id = ?`, encoded, digest, record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.job(context.Background(), record.JobID); err == nil {
		t.Fatal("job() accepted changed resources with a recomputed digest")
	}
}

func TestCoordinatorResourceWitnessMigrationLeavesExecutedRowsUnknown(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/migration.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE coordinator_jobs (container_phase TEXT NOT NULL DEFAULT '');
INSERT INTO coordinator_jobs (container_phase) VALUES ('removed')`); err != nil {
		t.Fatal(err)
	}
	if err := ensureCoordinatorContainerEvidenceSchema(context.Background(), db); err != nil {
		t.Fatalf("ensureCoordinatorContainerEvidenceSchema() error = %v", err)
	}
	var verified sql.NullBool
	if err := db.QueryRow("SELECT container_resource_witness_verified FROM coordinator_jobs").Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if verified.Valid {
		t.Fatalf("migrated executed row verification = %v, want unknown fail-closed state", verified.Bool)
	}
}

func TestAggregateContainerEvidenceRequiresOneResourceWitness(t *testing.T) {
	witness, digest := testContainerResourceWitness()
	container := gatecontract.ContainerEvidence{
		ContainerID: "container-1", NetworkID: "network-1",
		HostConfigDigest: coordinatorDigest("5"), ResourceWitness: witness,
		ResourceWitnessDigest: digest, NetworkPolicyDigest: coordinatorDigest("6"),
		Removed: true, NetworkRemoved: true,
	}
	aggregate, err := aggregateContainerEvidence([]gatecontract.ContainerEvidence{container, container})
	if err != nil {
		t.Fatalf("aggregateContainerEvidence() error = %v", err)
	}
	if !reflect.DeepEqual(aggregate.ResourceWitness, witness) || aggregate.ResourceWitnessDigest != digest {
		t.Fatalf("aggregate resource witness = %+v", aggregate)
	}
	tampered := container
	tampered.ResourceWitness.PidsLimit++
	tampered.ResourceWitnessDigest, err = tampered.ResourceWitness.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aggregateContainerEvidence([]gatecontract.ContainerEvidence{container, tampered}); err == nil {
		t.Fatal("aggregateContainerEvidence() accepted inconsistent resource witnesses")
	}
}

func persistOOMResourceWitnessJob(t *testing.T) (*coordinatorStore, coordinatorJobRecord) {
	t.Helper()
	store, record := coordinatorLogTestStore(t)
	ctx := context.Background()
	if err := store.startJob(ctx, record.JobID); err != nil {
		t.Fatalf("startJob() error = %v", err)
	}
	started, err := store.job(ctx, record.JobID)
	if err != nil {
		t.Fatalf("started job() error = %v", err)
	}
	recordOOMContainerLifecycle(t, store, record, started)
	if err := store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil,
		"finished gate container was OOM-killed", nil); err != nil {
		t.Fatalf("finishJob() error = %v", err)
	}
	return store, record
}

func recordOOMContainerLifecycle(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	started coordinatorJobRecord,
) {
	t.Helper()
	witness, digest := testContainerResourceWitness()
	containerID, sourceSnapshotDir := strings.Repeat("e", 64), t.TempDir()
	startedAt := time.Now().UTC()
	deadline := startedAt.Add(coordinatorTimeout(started.Profile))
	started.StartedAt, started.Deadline = &startedAt, &deadline
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated,
		localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted,
		localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemoved,
	} {
		event := oomLifecycleEvent(phase, containerID, sourceSnapshotDir, started, witness, digest)
		if err := store.recordContainerLifecycle(
			context.Background(), record.JobID, record.Plan.Gates[0].ID,
			map[string]string{"job": record.JobID}, event); err != nil {
			t.Fatalf("recordContainerLifecycle(%s) error = %v", phase, err)
		}
	}
}

func oomLifecycleEvent(
	phase localci.FreshContainerLifecyclePhase,
	containerID string,
	sourceSnapshotDir string,
	started coordinatorJobRecord,
	witness gatecontract.ContainerResourceWitness,
	digest string,
) localci.FreshContainerLifecycleEvent {
	event := localci.FreshContainerLifecycleEvent{
		Phase: phase, ContainerID: containerID,
		ImageReference: "test@" + coordinatorDigest("7"), ConfigDigest: coordinatorDigest("8"),
		SourceSnapshotDir: sourceSnapshotDir, StartedAt: *started.StartedAt, Deadline: *started.Deadline,
		ExitCode: 137,
	}
	if phase == localci.FreshContainerPhasePrepared || phase == localci.FreshContainerPhaseCreating {
		event.ContainerID = ""
	}
	if phase != localci.FreshContainerPhasePrepared && phase != localci.FreshContainerPhaseCreating && phase != localci.FreshContainerPhaseCreated {
		event.HostConfigDigest = coordinatorDigest("5")
		event.ResourceWitness, event.ResourceWitnessDigest = witness, digest
	}
	if phase == localci.FreshContainerPhaseExited || phase == localci.FreshContainerPhaseRemoved {
		event.ExitedAt = started.StartedAt.Add(45 * time.Second)
		event.CompletedAt = started.StartedAt.Add(time.Minute)
	}
	if phase == localci.FreshContainerPhaseRemoved {
		event.RemovalProofDigest = containerRemovalProofDigest(containerID)
	}
	return event
}

func testContainerResourceWitness() (gatecontract.ContainerResourceWitness, string) {
	return localci.ExpectedFreshContainerResourceWitness(),
		"sha256:4cac604d08d84d4163bdc9a765889133571e5042ce3bf297291f4d05f83c590a"
}

func emitFakeContainerLifecycle(
	ctx context.Context,
	request freshContainerRequest,
	startedAt time.Time,
) (time.Time, string, error) {
	deadline := request.Deadline.UTC()
	containerID := fakeFreshContainerID(request)
	removalProof := containerRemovalProofDigest(containerID)
	witness, digest := testContainerResourceWitness()
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated,
	} {
		if err := emitFakeContainerLifecycleEvent(ctx, request, phase, containerID, startedAt, deadline, witness, digest); err != nil {
			return time.Time{}, "", err
		}
	}
	if request.ClaimDeadline != nil {
		claimed, err := request.ClaimDeadline(ctx, startedAt)
		if err != nil {
			return time.Time{}, "", err
		}
		deadline = claimed.UTC()
	} else if deadline.IsZero() {
		deadline = startedAt.Add(coordinatorTimeout(request.Profile))
	}
	// Worker observations use the durable group clock after the atomic claim, not each goroutine's local sample.
	startedAt = deadline.Add(-coordinatorTimeout(request.Profile))
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted,
		localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemoved,
	} {
		if err := emitFakeContainerLifecycleEvent(ctx, request, phase, containerID, startedAt, deadline, witness, digest); err != nil {
			return time.Time{}, "", err
		}
	}
	return deadline, removalProof, nil
}

// emitFakeContainerLifecycleEvent 发射 runner 测试使用的一条完整生命周期事件。
func emitFakeContainerLifecycleEvent(
	ctx context.Context,
	request freshContainerRequest,
	phase localci.FreshContainerLifecyclePhase,
	containerID string,
	startedAt time.Time,
	eventDeadline time.Time,
	witness gatecontract.ContainerResourceWitness,
	digest string,
) error {
	event := oomLifecycleEvent(phase, containerID, request.SourceSnapshotDir,
		coordinatorJobRecord{StartedAt: &startedAt, Deadline: &eventDeadline}, witness, digest)
	event.ImageReference = "test@" + request.Image.PlatformManifestDigest
	event.ConfigDigest = request.Image.ConfigDigest
	if phase == localci.FreshContainerPhaseExited || phase == localci.FreshContainerPhaseRemoved {
		event.ExitedAt = startedAt
	}
	event.CompletedAt, event.ExitCode = startedAt, 0
	if request.LifecycleHook != nil {
		return request.LifecycleHook(ctx, event)
	}
	return nil
}

func fakeFreshContainerID(request freshContainerRequest) string {
	if strings.HasPrefix(request.ShardIdentity, "sha256:") && len(request.ShardIdentity) == len("sha256:")+64 {
		return strings.TrimPrefix(request.ShardIdentity, "sha256:")
	}
	return strings.Repeat("a", 64)
}
