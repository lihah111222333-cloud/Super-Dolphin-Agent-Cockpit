package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestProjectCompileGroupsForShardRejectsDifferentArtifacts(t *testing.T) {
	first, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestCoordinatorArtifactFirst", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCoordinatorArtifactSecond", 10)
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := gate.GateID(first.ID), gate.GateID(second.ID)
	firstGroup := compileBindingGroup(t, firstID)
	secondGroup := gate.CompileGroup{
		PackageTarget: "./internal/devtools/gate", SemanticKey: gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("c", 64), ProfileDigest: "sha256:" + strings.Repeat("d", 64),
		ResourceClassID: "small", WorkloadIDs: []gate.GateID{secondID}, CompileEstimateMS: 10,
		BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	finalizeTestCompileGroup(t, &secondGroup)
	shard := gate.ContainerShard{Index: 0, GateIDs: []gate.GateID{firstID, secondID}}
	if _, err := projectCompileGroupsForShard(shard, []gate.CompileGroup{firstGroup, secondGroup}); err == nil || !strings.Contains(err.Error(), "not eligible for same-resource serial packing") {
		t.Fatalf("different artifacts were projected into one shard: %v", err)
	}
}

func TestProjectCompileGroupsForShardAcceptsSameResourceOrdinaryGroups(t *testing.T) {
	first, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCoordinatorOrdinaryFirst", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCoordinatorOrdinarySecond", 10)
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := gate.GateID(first.ID), gate.GateID(second.ID)
	firstGroup := ordinaryCompileGroup(t, firstID, "a", "b")
	secondGroup := ordinaryCompileGroup(t, secondID, "c", "d")
	if firstGroup.GroupID >= secondGroup.GroupID {
		firstGroup, secondGroup = secondGroup, firstGroup
	}
	shard := gate.ContainerShard{Index: 0, GateIDs: []gate.GateID{firstID, secondID}}
	projected, err := projectCompileGroupsForShard(shard, []gate.CompileGroup{firstGroup, secondGroup})
	if err != nil {
		t.Fatalf("same-resource ordinary groups rejected: %v", err)
	}
	if len(projected) != 2 || projected[0].GroupID != firstGroup.GroupID || projected[1].GroupID != secondGroup.GroupID {
		t.Fatalf("projected groups = %#v, want canonical two-group order", projected)
	}
}

func ordinaryCompileGroup(t *testing.T, workloadID gate.GateID, inputByte, profileByte string) gate.CompileGroup {
	t.Helper()
	group := gate.CompileGroup{
		PackageTarget: "./internal/devtools/gate", SemanticKey: gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat(inputByte, 64), ProfileDigest: "sha256:" + strings.Repeat(profileByte, 64),
		ResourceClassID: "small", WorkloadIDs: []gate.GateID{workloadID}, CompileEstimateMS: 10,
		BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	finalizeTestCompileGroup(t, &group)
	if !gate.CompileGroupSerialPackingEligible(group) {
		t.Fatal("ordinary compile group fixture is not packing eligible")
	}
	return group
}
