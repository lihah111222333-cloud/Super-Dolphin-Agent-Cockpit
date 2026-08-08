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
	if _, err := projectCompileGroupsForShard(shard, []gate.CompileGroup{firstGroup, secondGroup}); err == nil || !strings.Contains(err.Error(), "exactly one compile group") {
		t.Fatalf("different artifacts were projected into one shard: %v", err)
	}
}
