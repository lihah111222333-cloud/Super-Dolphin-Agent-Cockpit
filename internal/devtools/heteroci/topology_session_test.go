package heteroci

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTopologySessionContract(t *testing.T) {
	t.Run("scope excludes target repository", testDeriveTopologyOwnerScopeDoesNotBindTargetRepository)
	t.Run("revision is storage owned", testTopologySessionAcceptsCurrentRevisionWithinIncarnation)
	t.Run("request field guard", testTopologyPlanRequestFieldGuardAndValidation)
	t.Run("storage neutral port", testTopologySessionMutatorPortIsStorageNeutral)
}

func testDeriveTopologyOwnerScopeDoesNotBindTargetRepository(t *testing.T) {
	manifest := topologySessionManifest()
	session, firstRequest := topologyPlanFixture(t)
	secondRequest := firstRequest
	secondRequest.RepositoryID = "repo-b"
	secondRequest.SourceTreeSHA = strings.Repeat("c", 40)
	if err := firstRequest.Validate(session); err != nil {
		t.Fatalf("first target request: %v", err)
	}
	if err := secondRequest.Validate(session); err != nil {
		t.Fatalf("second target request: %v", err)
	}
	first, err := DeriveTopologyOwnerScope("shared-pool", manifest)
	if err != nil {
		t.Fatalf("DeriveTopologyOwnerScope: %v", err)
	}
	second, err := DeriveTopologyOwnerScope("shared-pool", manifest)
	if err != nil {
		t.Fatalf("DeriveTopologyOwnerScope repeat: %v", err)
	}
	if first != second {
		t.Fatalf("same topology scope drifted: first=%q second=%q", first, second)
	}
	if different, err := DeriveTopologyOwnerScope("another-pool", manifest); err != nil || different == first {
		t.Fatalf("different namespace scope=%q error=%v", different, err)
	}
}

func testTopologySessionAcceptsCurrentRevisionWithinIncarnation(t *testing.T) {
	acquired := topologySessionFence(t, 1, "1")
	session, err := NewTopologySession(acquired)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	if err := session.ValidateMutationFence(topologySessionFence(t, 9, "1")); err != nil {
		t.Fatalf("ValidateMutationFence current revision: %v", err)
	}
	if err := session.ValidateMutationFence(topologySessionFence(t, 9, "2")); err == nil {
		t.Fatal("ValidateMutationFence accepted another owner incarnation")
	}
}

func testTopologyPlanRequestFieldGuardAndValidation(t *testing.T) {
	typeOf := reflect.TypeFor[TopologyPlanRequest]()
	want := []string{"OperationID", "RepositoryID", "SourceTreeSHA", "RunID", "Manifest", "SourceSnapshot", "Units", "HeartbeatTTL"}
	got := make([]string, 0, typeOf.NumField())
	for field := range typeOf.Fields() {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TopologyPlanRequest fields = %v, want %v", got, want)
	}
	session, request := topologyPlanFixture(t)
	if err := request.Validate(session); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	request.RepositoryID = ""
	if err := request.Validate(session); err == nil {
		t.Fatal("Validate accepted missing repository audit identity")
	}
}

func testTopologySessionMutatorPortIsStorageNeutral(t *testing.T) {
	var mutator TopologySessionMutator = topologySessionMutatorStub{}
	session, request := topologyPlanFixture(t)
	result, err := mutator.PlanAndReserve(context.Background(), session, request)
	if err != nil || result.Fence.OwnerEpoch != session.OwnerEpoch {
		t.Fatalf("storage-neutral mutator result=%#v error=%v", result, err)
	}
}

type topologySessionMutatorStub struct{}

func (topologySessionMutatorStub) PlanAndReserve(_ context.Context, session TopologySession, _ TopologyPlanRequest) (TopologyReservedPlan, error) {
	fence, err := NewOwnerFence(session.Scope, session.OwnerID, session.OwnerEpoch, 2)
	return TopologyReservedPlan{Fence: fence}, err
}

func topologyPlanFixture(t *testing.T) (TopologySession, TopologyPlanRequest) {
	t.Helper()
	fence := topologySessionFence(t, 1, "1")
	session, err := NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	manifest := topologySessionManifest()
	now := time.UnixMilli(1_700_000_000_000)
	capacity := ResourceCapacity{CPUMilli: 1_000, MemoryBytes: 1 << 30, MaxConcurrency: 1}
	request := TopologyPlanRequest{
		OperationID:  OperationID(strings.Repeat("a", OperationIDHexLength)),
		RepositoryID: "repo-a", SourceTreeSHA: strings.Repeat("b", 40), RunID: "repo-a/run",
		Manifest: manifest,
		SourceSnapshot: SnapshotSet{
			SchemaVersion: SnapshotSchemaVersion, Authority: AuthorityLocalNonAuthoritative, OwnerEpoch: session.OwnerEpoch,
			CapacityBasis: SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations, CapturedAtUnixMilli: now.UnixMilli(),
			Nodes: []NodeSnapshot{{
				NodeID: "node-a", Endpoint: "https://node-a.invalid/runner", Platform: PlatformLinuxAMD64,
				ProbeIdentity: "sha256:" + strings.Repeat("1", 64), ProbeBootID: "boot-a", Status: NodeStatusLive,
				HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity,
				ActiveCapabilities: []ActiveCapabilitySnapshot{{Runtime: RuntimeUbuntuContainer, ImageDigest: "sha256:" + strings.Repeat("2", 64)}},
			}},
		},
		Units:        []ExecutionUnit{{UnitID: "unit-a", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 << 20}},
		HeartbeatTTL: 30 * time.Second,
	}
	return session, request
}

func topologySessionFence(t *testing.T, revision RoutingOwnerRevision, ownerDigit string) OwnerFence {
	t.Helper()
	fence, err := NewOwnerFence(
		RoutingOwnerScope("sha256:"+strings.Repeat("a", 64)),
		RoutingOwnerID(strings.Repeat(ownerDigit, RoutingOwnerIDHexLength)),
		1,
		revision,
	)
	if err != nil {
		t.Fatalf("NewOwnerFence: %v", err)
	}
	return fence
}

func topologySessionManifest() Manifest {
	return Manifest{
		SchemaVersion: ManifestSchemaVersion, Authority: AuthorityLocalNonAuthoritative,
		Nodes: []NodeConfig{{
			NodeID: "node-a", Endpoint: "https://node-a.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("1", 64), Platform: PlatformLinuxAMD64,
			Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("2", 64)}},
		}},
	}
}
