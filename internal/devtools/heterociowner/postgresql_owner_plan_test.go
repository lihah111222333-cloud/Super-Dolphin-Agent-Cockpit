package heterociowner

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestPostgreSQLOwnerPlanAndReserveJournalReplay(t *testing.T) {
	var _ heteroci.TopologySessionMutator = (*PostgreSQLOwner)(nil)
	database, state := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	request := postgresqlPlanRequestFixture(t, fence, now)
	state.mu.Lock()
	state.commitAppliedError = errors.New("network dropped after COMMIT")
	state.mu.Unlock()
	first, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve with commit reconciliation: %v", err)
	}
	replay, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve replay: %v", err)
	}
	if !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay drifted:\n got %#v\nwant %#v", replay, first)
	}
	drifted := request
	drifted.Units = append([]heteroci.ExecutionUnit(nil), request.Units...)
	drifted.Units[0].EstimatedDurationMS++
	if _, err := owner.PlanAndReserve(context.Background(), session, drifted); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("operation drift error = %v, want ErrOperationConflict", err)
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != first.Fence {
		t.Fatalf("CurrentFence = %#v, %v; want %#v", current, err, first.Fence)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	assertPostgreSQLOwnerTransactionEvidence(t, state, 4)
}

func TestPostgreSQLOwnerPlanSeparatesSameCallerAcrossRepositories(t *testing.T) {
	harness := newPostgreSQLPlanHarness(t)
	firstRequest, first := harness.plan(t, "repository-a", strings.Repeat("a", 40), "shared-run")
	secondRequest, second := harness.plan(t, "repository-b", strings.Repeat("b", 40), "shared-run")
	assertPostgreSQLPlanIdentitiesSeparate(t, firstRequest, first, secondRequest, second)
}

type postgresqlPlanHarness struct {
	owner   *PostgreSQLOwner
	fence   heteroci.OwnerFence
	session heteroci.TopologySession
	now     time.Time
}

func newPostgreSQLPlanHarness(t *testing.T) postgresqlPlanHarness {
	t.Helper()
	database, _ := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	return postgresqlPlanHarness{owner: owner, fence: fence, session: session, now: now}
}

func (harness postgresqlPlanHarness) plan(t *testing.T, repositoryID, treeSHA, callerRunID string) (heteroci.TopologyPlanRequest, heteroci.TopologyReservedPlan) {
	t.Helper()
	request := postgresqlPlanRequestFixture(t, harness.fence, harness.now)
	request.RunIdentity = topologyRunIdentity(t, repositoryID, treeSHA, callerRunID)
	request.OperationID = topologyPlanOperationID(t, request.RunIdentity.DurableRunID)
	result, err := harness.owner.PlanAndReserve(context.Background(), harness.session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve %s: %v", repositoryID, err)
	}
	return request, result
}

func assertPostgreSQLPlanIdentitiesSeparate(t *testing.T, firstRequest heteroci.TopologyPlanRequest, first heteroci.TopologyReservedPlan, secondRequest heteroci.TopologyPlanRequest, second heteroci.TopologyReservedPlan) {
	t.Helper()
	if firstRequest.RunIdentity.DurableRunID == secondRequest.RunIdentity.DurableRunID || first.OperationID == second.OperationID {
		t.Fatalf("PostgreSQL plans collided: first=%#v second=%#v", first, second)
	}
	if len(first.Leases) != 1 || len(second.Leases) != 1 || first.Leases[0].RunID != string(firstRequest.RunIdentity.DurableRunID) || second.Leases[0].RunID != string(secondRequest.RunIdentity.DurableRunID) {
		t.Fatalf("PostgreSQL plan run bindings drifted: first=%#v second=%#v", first.Leases, second.Leases)
	}
}

func postgresqlPlanRequestFixture(t *testing.T, fence heteroci.OwnerFence, now time.Time) heteroci.TopologyPlanRequest {
	t.Helper()
	manifest := durableRoutingManifest()
	runIdentity, err := heteroci.DeriveTopologyRunIdentity("repository-a", strings.Repeat("b", 40), "run-a")
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity: %v", err)
	}
	operationID := topologyPlanOperationID(t, runIdentity.DurableRunID)
	topologyManifestDigest, err := heteroci.DeriveTopologyManifestDigest(manifest)
	if err != nil {
		t.Fatalf("DeriveTopologyManifestDigest: %v", err)
	}
	return heteroci.TopologyPlanRequest{
		OperationID: operationID, ExternalOperationID: heteroci.OperationID(strings.Repeat("c", 64)), RequestDigest: digestBytes([]byte("topology-request-" + string(runIdentity.DurableRunID))), ManifestDigest: "sha256:" + strings.Repeat("9", 64), ExternalBindingDigest: "sha256:" + strings.Repeat("8", 64), ManifestBlobSHA256: "sha256:" + strings.Repeat("7", 64), RuntimePolicyDigest: "sha256:" + strings.Repeat("6", 64), TopologyManifestDigest: topologyManifestDigest, RunIdentity: runIdentity,
		Manifest: manifest, SourceSnapshot: durableRoutingSnapshot(manifest, fence.OwnerEpoch, now),
		Units: []heteroci.ExecutionUnit{{UnitID: "unit-a", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 * 1024 * 1024}}, HeartbeatTTL: 30 * time.Second,
	}
}

func topologyPlanOperationID(t *testing.T, runID heteroci.TopologyRunID) heteroci.OperationID {
	t.Helper()
	operationID, err := heteroci.DeriveTopologyOperationID(runID, heteroci.TopologyOperationPhasePlan, "")
	if err != nil {
		t.Fatalf("DeriveTopologyOperationID: %v", err)
	}
	return operationID
}

const (
	durableRoutingNowUnixMilli = int64(1_700_000_000_000)
	durableRoutingMemoryBytes  = int64(8 * 1024 * 1024 * 1024)
)

func durablePhysicalHostAttestation() heteroci.PhysicalHostAttestation {
	attestation := heteroci.PhysicalHostAttestation{
		SchemaVersion:      heteroci.PhysicalHostAttestationSchemaVersion,
		OSID:               "ubuntu",
		Machine:            "x86_64",
		MachineIDSHA256:    "sha256:" + strings.Repeat("1", 64),
		OSReleaseIdentity:  "sha256:" + strings.Repeat("2", 64),
		UnameIdentity:      "sha256:" + strings.Repeat("3", 64),
		DockerOSType:       "linux",
		DockerArchitecture: "x86_64",
	}
	attestation.Digest = attestation.CanonicalDigest()
	return attestation
}

func validOwnerScope() heteroci.RoutingOwnerScope {
	return heteroci.RoutingOwnerScope("sha256:" + strings.Repeat("a", 64))
}
func durableRoutingManifest() heteroci.Manifest {
	return heteroci.Manifest{SchemaVersion: heteroci.ManifestSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative, Nodes: []heteroci.NodeConfig{
		{NodeID: "node-a", Endpoint: "https://node-a.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("1", 64), Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("2", 64)}}},
		{NodeID: "node-b", Endpoint: "https://node-b.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("3", 64), Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("2", 64)}}},
		{NodeID: "node-c", Endpoint: "https://node-c.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("4", 64), Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("2", 64)}}},
		{NodeID: "node-d", Endpoint: "https://node-d.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("5", 64), Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("2", 64)}}},
	}}
}
func durableRoutingSnapshot(manifest heteroci.Manifest, ownerEpoch heteroci.RoutingOwnerEpoch, now time.Time) heteroci.SnapshotSet {
	config := manifest.Nodes[0]
	capacity := heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: durableRoutingMemoryBytes, MaxConcurrency: 4}
	return heteroci.SnapshotSet{SchemaVersion: heteroci.SnapshotSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative, OwnerEpoch: ownerEpoch, CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations, CapturedAtUnixMilli: now.UnixMilli(), Nodes: []heteroci.NodeSnapshot{
		{NodeID: config.NodeID, Endpoint: config.Endpoint, Platform: config.Platform, ProbeIdentity: config.ProbeIdentity, PhysicalAttestation: durablePhysicalHostAttestation(), ProbeBootID: "boot-a", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: config.Capabilities[0].ImageDigest}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
		{NodeID: manifest.Nodes[1].NodeID, Endpoint: manifest.Nodes[1].Endpoint, Platform: manifest.Nodes[1].Platform, ProbeIdentity: manifest.Nodes[1].ProbeIdentity, PhysicalAttestation: durablePhysicalHostAttestation(), ProbeBootID: "boot-b", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: manifest.Nodes[1].Capabilities[0].ImageDigest}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
		{NodeID: manifest.Nodes[2].NodeID, Endpoint: manifest.Nodes[2].Endpoint, Platform: manifest.Nodes[2].Platform, ProbeIdentity: manifest.Nodes[2].ProbeIdentity, PhysicalAttestation: durablePhysicalHostAttestation(), ProbeBootID: "boot-c", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: manifest.Nodes[2].Capabilities[0].ImageDigest}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
		{NodeID: manifest.Nodes[3].NodeID, Endpoint: manifest.Nodes[3].Endpoint, Platform: manifest.Nodes[3].Platform, ProbeIdentity: manifest.Nodes[3].ProbeIdentity, PhysicalAttestation: durablePhysicalHostAttestation(), ProbeBootID: "boot-d", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: manifest.Nodes[3].Capabilities[0].ImageDigest}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
	}}
}
func topologyRunIdentity(t *testing.T, repositoryID, sourceTreeSHA, callerRunID string) heteroci.TopologyRunIdentity {
	t.Helper()
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(repositoryID, sourceTreeSHA, callerRunID)
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity: %v", err)
	}
	return runIdentity
}
